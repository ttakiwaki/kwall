// Package wallpaper applies a wallpaper on macOS and tracks kwall's state.
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
)

// osascriptArgs builds the osascript invocation. The path is passed as an
// argument (argv) rather than spliced into the script, so quotes, backslashes
// and unicode in file names can never break or inject into the AppleScript.
func osascriptArgs(abs string) []string {
	return []string{
		"-e", "on run argv",
		"-e", `tell application "System Events" to tell every desktop to set picture to (POSIX file (item 1 of argv))`,
		"-e", "end run",
		abs,
	}
}

// Set changes the wallpaper on every desktop/space.
//
// By default it uses System Events via osascript. Set KWALL_BACKEND=wallpaper
// to use the `wallpaper` CLI (https://github.com/sindresorhus/macos-wallpaper)
// when it is installed.
func Set(abs string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("setting the wallpaper is only supported on macOS")
	}

	if os.Getenv("KWALL_BACKEND") == "wallpaper" {
		bin, err := exec.LookPath("wallpaper")
		if err != nil {
			return errors.New("KWALL_BACKEND=wallpaper but the wallpaper CLI is not in PATH")
		}
		if out, err := exec.Command(bin, "set", abs).CombinedOutput(); err != nil {
			return fmt.Errorf("wallpaper set failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	out, err := exec.Command("osascript", osascriptArgs(abs)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript failed: %v: %s\n(if macOS asked for permission, allow your terminal to control System Events in System Settings > Privacy & Security > Automation)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Apply sets the wallpaper, records it as the current one and runs the
// optional post-hook. Only a failure to set the wallpaper is fatal.
func Apply(cfg *config.Config, abs, thumb string) error {
	if err := Set(abs); err != nil {
		return err
	}
	if err := WriteCurrent(cfg, abs); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: could not save state: %v\n", err)
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
// $KWALL_THUMBNAIL); tools that analyse the image, like pywal, run much
// faster on it than on a full-size 4K/5K original.
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
