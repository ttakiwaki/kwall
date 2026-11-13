// Package preview renders wallpaper previews with kitten icat and manages
// the thumbnail cache (generated with sips, ImageMagick or ffmpeg).
package preview

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kwall/internal/config"
)

// DefaultThumbSize is the default longest edge (in pixels) of cached thumbnails.
const DefaultThumbSize = 1000

// ThumbSize is kept as a string alias for backwards compatibility.
const ThumbSize = "1000"

const (
	thumbTimeout = 15 * time.Second
	icatTimeout  = 5 * time.Second
)

var tmpCounter atomic.Uint64

// ErrNoThumbnailer means no image tool for making thumbnails is installed.
var ErrNoThumbnailer = errors.New("no thumbnail tool found (macOS has sips built in; on Linux install ImageMagick or ffmpeg)")

type thumbTool struct {
	name string
	args func(in, out, size string) []string
}

// thumbTools are tried in order; the first one found in PATH is used. sips
// ships with macOS; ImageMagick (v7 `magick`, v6 `convert`) and ffmpeg are the
// usual choices on Linux.
var thumbTools = []thumbTool{
	{"sips", func(in, out, size string) []string {
		return []string{"-s", "format", "jpeg", "-Z", size, in, "--out", out}
	}},
	{"magick", imageMagickArgsSize},
	{"convert", imageMagickArgsSize},
	{"ffmpeg", func(in, out, size string) []string {
		return []string{"-v", "error", "-y", "-i", in,
			"-vf", "scale=w=" + size + ":h=" + size + ":force_original_aspect_ratio=decrease",
			"-frames:v", "1", "-update", "1", out}
	}},
}

func imageMagickArgs(in, out string) []string {
	return imageMagickArgsSize(in, out, ThumbSize)
}

// imageMagickArgsSize downscales (never upscales) to size and keeps only the
// first frame of animated inputs.
func imageMagickArgsSize(in, out, size string) []string {
	return []string{in + "[0]", "-auto-orient", "-resize", size + "x" + size + ">", "-quality", "85", out}
}

var (
	toolCacheMu   sync.Mutex
	toolCachePATH string
	toolCacheBin  string
	toolCacheTool thumbTool
	toolCacheOK   bool
)

func findThumbTool() (string, thumbTool, bool) {
	curPATH := os.Getenv("PATH")
	toolCacheMu.Lock()
	if toolCachePATH == curPATH && (toolCacheOK || toolCacheBin == "__none__") {
		bin, tool, ok := toolCacheBin, toolCacheTool, toolCacheOK
		toolCacheMu.Unlock()
		return bin, tool, ok
	}
	toolCacheMu.Unlock()

	var foundBin string
	var foundTool thumbTool
	var found bool
	for _, t := range thumbTools {
		if p, err := exec.LookPath(t.name); err == nil {
			foundBin, foundTool, found = p, t, true
			break
		}
	}

	toolCacheMu.Lock()
	toolCachePATH = curPATH
	if found {
		toolCacheBin, toolCacheTool, toolCacheOK = foundBin, foundTool, true
	} else {
		toolCacheBin, toolCacheTool, toolCacheOK = "__none__", thumbTool{}, false
	}
	toolCacheMu.Unlock()
	return foundBin, foundTool, found
}

// HasThumbnailer reports whether a tool for generating thumbnails exists.
func HasThumbnailer() bool {
	_, _, ok := findThumbTool()
	return ok
}

// InKitty reports whether we are running inside a Kitty window.
func InKitty() bool {
	return os.Getenv("KITTY_WINDOW_ID") != ""
}

func thumbSize(cfg *config.Config) int {
	if cfg != nil && cfg.Cache.ThumbSize > 0 {
		return cfg.Cache.ThumbSize
	}
	return DefaultThumbSize
}

// ThumbPath returns the cache location for abs. The name is a SHA-1 of the
// file path, modification time, and non-default thumbnail size.
func ThumbPath(cfg *config.Config, abs string) (string, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	size := thumbSize(cfg)
	if size == DefaultThumbSize {
		fmt.Fprintf(h, "%s\x00%d", abs, fi.ModTime().UnixNano())
	} else {
		fmt.Fprintf(h, "%s\x00%d\x00%d", abs, fi.ModTime().UnixNano(), size)
	}
	return cfg.ThumbDir + "/" + hex.EncodeToString(h.Sum(nil)) + ".jpg", nil
}

