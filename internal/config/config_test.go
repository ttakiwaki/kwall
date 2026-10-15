package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListImages(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "b.jpg"))
	touch(t, filepath.Join(dir, "a.HEIC"))
	touch(t, filepath.Join(dir, "sub", "c.PNG"))
	touch(t, filepath.Join(dir, "notes.txt"))
	touch(t, filepath.Join(dir, ".hidden.jpg"))
	touch(t, filepath.Join(dir, ".git", "d.jpg"))

	got, err := ListImages(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.HEIC", "b.jpg", filepath.Join("sub", "c.PNG")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListImagesSymlinkRoot(t *testing.T) {
	real := t.TempDir()
	touch(t, filepath.Join(real, "a.png"))
	link := filepath.Join(t.TempDir(), "walls")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	got, err := ListImages(link)
	if err != nil || len(got) != 1 || got[0] != "a.png" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestListImagesMissingDir(t *testing.T) {
	if _, err := ListImages(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WALLPAPER_DIR", "~/walls")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string][2]string{
		"WallpaperDir": {cfg.WallpaperDir, filepath.Join(home, "walls")},
		"ConfigDir":    {cfg.ConfigDir, filepath.Join(home, ".config", "kwall")},
		"ThumbDir":     {cfg.ThumbDir, filepath.Join(home, ".cache", "kwall", "thumbs")},
		"StateFile":    {cfg.StateFile, filepath.Join(home, ".local", "state", "kwall", "current")},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
}

func TestLoadDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WALLPAPER_DIR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Pictures", "Wallpapers"); cfg.WallpaperDir != want {
		t.Fatalf("got %q, want %q", cfg.WallpaperDir, want)
	}
}

func TestResolveInput(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "x.jpg"))
	touch(t, filepath.Join(dir, "doc.txt"))
	cfg := &Config{WallpaperDir: dir}

	if got, err := cfg.ResolveInput("x.jpg"); err != nil || got != filepath.Join(dir, "x.jpg") {
		t.Errorf("relative to wallpaper dir: got %q, %v", got, err)
	}
	if got, err := cfg.ResolveInput(filepath.Join(dir, "x.jpg")); err != nil || got != filepath.Join(dir, "x.jpg") {
		t.Errorf("absolute: got %q, %v", got, err)
	}
	if _, err := cfg.ResolveInput("doc.txt"); err == nil {
		t.Error("expected error for unsupported extension")
	}
	if _, err := cfg.ResolveInput("missing.jpg"); err == nil {
		t.Error("expected error for missing file")
	}
}
