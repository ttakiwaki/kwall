// Package wallpaper applies a wallpaper (macOS or Linux/Wayland), records
// what was set and runs the pywal integration and the optional post-hook.
package wallpaper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"kwall/internal/config"
	"kwall/internal/pywal"
)

// backend picks the wallpaper setter: $KWALL_BACKEND if set, otherwise the
// default for this OS (osascript on macOS, awww on Linux).
func backend() string {
	if b := strings.ToLower(strings.TrimSpace(os.Getenv("KWALL_BACKEND"))); b != "" {
		return b
	}
	switch runtime.GOOS {
	case "darwin":
		return "osascript"
	case "linux":
		return "awww"
	}
	return ""
}

// Set changes the wallpaper using the selected backend.
func Set(abs string) error {
	switch b := backend(); b {
	case "osascript":
		return setOsascript(abs)
	case "wallpaper":
		return setWallpaperCLI(abs)
	case "awww", "swww":
		return setAwww(abs)
	case "":
		return fmt.Errorf("no wallpaper backend for %s; set KWALL_BACKEND to osascript, wallpaper or awww", runtime.GOOS)
	default:
		return fmt.Errorf("unknown KWALL_BACKEND %q (use osascript, wallpaper or awww)", b)
	}
}

// Apply sets the wallpaper, records it as the current one, runs pywal when
// enabled and then the optional post-hook. thumb is a small cached JPEG of
// the wallpaper ("" if none). Only a failure to set the wallpaper is fatal.
func Apply(cfg *config.Config, abs, thumb string) error {
	if err := Set(abs); err != nil {
		return err
	}
	if err := WriteCurrent(cfg, abs); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: could not save state: %v\n", err)
	}
	if pywal.Enabled() {
		img := abs
		if thumb != "" {
			img = thumb
		}
		if err := pywal.Run(cfg, img); err != nil {
			fmt.Fprintf(os.Stderr, "kwall: warning: pywal: %v\n", err)
		}
	}
	if err := RunHook(cfg, abs, thumb); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: post-hook: %v\n", err)
	}
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

// RunHook runs ~/.config/kwall/post-hook.sh (or post-hook) with the new
// wallpaper path as $1 (also in $KWALL_WALLPAPER). If thumb is non-empty it
// is a small cached JPEG of the wallpaper, passed as $2 (also in
// $KWALL_THUMBNAIL); tools that analyse the image run much faster on it than
// on a full-size 4K/5K original.
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
	cmd := exec.Command(p, args...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}
