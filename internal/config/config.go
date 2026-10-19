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

// Config holds every path kwall needs.
type Config struct {
	WallpaperDir string // where wallpapers live ($WALLPAPER_DIR)
	ConfigDir    string // ~/.config/kwall (post-hook lives here)
	CacheDir     string // ~/.cache/kwall
	ThumbDir     string // ~/.cache/kwall/thumbs
	StateFile    string // ~/.local/state/kwall/current
}

// Load builds a Config from the environment, honouring the XDG base
// directory variables and falling back to the paths from the project plan.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	dir := os.Getenv("WALLPAPER_DIR")
	if dir == "" {
		dir = filepath.Join(home, "Pictures", "Wallpapers")
	}
	dir = expandHome(dir, home)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	configDir := filepath.Join(xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "kwall")
	cacheDir := filepath.Join(xdg("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "kwall")
	stateDir := filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "kwall")

	return &Config{
		WallpaperDir: dir,
		ConfigDir:    configDir,
		CacheDir:     cacheDir,
		ThumbDir:     filepath.Join(cacheDir, "thumbs"),
		StateFile:    filepath.Join(stateDir, "current"),
	}, nil
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
