package wallpaper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kwall/internal/config"
)

func TestOsascriptArgsKeepsPathOutOfScript(t *testing.T) {
	nasty := `/Users/me/Pictures/Wallpapers/it's "quoted" \ ünï.png`
	args := osascriptArgs(nasty)
	if args[len(args)-1] != nasty {
		t.Fatal("path must be passed as the final argv entry")
	}
	for _, a := range args[:len(args)-1] {
		if strings.Contains(a, "quoted") {
			t.Fatalf("path leaked into script text: %q", a)
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	cfg := &config.Config{StateFile: filepath.Join(t.TempDir(), "state", "current")}

	if got, err := ReadCurrent(cfg); err != nil || got != "" {
		t.Fatalf("empty state: got %q, %v", got, err)
	}
	if err := WriteCurrent(cfg, "/a/b.jpg"); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCurrent(cfg); err != nil || got != "/a/b.jpg" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRunHook(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir}

	// No hook: no error.
	if HasHook(cfg) {
		t.Fatal("HasHook should be false with no script")
	}
	if err := RunHook(cfg, "/x.jpg", ""); err != nil {
		t.Fatal(err)
	}

	hook := filepath.Join(dir, "post-hook.sh")
	script := "#!/bin/sh\nprintf '%s|%s|%s|%s' \"$1\" \"$2\" \"$KWALL_WALLPAPER\" \"$KWALL_THUMBNAIL\" > \"$(dirname \"$0\")/out.txt\"\n"
	if err := os.WriteFile(hook, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasHook(cfg) {
		t.Fatal("HasHook should be true once the script exists")
	}
	// Not executable: reported, not run.
	if err := RunHook(cfg, "/x.jpg", ""); err == nil {
		t.Fatal("expected error for non-executable hook")
	}

	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		out, err := os.ReadFile(filepath.Join(dir, "out.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	if err := RunHook(cfg, "/x.jpg", ""); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "/x.jpg||/x.jpg|" {
		t.Fatalf("hook output without thumb = %q", got)
	}

	if err := RunHook(cfg, "/x.jpg", "/t.jpg"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "/x.jpg|/t.jpg|/x.jpg|/t.jpg" {
		t.Fatalf("hook output with thumb = %q", got)
	}
}
