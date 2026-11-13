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
  kwall cache [--prune]  pre-generate thumbnails (and prune stale entries)
  kwall version          print the version

Configuration:
  Optional ~/.config/kwall/config.toml (CLI flags > env vars > config.toml > defaults).

Environment:
  WALLPAPER_DIR          wallpaper directory (default ~/Pictures/Wallpapers)
  KWALL_BACKEND          "auto" (default), "plist"/"macos" (macOS 14+), "osascript",
                         "awww" (Linux), or "wallpaper" (active Space only)
  KWALL_AWWW_ARGS        extra flags for "awww img", e.g. "--resize fit --outputs DP-1"
  KWALL_COLORS=1/0       enable/disable native color extraction (default on)
  KWALL_COLOR_MODE       "dark" (default), "light", or "auto"
  KWALL_ALPHA            opacity percentage 0..100 (default 100)
  KWALL_THUMB_SIZE       thumbnail longest edge in px (default 1000)
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
		return cmdCache(cfg, args[1:])
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
	// Overlap thumbnail generation (needed for fast color extraction and hooks)
	// with setting the wallpaper so cold-cache runs don't pay both sequentially.
	var thumbCh chan string
	if cfg.Colors.Enabled || wallpaper.HasHook(cfg) {
		thumbCh = make(chan string, 1)
		go func() {
			t, _, err := preview.EnsureThumb(cfg, abs)
			if err != nil {
				t = ""
			}
			thumbCh <- t
		}()
	}

	if err := wallpaper.SetWithConfig(cfg, abs); err != nil {
		if thumbCh != nil {
			<-thumbCh
		}
		return err
	}

	thumb := ""
	if thumbCh != nil {
		thumb = <-thumbCh
	}
	wallpaper.Finish(cfg, abs, thumb)
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

func cmdCache(cfg *config.Config, args []string) error {
	prune := cfg.Cache.AutoPrune
	for _, a := range args {
		switch a {
		case "--prune":
			prune = true
		case "--no-prune":
			prune = false
		default:
			return fmt.Errorf("unknown flag %q for cache (use --prune or --no-prune)", a)
		}
	}
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
	pruned := 0
	if prune {
		if n, err := preview.PruneCache(cfg, files); err == nil {
			pruned = n
		}
	}
	fmt.Printf("Done in %s: %d created, %d already cached, %d pruned, %d failed\n",
		time.Since(start).Round(time.Millisecond), st.Created, st.Cached, pruned, st.Failed)
	if st.Failed > 0 {
		return fmt.Errorf("%d thumbnails failed", st.Failed)
	}
	return nil
}
