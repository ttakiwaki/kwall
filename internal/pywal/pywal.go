// Package pywal runs pywal (or its fork pywal16) after a wallpaper change so
// terminal and desktop colours follow the wallpaper.
package pywal

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

// Enabled reports whether the user opted in with KWALL_PYWAL=1.
func Enabled() bool {
	switch strings.ToLower(os.Getenv("KWALL_PYWAL")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Args builds the `wal` arguments for img.
//
// Base flags: -n (kwall already set the wallpaper) and -q (no output). On
// macOS -e is added too: it skips reloading Linux desktop pieces (gtk, xrdb,
// i3, sway, polybar) that do not exist there. extra is the user's
// KWALL_PYWAL_ARGS, e.g. "-a 85 --backend colorz", added before -i.
func Args(img, goos, extra string) []string {
	args := []string{"-n", "-q"}
	if goos == "darwin" {
		args = append(args, "-e")
	}
	args = append(args, strings.Fields(extra)...)
	return append(args, "-i", img)
}

// WalCacheDir is where pywal writes its generated files (colors-kitty.conf,
// colors.json, ...). pywal honours $XDG_CACHE_HOME, like kwall.
func WalCacheDir(cfg *config.Config) string {
	return filepath.Join(filepath.Dir(cfg.CacheDir), "wal")
}

// Run generates a colour scheme from img (a small thumbnail is best: pywal
// analyses the pixels, so a 1000px JPEG is much faster than a 5K original)
// and, when running inside Kitty, reloads its colours live.
func Run(cfg *config.Config, img string) error {
	bin, err := exec.LookPath("wal")
	if err != nil {
		return errors.New("KWALL_PYWAL is set but `wal` is not in PATH (install pywal or pywal16, e.g. `pipx install pywal16`)")
	}
	out, err := exec.Command(bin, Args(img, runtime.GOOS, os.Getenv("KWALL_PYWAL_ARGS"))...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("wal failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	reloadKitty(cfg)
	return nil
}

// reloadKitty pushes pywal's colours into every open Kitty window. It needs
// `allow_remote_control` in kitty.conf, so failure is silent: pywal's own
// terminal escape sequences already update most terminals.
func reloadKitty(cfg *config.Config) {
	if os.Getenv("KITTY_WINDOW_ID") == "" {
		return
	}
	conf := filepath.Join(WalCacheDir(cfg), "colors-kitty.conf")
	if _, err := os.Stat(conf); err != nil {
		return
	}
	_ = exec.Command("kitten", "@", "set-colors", "--all", "--configured", conf).Run()
}
