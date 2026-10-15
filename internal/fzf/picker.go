// Package fzf runs the interactive fuzzy selector.
package fzf

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"kwall/internal/config"
)

// ErrCancelled is returned when the user quits fzf without choosing anything.
var ErrCancelled = errors.New("selection cancelled")

// shellQuote single-quotes s for safe use inside an `sh -c` command line
// (fzf runs --preview commands through the shell).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Args returns the fzf arguments. exe is the absolute path of the running
// kwall binary, so previews work even when kwall is not in $PATH.
func Args(exe string) []string {
	return []string{
		"--ansi",
		"--cycle",
		"--layout=reverse",
		"--border=rounded",
		"--prompt=Wallpaper > ",
		"--preview=" + shellQuote(exe) + " preview {}",
		"--preview-window=right:65%:noborder",
	}
}

// Pick shows files (relative paths) in fzf and returns the chosen one.
func Pick(cfg *config.Config, files []string) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", errors.New("fzf not found in PATH (install it with: brew install fzf)")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot locate kwall executable: %w", err)
	}

	cmd := exec.Command("fzf", Args(exe)...)
	cmd.Stdin = strings.NewReader(strings.Join(files, "\n") + "\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	// The preview subprocess must look in the same directory we listed.
	cmd.Env = append(os.Environ(), "WALLPAPER_DIR="+cfg.WallpaperDir)

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 1 || ee.ExitCode() == 130) {
			return "", ErrCancelled
		}
		return "", fmt.Errorf("fzf: %w", err)
	}

	sel := strings.TrimRight(out.String(), "\r\n")
	if sel == "" {
		return "", ErrCancelled
	}
	return sel, nil
}
