// Package config resolves directories, environment variables and image
// discovery for kwall.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Extensions lists the image formats kwall offers as wallpapers
// (lowercase, including the leading dot).
var Extensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".heic": true,
}

// ColorsConfig controls native Go palette extraction.
type ColorsConfig struct {
	Enabled    bool
	Mode       string  // "dark" | "light" | "auto"
	Saturation float64 // multiplier >= 0 (default 1.0)
	Contrast   float64 // multiplier >= 0 (default 1.0)
	Alpha      int     // opacity 0..100 (default 100)
}

// TemplatesConfig controls extra embedded templates rendered to ~/.cache/wal.
type TemplatesConfig struct {
	Enabled []string
}

// ReloadConfig controls terminal reload and post-hook execution.
type ReloadConfig struct {
	Kitty    bool
	PostHook string
}

// CacheConfig controls thumbnail generation and pruning.
type CacheConfig struct {
	ThumbSize int
	AutoPrune bool
}

// Config holds every path and setting kwall needs.
type Config struct {
	WallpaperDir string // where wallpapers live ($WALLPAPER_DIR)
	Backend      string // "auto" | "macos" | "plist" | "osascript" | "wallpaper" | "awww" | "swww"
	ConfigDir    string // ~/.config/kwall (config.toml and post-hook live here)
	WalConfigDir string // ~/.config/wal (user templates live here)
	CacheDir     string // ~/.cache/kwall
	WalCacheDir  string // ~/.cache/wal (rendered color files live here)
	ThumbDir     string // ~/.cache/kwall/thumbs
	StateFile    string // ~/.local/state/kwall/current
	ConfigFile   string // ~/.config/kwall/config.toml

	Colors    ColorsConfig
	Templates TemplatesConfig
	Reload    ReloadConfig
	Cache     CacheConfig
}

// Load builds a Config from built-in defaults, optional ~/.config/kwall/config.toml,
// and environment variable overrides (CLI flags > env vars > config file > defaults).
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	xdgConfig := xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	xdgCache := xdg("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	xdgState := xdg("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))

	configDir := filepath.Join(xdgConfig, "kwall")
	cacheDir := filepath.Join(xdgCache, "kwall")
	stateDir := filepath.Join(xdgState, "kwall")

	cfg := &Config{
		WallpaperDir: filepath.Join(home, "Pictures", "Wallpapers"),
		Backend:      "auto",
		ConfigDir:    configDir,
		WalConfigDir: filepath.Join(xdgConfig, "wal"),
		CacheDir:     cacheDir,
		WalCacheDir:  filepath.Join(xdgCache, "wal"),
		ThumbDir:     filepath.Join(cacheDir, "thumbs"),
		StateFile:    filepath.Join(stateDir, "current"),
		ConfigFile:   filepath.Join(configDir, "config.toml"),
		Colors: ColorsConfig{
			Enabled:    true,
			Mode:       "dark",
			Saturation: 1.0,
			Contrast:   1.0,
			Alpha:      100,
		},
		Templates: TemplatesConfig{
			Enabled: []string{"kitty"},
		},
		Reload: ReloadConfig{
			Kitty: true,
		},
		Cache: CacheConfig{
			ThumbSize: 1000,
			AutoPrune: true,
		},
	}

	if err := cfg.loadTOML(cfg.ConfigFile); err != nil {
		return nil, err
	}
	cfg.applyEnvOverrides()

	cfg.WallpaperDir = expandHome(cfg.WallpaperDir, home)
	if abs, err := filepath.Abs(cfg.WallpaperDir); err == nil {
		cfg.WallpaperDir = abs
	}
	if cfg.Reload.PostHook != "" {
		cfg.Reload.PostHook = expandHome(cfg.Reload.PostHook, home)
	}
	return cfg, nil
}