// EnsureThumb returns the cached JPEG thumbnail for abs, generating it if
// needed. created reports whether a new thumbnail was written.
func EnsureThumb(cfg *config.Config, abs string) (thumb string, created bool, err error) {
	thumb, err = ThumbPath(cfg, abs)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(thumb); err == nil {
		return thumb, false, nil
	}
	if err := os.MkdirAll(cfg.ThumbDir, 0o755); err != nil {
		return "", false, err
	}

	bin, tool, ok := findThumbTool()
	if !ok {
		return "", false, ErrNoThumbnailer
	}

	// Write to a temp name then rename, so a concurrent reader never sees a
	// half-written thumbnail. The name keeps a .jpg extension because
	// ImageMagick and ffmpeg pick the output format from it.
	tmp := fmt.Sprintf("%s.tmp-%d-%d.jpg", strings.TrimSuffix(thumb, ".jpg"), os.Getpid(), tmpCounter.Add(1))
	sizeStr := strconv.Itoa(thumbSize(cfg))
	ctx, cancel := context.WithTimeout(context.Background(), thumbTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, tool.args(abs, tmp, sizeStr)...).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return "", false, fmt.Errorf("%s failed: %v: %s", tool.name, err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmp, thumb); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	return thumb, true, nil
}

// PruneCache removes cached thumbnails in cfg.ThumbDir that do not correspond
// to any current file in files (relative paths under cfg.WallpaperDir).
func PruneCache(cfg *config.Config, files []string) (int, error) {
	entries, err := os.ReadDir(cfg.ThumbDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	keep := make(map[string]bool, len(files))
	for _, rel := range files {
		if tp, err := ThumbPath(cfg, cfg.ResolveRel(rel)); err == nil {
			keep[filepath.Base(tp)] = true
		}
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jpg") || keep[name] {
			continue
		}
		if err := os.Remove(filepath.Join(cfg.ThumbDir, name)); err == nil {
			removed++
		}
	}
	return removed, nil
}

// IcatArgs builds the kitten arguments for an fzf preview pane. cols and
// lines come from $FZF_PREVIEW_COLUMNS / $FZF_PREVIEW_LINES; when either is
// empty (e.g. running `kwall preview` by hand) no placement is requested.
func IcatArgs(img, cols, lines string, placeholder bool) []string {
	args := []string{"icat", "--clear", "--stdin=no", "--transfer-mode=memory"}
	if placeholder {
		args = append(args, "--unicode-placeholder")
	}
	args = append(args, "--scale-up")
	if cols != "" && lines != "" {
		args = append(args, fmt.Sprintf("--place=%sx%s@0x0", cols, lines))
	}
	return append(args, img)
}

// Render draws the wallpaper at rel (relative to the wallpaper directory)
// in the current fzf preview pane.
func Render(cfg *config.Config, rel string) error {
	abs := cfg.ResolveRel(rel)
	img := abs
	if thumb, _, err := EnsureThumb(cfg, abs); err == nil {
		img = thumb
	} else if !errors.Is(err, ErrNoThumbnailer) {
		fmt.Fprintf(os.Stderr, "kwall: thumbnail failed, using original: %v\n", err)
	}

	// Set KWALL_UNICODE_PLACEHOLDER=0 to fall back to kitty's direct
	// graphics placement if your fzf/kitty combination misrenders.
	placeholder := os.Getenv("KWALL_UNICODE_PLACEHOLDER") != "0"
	args := IcatArgs(img, os.Getenv("FZF_PREVIEW_COLUMNS"), os.Getenv("FZF_PREVIEW_LINES"), placeholder)

	ctx, cancel := context.WithTimeout(context.Background(), icatTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kitten", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Show prints an inline preview of abs in the terminal (used by `kwall current`).
func Show(cfg *config.Config, abs string) error {
	img := abs
	if thumb, _, err := EnsureThumb(cfg, abs); err == nil {
		img = thumb
	}
	ctx, cancel := context.WithTimeout(context.Background(), icatTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kitten", "icat", "--stdin=no", img)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Clear removes any images kitten icat left in the terminal. Best effort.
func Clear() {
	if !InKitty() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), icatTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kitten", "icat", "--clear", "--stdin=no")
	cmd.Stdout = os.Stdout
	_ = cmd.Run()
}

// Stats summarises a WarmCache run.
type Stats struct {
	Created int
	Cached  int
	Failed  int
}

// WarmCache generates thumbnails for files (relative paths) using a pool of
// workers goroutines. Failures are logged to log and counted.
func WarmCache(cfg *config.Config, files []string, workers int, log io.Writer) Stats {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string)
	var (
		mu sync.Mutex
		st Stats
		wg sync.WaitGroup
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range jobs {
				_, created, err := EnsureThumb(cfg, cfg.ResolveRel(rel))
				mu.Lock()
				switch {
				case err != nil:
					st.Failed++
					fmt.Fprintf(log, "kwall: %s: %v\n", rel, err)
				case created:
					st.Created++
				default:
					st.Cached++
				}
				mu.Unlock()
			}
		}()
	}
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	return st
}
