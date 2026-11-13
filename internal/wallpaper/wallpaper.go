// Package wallpaper applies a wallpaper (macOS or Linux/Wayland), records
// what was set, extracts/applies native colors, and runs the optional post-hook.
package wallpaper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kwall/internal/colors"
	"kwall/internal/config"
)

const hookTimeout = 15 * time.Second

// backend picks the wallpaper setter: $KWALL_BACKEND if set, then cfgBackend,
// otherwise the default for this OS (plist on macOS, awww on Linux).
func backend(cfgBackend string) string {
	b := strings.ToLower(strings.TrimSpace(os.Getenv("KWALL_BACKEND")))
	if b == "" {
		b = strings.ToLower(strings.TrimSpace(cfgBackend))
	}
	switch b {
	case "", "auto":
		switch runtime.GOOS {
		case "darwin":
			return "plist"
		case "linux":
			return "awww"
		}
		return ""
	case "macos":
		return "plist"
	default:
		return b
	}
}

// Set changes the wallpaper using the environment or OS default backend.
func Set(abs string) error {
	return SetWithConfig(nil, abs)
}

// SetWithConfig changes the wallpaper using cfg's backend (overridden by $KWALL_BACKEND).
func SetWithConfig(cfg *config.Config, abs string) error {
	cfgBackend := ""
	if cfg != nil {
		cfgBackend = cfg.Backend
	}
	switch b := backend(cfgBackend); b {
	case "plist":
		return setPlist(abs)
	case "osascript":
		return setOsascript(abs)
	case "wallpaper":
		return setWallpaperCLI(abs)
	case "awww", "swww":
		return setAwww(abs)
	case "":
		return fmt.Errorf("no wallpaper backend for %s; set KWALL_BACKEND to plist, osascript, wallpaper or awww", runtime.GOOS)
	default:
		return fmt.Errorf("unknown KWALL_BACKEND %q (use plist, osascript, wallpaper or awww)", b)
	}
}

// Finish saves the current wallpaper state, extracts & renders native colors
// when enabled, and runs the optional post-hook.
func Finish(cfg *config.Config, abs, thumb string) {
	if err := WriteCurrent(cfg, abs); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: could not save state: %v\n", err)
	}
	if cfg.Colors.Enabled {
		img := abs
		if thumb != "" {
			img = thumb
		}
		if err := colors.Run(cfg, img, abs); err != nil {
			fmt.Fprintf(os.Stderr, "kwall: warning: colors: %v\n", err)
		}
	}
	if err := RunHook(cfg, abs, thumb); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: post-hook: %v\n", err)
	}
}

// Apply sets the wallpaper, records it as the current one, generates colors
// when enabled, and runs the optional post-hook.
func Apply(cfg *config.Config, abs, thumb string) error {
	if err := SetWithConfig(cfg, abs); err != nil {
		return err
	}
	Finish(cfg, abs, thumb)
	return nil
}

// WriteCurrent stores the last wallpaper set by kwall.
func WriteCurrent(cfg *config.Config, abs string) error {
	if err := os.MkdirAll(filepath.Dir(cfg.StateFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.StateFile, []byte(abs+"\n"), 0o644)
}

// ReadCurrent returns the last wallpaper set by kwall, or "" if none.
func ReadCurrent(cfg *config.Config) (string, error) {
	b, err := os.ReadFile(cfg.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// hookPath returns the post-hook script if one exists.
func hookPath(cfg *config.Config) string {
	if cfg.Reload.PostHook != "" {
		if fi, err := os.Stat(cfg.Reload.PostHook); err == nil && !fi.IsDir() {
			return cfg.Reload.PostHook
		}
		return ""
	}
	for _, name := range []string{"post-hook.sh", "post-hook"} {
		p := filepath.Join(cfg.ConfigDir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// HasHook reports whether a post-hook script exists.
func HasHook(cfg *config.Config) bool {
	return hookPath(cfg) != ""
}

// RunHook runs the configured post-hook script (or ~/.config/kwall/post-hook.sh)
// with the new wallpaper path as $1 ($KWALL_WALLPAPER) and the cached thumbnail
// as $2 ($KWALL_THUMBNAIL) if non-empty.
func RunHook(cfg *config.Config, abs, thumb string) error {
	p := hookPath(cfg)
	if p == "" {
		return nil
	}
	if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 == 0 {
		return fmt.Errorf("%s exists but is not executable (chmod +x it)", p)
	}
	args := []string{abs}
	env := append(os.Environ(), "KWALL_WALLPAPER="+abs)
	if thumb != "" {
		args = append(args, thumb)
		env = append(env, "KWALL_THUMBNAIL="+thumb)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p, args...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s timed out after %s", p, hookTimeout)
		}
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}
