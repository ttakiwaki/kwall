// Package preview renders wallpaper previews with kitten icat and manages
// the sips-generated thumbnail cache.
package preview

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"kwall/internal/config"
)

// ThumbSize is the longest edge (in pixels) of cached thumbnails.
const ThumbSize = "1000"

var tmpCounter atomic.Uint64

// InKitty reports whether we are running inside a Kitty window.
func InKitty() bool {
	return os.Getenv("KITTY_WINDOW_ID") != ""
}

// ThumbPath returns the cache location for abs. The name is a SHA-1 of the
// file path and its modification time, so edited files get a fresh thumbnail.
func ThumbPath(cfg *config.Config, abs string) (string, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	fmt.Fprintf(h, "%s\x00%d", abs, fi.ModTime().UnixNano())
	return cfg.ThumbDir + "/" + hex.EncodeToString(h.Sum(nil)) + ".jpg", nil
}

// EnsureThumb returns the cached JPEG thumbnail for abs, generating it with
// sips if needed. created reports whether a new thumbnail was written.
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

	// Write to a temp name then rename, so a concurrent reader never sees a
	// half-written thumbnail.
	tmp := fmt.Sprintf("%s.tmp-%d-%d", thumb, os.Getpid(), tmpCounter.Add(1))
	out, err := exec.Command("sips", "-s", "format", "jpeg", "-Z", ThumbSize, abs, "--out", tmp).CombinedOutput()
	if err != nil {
		os.Remove(tmp)
		return "", false, fmt.Errorf("sips failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmp, thumb); err != nil {
		os.Remove(tmp)
		return "", false, err
	}
	return thumb, true, nil
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
	if thumb, _, err := EnsureThumb(cfg, abs); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: thumbnail failed, using original: %v\n", err)
	} else {
		img = thumb
	}

	// Set KWALL_UNICODE_PLACEHOLDER=0 to fall back to kitty's direct
	// graphics placement if your fzf/kitty combination misrenders.
	placeholder := os.Getenv("KWALL_UNICODE_PLACEHOLDER") != "0"
	args := IcatArgs(img, os.Getenv("FZF_PREVIEW_COLUMNS"), os.Getenv("FZF_PREVIEW_LINES"), placeholder)

	cmd := exec.Command("kitten", args...)
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
	cmd := exec.Command("kitten", "icat", "--stdin=no", img)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Clear removes any images kitten icat left in the terminal. Best effort.
func Clear() {
	if !InKitty() {
		return
	}
	cmd := exec.Command("kitten", "icat", "--clear", "--stdin=no")
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
