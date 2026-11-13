package wallpaper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const macOSCmdTimeout = 10 * time.Second

func fileURL(abs string) string {
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

// canonicalPath resolves symlinks and restores the exact on-disk casing of each
// path component on case-insensitive filesystems (APFS), because macOS's
// sandboxed WallpaperImageExtension matches URLs in ChoiceRequests.ImageFiles
// case-sensitively.
func canonicalPath(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	if !filepath.IsAbs(abs) {
		return abs
	}
	cur := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(abs, cur), string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return abs
		}
		matched := ""
		for _, e := range entries {
			if e.Name() == part {
				matched = e.Name()
				break
			}
			if matched == "" && strings.EqualFold(e.Name(), part) {
				matched = e.Name()
			}
		}
		if matched == "" {
			return abs
		}
		cur = filepath.Join(cur, matched)
	}
	return cur
}

func nsworkspaceArgs(abs string) []string {
	script := `ObjC.import("AppKit");
function run(argv) {
	const ws = $.NSWorkspace.sharedWorkspace;
	const url = $.NSURL.fileURLWithPath(argv[0]);
	const screens = $.NSScreen.screens;
	for (let i = 0; i < screens.count; i++) {
		const s = screens.objectAtIndex(i);
		const err = Ref();
		if (!ws.setDesktopImageURLForScreenOptionsError(url, s, $(), err)) {
			const msg = err[0] && err[0].localizedDescription ? ObjC.unwrap(err[0].localizedDescription) : "unknown error";
			throw new Error(msg);
		}
	}
}`
	return []string{"-l", "JavaScript", "-e", script, abs}
}

// registerAndSetActiveSpace asks AppKit (via NSWorkspace) to set the wallpaper
// on the active Space. This grants macOS's sandboxed WallpaperImageExtension a
// security-scoped bookmark in ChoiceRequests.ImageFiles without requiring System
// Events Automation permission.
func registerAndSetActiveSpace(abs string) error {
	ctx, cancel := context.WithTimeout(context.Background(), macOSCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", nsworkspaceArgs(abs)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("NSWorkspace setDesktopImageURL failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitForStoreWrite waits briefly for WallpaperAgent's asynchronous scheduled
// store write triggered by NSWorkspace to finish flushing to Index.plist so it
// does not overwrite our multi-Space update right afterward.
func waitForStoreWrite(idxPath string, before time.Time) {
	deadline := time.Now().Add(1200 * time.Millisecond)
	noChangeCutoff := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(idxPath); err == nil && fi.ModTime().After(before) {
			time.Sleep(150 * time.Millisecond)
			return
		}
		if time.Now().After(noChangeCutoff) {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func indexPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "com.apple.wallpaper", "Store", "Index.plist"), nil
}

func plutilToXML(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), macOSCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "plutil", "-convert", "xml1", "-o", "-", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plutil xml1 failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func plutilToBinary(xmlData []byte, outPath string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), macOSCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "plutil", "-convert", "binary1", "-o", outPath, "-")
	cmd.Stdin = bytes.NewReader(xmlData)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plutil binary1 failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// setViaStore registers the image for WallpaperImageExtension via NSWorkspace,
// updates macOS 14+ com.apple.wallpaper Index.plist across every Space and
// display, and restarts WallpaperAgent so all Spaces reload it.
func setViaStore(abs, idxPath string) error {
	abs = canonicalPath(abs)

	var before time.Time
	if fi, err := os.Stat(idxPath); err == nil {
		before = fi.ModTime()
	}
	if err := registerAndSetActiveSpace(abs); err != nil {
		return err
	}
	waitForStoreWrite(idxPath, before)

	u := fileURL(abs)
	cfgBin, err := buildImageConfigBplist(u)
	if err != nil {
		return err
	}

	rawStore, err := os.ReadFile(idxPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", idxPath, err)
	}

	tmp := fmt.Sprintf("%s.tmp-%d", idxPath, os.Getpid())
	defer os.Remove(tmp)

	if isBplist(rawStore) {
		root, err := parseBplist(rawStore)
		if err != nil {
			return err
		}
		if _, err := updateWallpaperStore(root, u, cfgBin, time.Now()); err != nil {
			return err
		}
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		writeErr := writeBplist(f, root)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	} else {
		storeXML, err := plutilToXML(idxPath)
		if err != nil {
			return err
		}
		root, err := parsePlistXML(storeXML)
		if err != nil {
			return err
		}
		if _, err := updateWallpaperStore(root, u, cfgBin, time.Now()); err != nil {
			return err
		}
		updatedXML, err := marshalPlistXML(root)
		if err != nil {
			return err
		}
		if _, err := plutilToBinary(updatedXML, tmp); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0o644); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, idxPath); err != nil {
		return fmt.Errorf("replace %s: %w", idxPath, err)
	}

	killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer killCancel()
	if out, err := exec.CommandContext(killCtx, "killall", "WallpaperAgent").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if !strings.Contains(msg, "No matching processes") {
			return fmt.Errorf("restart WallpaperAgent failed: %v: %s", err, msg)
		}
	}
	return nil
}

// setPlist updates macOS 14+ com.apple.wallpaper Index.plist across every
// Space and display, falling back to osascript on macOS 13 and earlier.
func setPlist(abs string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("KWALL_BACKEND=plist is only supported on macOS")
	}
	if idxPath, err := indexPlistPath(); err == nil {
		if _, err := os.Stat(idxPath); err == nil {
			return setViaStore(abs, idxPath)
		}
	}
	return setOsascript(abs)
}
// osascriptArgs builds the osascript invocation. The path is passed as an
// argument (argv) rather than spliced into the script, so quotes, backslashes
// and unicode in file names can never break or inject into the AppleScript.
func osascriptArgs(abs string) []string {
	return []string{
		"-e", "on run argv",
		"-e", `tell application "System Events" to tell every desktop to set picture to (POSIX file (item 1 of argv))`,
		"-e", "end run",
		abs,
	}
}

// setOsascript changes the wallpaper through System Events (active Space only).
func setOsascript(abs string) error {
	if _, err := exec.LookPath("osascript"); err != nil {
		return errors.New("osascript not found; it is only available on macOS (on Linux use KWALL_BACKEND=awww)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), macOSCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", osascriptArgs(abs)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript failed: %v: %s\n(if macOS asked for permission, allow your terminal to control System Events in System Settings > Privacy & Security > Automation)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// setWallpaperCLI uses the `wallpaper` CLI
// (https://github.com/sindresorhus/macos-wallpaper); KWALL_BACKEND=wallpaper.
func setWallpaperCLI(abs string) error {
	bin, err := exec.LookPath("wallpaper")
	if err != nil {
		return errors.New("KWALL_BACKEND=wallpaper but the wallpaper CLI is not in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), macOSCmdTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, bin, "set", abs).CombinedOutput(); err != nil {
		return fmt.Errorf("wallpaper set failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
