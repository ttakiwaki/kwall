package wallpaper

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

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

// setOsascript changes the wallpaper through System Events (macOS default).
// macOS only applies it to the active Space; there is no scriptable way to
// set every Space.
func setOsascript(abs string) error {
	if _, err := exec.LookPath("osascript"); err != nil {
		return errors.New("osascript not found; it is only available on macOS (on Linux use KWALL_BACKEND=awww)")
	}
	out, err := exec.Command("osascript", osascriptArgs(abs)...).CombinedOutput()
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
	if out, err := exec.Command(bin, "set", abs).CombinedOutput(); err != nil {
		return fmt.Errorf("wallpaper set failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
