package wallpaper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAwww installs stub awww and awww-daemon binaries in a private PATH.
// `awww query` fails until the stub daemon has been started (it creates the
// ready file), mimicking a real daemon that is not running yet.
func fakeAwww(t *testing.T, daemonRunning bool) (logFile string) {
	t.Helper()
	bin := t.TempDir()
	logFile = filepath.Join(t.TempDir(), "log")
	ready := filepath.Join(t.TempDir(), "ready")

	awww := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n" +
		"if [ \"$1\" = query ] && [ ! -e \"" + ready + "\" ]; then exit 1; fi\nexit 0\n"
	daemon := "#!/bin/sh\ntouch \"" + ready + "\"\n"
	for name, body := range map[string]string{"awww": awww, "awww-daemon": daemon, "touch": "", "sleep": ""} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if daemonRunning {
		if err := os.WriteFile(ready, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The daemon stub needs `touch`; keep the real PATH after our stubs.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KWALL_BACKEND", "awww")
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	t.Setenv("KWALL_AWWW_ARGS", "")
	return logFile
}

func readLog(t *testing.T, f string) []string {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestSetAwwwDaemonRunning(t *testing.T) {
	log := fakeAwww(t, true)
	t.Setenv("KWALL_AWWW_ARGS", "--resize fit --outputs DP-1")

	if err := Set("/walls/a b.png"); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, log)
	want := []string{"query", "img --resize fit --outputs DP-1 /walls/a b.png"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("awww calls = %q, want %q", got, want)
	}
}

func TestSetAwwwStartsDaemon(t *testing.T) {
	log := fakeAwww(t, false)
	old := daemonWait
	daemonWait = 5 * time.Second
	t.Cleanup(func() { daemonWait = old })

	if err := Set("/walls/a.png"); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, log)
	if last := got[len(got)-1]; last != "img /walls/a.png" {
		t.Fatalf("last awww call = %q (all: %q)", last, got)
	}
}

func TestSetAwwwNeedsWayland(t *testing.T) {
	fakeAwww(t, false)
	t.Setenv("WAYLAND_DISPLAY", "")
	err := Set("/walls/a.png")
	if err == nil || !strings.Contains(err.Error(), "WAYLAND_DISPLAY") {
		t.Fatalf("expected WAYLAND_DISPLAY error, got %v", err)
	}
}

func TestSetAwwwMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("KWALL_BACKEND", "awww")
	err := Set("/walls/a.png")
	if err == nil || !strings.Contains(err.Error(), "awww not found") {
		t.Fatalf("expected 'awww not found', got %v", err)
	}
}

func TestSetUnknownBackend(t *testing.T) {
	t.Setenv("KWALL_BACKEND", "bogus")
	if err := Set("/x.png"); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-backend error, got %v", err)
	}
}
