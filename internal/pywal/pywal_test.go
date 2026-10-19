package pywal

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"kwall/internal/config"
)

func TestEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "no": false, "1": true, "true": true, "ON": true, "Yes": true} {
		t.Setenv("KWALL_PYWAL", v)
		if got := Enabled(); got != want {
			t.Errorf("KWALL_PYWAL=%q: Enabled() = %v, want %v", v, got, want)
		}
	}
}

func TestArgs(t *testing.T) {
	got := Args("/t.jpg", "linux", "")
	if want := []string{"-n", "-q", "-i", "/t.jpg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("linux: got %v, want %v", got, want)
	}
	got = Args("/t.jpg", "darwin", "-a 85 --backend colorz")
	want := []string{"-n", "-q", "-e", "-a", "85", "--backend", "colorz", "-i", "/t.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("darwin: got %v, want %v", got, want)
	}
}

func TestWalCacheDir(t *testing.T) {
	cfg := &config.Config{CacheDir: "/home/u/.cache/kwall"}
	if got := WalCacheDir(cfg); got != "/home/u/.cache/wal" {
		t.Fatalf("got %q", got)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	// printf, not echo: echo would swallow the leading -n as its own flag.
	script := "#!/bin/sh\nprintf '%s ' \"$@\" > \"" + log + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "wal"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("KITTY_WINDOW_ID", "") // no kitty reload attempt
	t.Setenv("KWALL_PYWAL_ARGS", "-a 85")
	cfg := &config.Config{CacheDir: filepath.Join(dir, "cache", "kwall")}

	if err := Run(cfg, "/t.jpg"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	got := strings.TrimSpace(string(b))
	// -e is only added on darwin, so the expected args depend on the host OS.
	if !strings.HasSuffix(got, "-a 85 -i /t.jpg") || !strings.HasPrefix(got, "-n -q") {
		t.Fatalf("wal args = %q", got)
	}
}

func TestRunMissingWal(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := Run(&config.Config{CacheDir: t.TempDir()}, "/t.jpg")
	if err == nil || !strings.Contains(err.Error(), "wal") {
		t.Fatalf("expected missing-wal error, got %v", err)
	}
}

func TestRunFailureIncludesOutput(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'boom: bad image' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "wal"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	err := Run(&config.Config{CacheDir: t.TempDir()}, "/t.jpg")
	if err == nil || !strings.Contains(err.Error(), "boom: bad image") {
		t.Fatalf("expected wal output in error, got %v", err)
	}
}
