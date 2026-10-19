package preview

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"kwall/internal/config"
)

func TestIcatArgs(t *testing.T) {
	got := IcatArgs("/img.jpg", "80", "24", true)
	want := []string{
		"icat", "--clear", "--stdin=no", "--transfer-mode=memory",
		"--unicode-placeholder", "--scale-up", "--place=80x24@0x0", "/img.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	got = IcatArgs("/img.jpg", "", "", false)
	want = []string{"icat", "--clear", "--stdin=no", "--transfer-mode=memory", "--scale-up", "/img.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// installFakeSips puts a stub `sips` first in PATH that writes its --out file.
func installFakeSips(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nfor last; do :; done\necho thumb > \"$last\"\n"
	if err := os.WriteFile(filepath.Join(bin, "sips"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// installOnlyTools makes PATH contain nothing but stub tools that write their
// own name into the output file (the last argument), so the test is hermetic
// on any OS regardless of what is really installed.
func installOnlyTools(t *testing.T, names ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, n := range names {
		script := "#!/bin/sh\nfor last; do :; done\necho " + n + " > \"$last\"\n"
		if err := os.WriteFile(filepath.Join(bin, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func TestThumbToolSelection(t *testing.T) {
	cases := []struct {
		tools []string
		want  string
	}{
		{[]string{"sips", "magick", "ffmpeg"}, "sips"},
		{[]string{"magick", "convert", "ffmpeg"}, "magick"},
		{[]string{"convert", "ffmpeg"}, "convert"},
		{[]string{"ffmpeg"}, "ffmpeg"},
	}
	for _, c := range cases {
		installOnlyTools(t, c.tools...)
		cfg, img := newTestConfig(t)
		thumb, _, err := EnsureThumb(cfg, img)
		if err != nil {
			t.Fatalf("%v: %v", c.tools, err)
		}
		b, _ := os.ReadFile(thumb)
		if got := strings.TrimSpace(string(b)); got != c.want {
			t.Errorf("tools %v: used %q, want %q", c.tools, got, c.want)
		}
	}
}

func TestNoThumbnailer(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if HasThumbnailer() {
		t.Fatal("HasThumbnailer should be false")
	}
	cfg, img := newTestConfig(t)
	if _, _, err := EnsureThumb(cfg, img); !errors.Is(err, ErrNoThumbnailer) {
		t.Fatalf("got %v, want ErrNoThumbnailer", err)
	}
}

func TestImageMagickArgs(t *testing.T) {
	got := imageMagickArgs("/in.heic", "/out.jpg")
	want := []string{"/in.heic[0]", "-auto-orient", "-resize", "1000x1000>", "-quality", "85", "/out.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func newTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	wd := t.TempDir()
	img := filepath.Join(wd, "a.jpg")
	if err := os.WriteFile(img, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{WallpaperDir: wd, ThumbDir: filepath.Join(t.TempDir(), "thumbs")}, img
}

func TestThumbPathDependsOnMtime(t *testing.T) {
	cfg, img := newTestConfig(t)
	p1, err := ThumbPath(cfg, img)
	if err != nil {
		t.Fatal(err)
	}
	p1b, _ := ThumbPath(cfg, img)
	if p1 != p1b {
		t.Fatal("thumb path should be stable")
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(img, later, later); err != nil {
		t.Fatal(err)
	}
	p2, _ := ThumbPath(cfg, img)
	if p1 == p2 {
		t.Fatal("thumb path should change when mtime changes")
	}
}

func TestEnsureThumb(t *testing.T) {
	installFakeSips(t)
	cfg, img := newTestConfig(t)

	thumb, created, err := EnsureThumb(cfg, img)
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(thumb); err != nil {
		t.Fatalf("thumbnail missing: %v", err)
	}
	if _, created, err := EnsureThumb(cfg, img); err != nil || created {
		t.Fatalf("second call should hit cache: created=%v err=%v", created, err)
	}

	entries, _ := os.ReadDir(cfg.ThumbDir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one file in cache (no temp leftovers), got %d", len(entries))
	}
}

func TestWarmCache(t *testing.T) {
	installFakeSips(t)
	cfg, _ := newTestConfig(t)
	var files []string
	for _, n := range []string{"b.jpg", "c.png", "d.heic"} {
		if err := os.WriteFile(filepath.Join(cfg.WallpaperDir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files = []string{"a.jpg", "b.jpg", "c.png", "d.heic", "missing.jpg"}

	var log bytes.Buffer
	st := WarmCache(cfg, files, 4, &log)
	if st.Created != 4 || st.Failed != 1 || st.Cached != 0 {
		t.Fatalf("unexpected stats: %+v (log: %s)", st, log.String())
	}
	st = WarmCache(cfg, files[:4], 4, &log)
	if st.Cached != 4 || st.Created != 0 {
		t.Fatalf("second run should be all cached: %+v", st)
	}
}
