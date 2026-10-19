// kwall - Kitty Wallpaper Switcher for macOS and Linux (Wayland/Hyprland).
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"kwall/internal/config"
	"kwall/internal/fzf"
	"kwall/internal/preview"
	"kwall/internal/pywal"
	"kwall/internal/wallpaper"
)

var version = "dev"

const cacheWorkers = 8

const usage = `kwall - switch wallpapers from the Kitty terminal (macOS and Linux)

Usage:
  kwall                  pick a wallpaper with fzf and a live image preview
  kwall set <path>       set a specific image as the wallpaper
  kwall random           set a random wallpaper from the wallpaper directory
  kwall current          show the current wallpaper path (and preview in Kitty)
  kwall cache            pre-generate thumbnails for the whole directory
  kwall version          print the version

Environment:
  WALLPAPER_DIR          wallpaper directory (default ~/Pictures/Wallpapers)
  KWALL_BACKEND          osascript (macOS default), awww (Linux default), or wallpaper
  KWALL_AWWW_ARGS        extra flags for "awww img", e.g. "--resize fit --outputs DP-1"
  KWALL_PYWAL=1          run pywal after each change (colours follow the wallpaper)
  KWALL_PYWAL_ARGS       extra flags for wal, e.g. "-a 85 --backend colorz"
  KWALL_UNICODE_PLACEHOLDER=0   disable kitty's unicode placeholder mode in previews

Post-hook: an executable ~/.config/kwall/post-hook.sh is run after each change
with the wallpaper path as $1 and a small cached JPEG thumbnail as $2.
`

func main() {
	err := run(os.Args[1:])
	if err == nil || errors.Is(err, fzf.ErrCancelled) {
		return
	}
	fmt.Fprintln(os.Stderr, "kwall:", err)
	os.Exit(1)
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Print(usage)
			return nil
		case "version", "-v", "--version":
			fmt.Println("kwall", version)
			return nil
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return cmdPick(cfg)
	}
	switch args[0] {
	case "set":
		return cmdSet(cfg, args[1:])
	case "random":
		return cmdRandom(cfg)
	case "current":
		return cmdCurrent(cfg)
	case "cache":
		return cmdCache(cfg)
	case "preview": // hidden: called by fzf --preview
		if len(args) != 2 {
			return errors.New("usage: kwall preview <rel-path>")
		}
		return preview.Render(cfg, args[1])
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func listImages(cfg *config.Config) ([]string, error) {
	files, err := config.ListImages(cfg.WallpaperDir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no images found in %s", cfg.WallpaperDir)
	}
	return files, nil
}

func apply(cfg *config.Config, abs string) error {
	// Hand pywal and the hook a small cached thumbnail (usually already
	// cached by the preview), so image analysis runs fast.
	thumb := ""
	if pywal.Enabled() || wallpaper.HasHook(cfg) {
		if t, _, err := preview.EnsureThumb(cfg, abs); err == nil {
			thumb = t
		}
	}
	if err := wallpaper.Apply(cfg, abs, thumb); err != nil {
		return err
	}
	fmt.Println("Wallpaper set:", abs)
	return nil
}

func cmdPick(cfg *config.Config) error {
	files, err := listImages(cfg)
	if err != nil {
		return err
	}
	sel, err := fzf.Pick(cfg, files)
	preview.Clear()
	if err != nil {
		return err
	}
	return apply(cfg, cfg.ResolveRel(sel))
}

func cmdSet(cfg *config.Config, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: kwall set <path>")
	}
	abs, err := cfg.ResolveInput(args[0])
	if err != nil {
		return err
	}
	return apply(cfg, abs)
}

func cmdRandom(cfg *config.Config) error {
	files, err := listImages(cfg)
	if err != nil {
		return err
	}
	// Avoid picking the wallpaper that is already set, when there is a choice.
	if cur, _ := wallpaper.ReadCurrent(cfg); cur != "" && len(files) > 1 {
		filtered := files[:0:0]
		for _, f := range files {
			if cfg.ResolveRel(f) != cur {
				filtered = append(filtered, f)
			}
		}
		if len(filtered) > 0 {
			files = filtered
		}
	}
	return apply(cfg, cfg.ResolveRel(files[rand.IntN(len(files))]))
}

func cmdCurrent(cfg *config.Config) error {
	cur, err := wallpaper.ReadCurrent(cfg)
	if err != nil {
		return err
	}
	if cur == "" {
		return errors.New("no wallpaper has been set with kwall yet")
	}
	fmt.Println(cur)
	if preview.InKitty() {
		if _, err := os.Stat(cur); err == nil {
			return preview.Show(cfg, cur)
		}
	}
	return nil
}

func cmdCache(cfg *config.Config) error {
	if !preview.HasThumbnailer() {
		return preview.ErrNoThumbnailer
	}
	files, err := listImages(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Caching thumbnails for %d images with %d workers...\n", len(files), cacheWorkers)
	start := time.Now()
	st := preview.WarmCache(cfg, files, cacheWorkers, os.Stderr)
	fmt.Printf("Done in %s: %d created, %d already cached, %d failed\n",
		time.Since(start).Round(time.Millisecond), st.Created, st.Cached, st.Failed)
	if st.Failed > 0 {
		return fmt.Errorf("%d thumbnails failed", st.Failed)
	}
	return nil
}
