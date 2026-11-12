// Package wallpaper applies a wallpaper on macOS and tracks kwall's state.
package wallpaper

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kwall/internal/config"
)

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
	out, err := exec.Command("osascript", nsworkspaceArgs(abs)...).CombinedOutput()
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
	cmd := exec.Command("plutil", "-convert", "xml1", "-o", "-", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plutil xml1 failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func plutilToBinary(xmlData []byte, outPath string) ([]byte, error) {
	cmd := exec.Command("plutil", "-convert", "binary1", "-o", outPath, "-")
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
	cfgXML, err := buildImageConfigXML(u)
	if err != nil {
		return err
	}
	cfgBin, err := plutilToBinary(cfgXML, "-")
	if err != nil {
		return err
	}

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

	tmp := fmt.Sprintf("%s.tmp-%d", idxPath, os.Getpid())
	defer os.Remove(tmp)
	if _, err := plutilToBinary(updatedXML, tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, idxPath); err != nil {
		return fmt.Errorf("replace %s: %w", idxPath, err)
	}

	if out, err := exec.Command("killall", "WallpaperAgent").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if !strings.Contains(msg, "No matching processes") {
			return fmt.Errorf("restart WallpaperAgent failed: %v: %s", err, msg)
		}
	}
	return nil
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

func setViaOsascript(abs string) error {
	out, err := exec.Command("osascript", osascriptArgs(abs)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript failed: %v: %s\n(if macOS asked for permission, allow your terminal to control System Events in System Settings > Privacy & Security > Automation)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Set changes the wallpaper on every macOS Space and display.
//
// On macOS 14+ it updates com.apple.wallpaper's Index.plist and restarts
// WallpaperAgent so every Space updates, falling back to osascript on older
// macOS versions. Set KWALL_BACKEND=osascript or KWALL_BACKEND=wallpaper to
// force those active-Space-only backends instead.
func Set(abs string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("setting the wallpaper is only supported on macOS")
	}

	switch backend := os.Getenv("KWALL_BACKEND"); backend {
	case "", "plist":
		if idxPath, err := indexPlistPath(); err == nil {
			if _, err := os.Stat(idxPath); err == nil {
				return setViaStore(abs, idxPath)
			}
		}
		return setViaOsascript(abs)
	case "osascript":
		return setViaOsascript(abs)
	case "wallpaper":
		bin, err := exec.LookPath("wallpaper")
		if err != nil {
			return errors.New("KWALL_BACKEND=wallpaper but the wallpaper CLI is not in PATH")
		}
		if out, err := exec.Command(bin, "set", abs).CombinedOutput(); err != nil {
			return fmt.Errorf("wallpaper set failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return fmt.Errorf("unknown KWALL_BACKEND %q (supported: plist, osascript, wallpaper)", backend)
	}
}

// Apply sets the wallpaper, records it as the current one and runs the
// optional post-hook. Only a failure to set the wallpaper is fatal.
func Apply(cfg *config.Config, abs, thumb string) error {
	if err := Set(abs); err != nil {
		return err
	}
	if err := WriteCurrent(cfg, abs); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: could not save state: %v\n", err)
	}
	if err := RunHook(cfg, abs, thumb); err != nil {
		fmt.Fprintf(os.Stderr, "kwall: warning: post-hook: %v\n", err)
	}
	return nil
}

// WriteCurrent stores the last wallpaper set by kwall.
func WriteCurrent(cfg *config.Config, abs string) error {
	if err := os.MkdirAll(filepath.Dir(cfg.StateFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.StateFile, []byte(abs+"\n"), 0o644)
}

// ReadCurrent returns the last wallpaper set by kwall, or "" if none.
func ReadCurrent(cfg *config.Config) (string, error) {
	b, err := os.ReadFile(cfg.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// hookPath returns the post-hook script if one exists.
func hookPath(cfg *config.Config) string {
	for _, name := range []string{"post-hook.sh", "post-hook"} {
		p := filepath.Join(cfg.ConfigDir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// HasHook reports whether a post-hook script exists.
func HasHook(cfg *config.Config) bool {
	return hookPath(cfg) != ""
}

// RunHook runs ~/.config/kwall/post-hook.sh (or post-hook) with the new
// wallpaper path as $1 (also in $KWALL_WALLPAPER). If thumb is non-empty it
// is a small cached JPEG of the wallpaper, passed as $2 (also in
// $KWALL_THUMBNAIL); tools that analyse the image, like pywal, run much
// faster on it than on a full-size 4K/5K original.
func RunHook(cfg *config.Config, abs, thumb string) error {
	p := hookPath(cfg)
	if p == "" {
		return nil
	}
	if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 == 0 {
		return fmt.Errorf("%s exists but is not executable (chmod +x it)", p)
	}
	args := []string{abs}
	env := append(os.Environ(), "KWALL_WALLPAPER="+abs)
	if thumb != "" {
		args = append(args, thumb)
		env = append(env, "KWALL_THUMBNAIL="+thumb)
	}
	cmd := exec.Command(p, args...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}