func (c *Config) loadTOML(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	data, err := parseTOML(string(raw))
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if v, ok, err := data.getString("wallpaper.dir"); err != nil {
		return err
	} else if ok && v != "" {
		c.WallpaperDir = v
	}
	if v, ok, err := data.getString("wallpaper.backend"); err != nil {
		return err
	} else if ok && v != "" {
		c.Backend = strings.ToLower(strings.TrimSpace(v))
	}

	if v, ok, err := data.getBool("colors.enabled"); err != nil {
		return err
	} else if ok {
		c.Colors.Enabled = v
	}
	if v, ok, err := data.getString("colors.mode"); err != nil {
		return err
	} else if ok && v != "" {
		c.Colors.Mode = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok, err := data.getFloat("colors.saturation"); err != nil {
		return err
	} else if ok {
		c.Colors.Saturation = v
	}
	if v, ok, err := data.getFloat("colors.contrast"); err != nil {
		return err
	} else if ok {
		c.Colors.Contrast = v
	}
	if v, ok, err := data.getInt("colors.alpha"); err != nil {
		return err
	} else if ok {
		c.Colors.Alpha = clampInt(v, 0, 100)
	}

	if v, ok, err := data.getStringSlice("templates.enabled"); err != nil {
		return err
	} else if ok {
		c.Templates.Enabled = v
	}

	if v, ok, err := data.getBool("reload.kitty"); err != nil {
		return err
	} else if ok {
		c.Reload.Kitty = v
	}
	if v, ok, err := data.getString("reload.post_hook"); err != nil {
		return err
	} else if ok {
		c.Reload.PostHook = strings.TrimSpace(v)
	}

	if v, ok, err := data.getInt("cache.thumb_size"); err != nil {
		return err
	} else if ok && v > 0 {
		c.Cache.ThumbSize = v
	}
	if v, ok, err := data.getBool("cache.auto_prune"); err != nil {
		return err
	} else if ok {
		c.Cache.AutoPrune = v
	}
	return nil
}

func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("WALLPAPER_DIR"); v != "" {
		c.WallpaperDir = v
	}
	if v := strings.TrimSpace(os.Getenv("KWALL_BACKEND")); v != "" {
		c.Backend = strings.ToLower(v)
	}
	if b, ok := envBool("KWALL_COLORS"); ok {
		c.Colors.Enabled = b
	} else if b, ok := envBool("KWALL_PYWAL"); ok {
		c.Colors.Enabled = b
	}
	if v := strings.TrimSpace(os.Getenv("KWALL_COLOR_MODE")); v != "" {
		c.Colors.Mode = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("KWALL_ALPHA")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Colors.Alpha = clampInt(n, 0, 100)
		}
	}
	if b, ok := envBool("KWALL_RELOAD_KITTY"); ok {
		c.Reload.Kitty = b
	}
	if v := strings.TrimSpace(os.Getenv("KWALL_POST_HOOK")); v != "" {
		c.Reload.PostHook = v
	}
	if v := strings.TrimSpace(os.Getenv("KWALL_THUMB_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Cache.ThumbSize = n
		}
	}
}

func envBool(key string) (bool, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false, false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return fallback
}

func expandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// IsImage reports whether name has a supported image extension.
func IsImage(name string) bool {
	return Extensions[strings.ToLower(filepath.Ext(name))]
}

// ListImages walks dir and returns the supported images as sorted paths
// relative to dir. Hidden files and directories are skipped.
func ListImages(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("wallpaper directory %s does not exist (set $WALLPAPER_DIR to change it)", dir)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	// WalkDir does not follow a symlinked root, so resolve it first.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil // skip unreadable entries
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !IsImage(d.Name()) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if fi, err := os.Stat(path); err != nil || fi.IsDir() {
				return nil
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// ResolveRel turns a path from fzf (relative to the wallpaper directory)
// into an absolute path. Absolute paths are returned cleaned.
func (c *Config) ResolveRel(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(c.WallpaperDir, p)
}

// ResolveInput resolves a user-supplied path for `kwall set`. It tries the
// path as given (absolute, or relative to the current directory) and then
// relative to the wallpaper directory.
func (c *Config) ResolveInput(p string) (string, error) {
	if home, err := os.UserHomeDir(); err == nil {
		p = expandHome(p, home)
	}

	candidates := []string{}
	if filepath.IsAbs(p) {
		candidates = append(candidates, filepath.Clean(p))
	} else {
		if abs, err := filepath.Abs(p); err == nil {
			candidates = append(candidates, abs)
		}
		candidates = append(candidates, filepath.Join(c.WallpaperDir, p))
	}

	for _, cand := range candidates {
		fi, err := os.Stat(cand)
		if err != nil || fi.IsDir() {
			continue
		}
		if !IsImage(cand) {
			return "", fmt.Errorf("%s is not a supported image (jpg, jpeg, png, webp, heic)", cand)
		}
		return cand, nil
	}
	return "", fmt.Errorf("file not found: %s", p)
}
