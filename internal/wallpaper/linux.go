package wallpaper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// daemonWait is how long we wait for a freshly started awww-daemon to
// accept commands. A variable so tests can shorten it.
var daemonWait = 3 * time.Second

// awwwBinaries finds the awww client and daemon. The project was renamed from
// swww, so an older swww install is used as a fallback.
func awwwBinaries() (cli, daemon string, err error) {
	for _, pair := range [][2]string{{"awww", "awww-daemon"}, {"swww", "swww-daemon"}} {
		if _, err := exec.LookPath(pair[0]); err == nil {
			return pair[0], pair[1], nil
		}
	}
	return "", "", errors.New("awww not found in PATH (see https://codeberg.org/LGFae/awww)")
}

// ensureDaemon makes sure awww-daemon is accepting commands, starting it in
// the background if it is not running yet.
func ensureDaemon(cli, daemon string) error {
	running := func() bool { return exec.Command(cli, "query").Run() == nil }
	if running() {
		return nil
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("WAYLAND_DISPLAY is not set: awww needs a running Wayland session (run kwall from a terminal inside Hyprland)")
	}
	path, err := exec.LookPath(daemon)
	if err != nil {
		return fmt.Errorf("%s not found in PATH", daemon)
	}

	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlive kwall and its terminal
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", daemon, err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(daemonWait)
	for time.Now().Before(deadline) {
		if running() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s was started but is not responding", daemon)
}

// setAwww sets the wallpaper on all outputs with awww.
//
// awww reads its own settings from the environment (AWWW_TRANSITION,
// AWWW_TRANSITION_FPS, AWWW_TRANSITION_DURATION); extra `awww img` flags such
// as "--resize fit" or "--outputs DP-1" can be given in $KWALL_AWWW_ARGS.
func setAwww(abs string) error {
	cli, daemon, err := awwwBinaries()
	if err != nil {
		return err
	}
	if err := ensureDaemon(cli, daemon); err != nil {
		return err
	}
	args := append([]string{"img"}, strings.Fields(os.Getenv("KWALL_AWWW_ARGS"))...)
	args = append(args, abs)
	if out, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s img failed: %v: %s", cli, err, strings.TrimSpace(string(out)))
	}
	return nil
}
