package colors

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"kwall/internal/config"
)

const kittyReloadTimeout = 5 * time.Second

// Run extracts a 16-color palette from imgPath (preferring a small cached
// thumbnail), writes all configured templates to cfg.WalCacheDir, and reloads
// Kitty colors live when enabled and running inside Kitty.
func Run(cfg *config.Config, imgPath, wallpaperPath string) error {
	p, err := Extract(imgPath, wallpaperPath, Options{
		Mode:       cfg.Colors.Mode,
		Saturation: cfg.Colors.Saturation,
		Contrast:   cfg.Colors.Contrast,
		Alpha:      cfg.Colors.Alpha,
	})
	if err != nil {
		return err
	}
	if err := RenderAll(cfg, p); err != nil {
		return err
	}
	if cfg.Reload.Kitty {
		reloadKitty(cfg)
	}
	return nil
}

// reloadKitty pushes generated colors into every open Kitty window. It needs
// `allow_remote_control` in kitty.conf, so failure is silent.
func reloadKitty(cfg *config.Config) {
	if os.Getenv("KITTY_WINDOW_ID") == "" {
		return
	}
	conf := filepath.Join(cfg.WalCacheDir, "colors-kitty.conf")
	if _, err := os.Stat(conf); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), kittyReloadTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "kitten", "@", "set-colors", "--all", "--configured", conf).Run()
}
