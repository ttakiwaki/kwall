package fzf

import (
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/kwall": "'/usr/local/bin/kwall'",
		"/with space/kwall":    "'/with space/kwall'",
		"/it's/kwall":          `'/it'\''s/kwall'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArgs(t *testing.T) {
	args := Args("/opt/my kwall/kwall")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--ansi", "--cycle", "--layout=reverse", "--border=rounded",
		"--prompt=Wallpaper > ", "--preview-window=right:65%:noborder",
		"--preview='/opt/my kwall/kwall' preview {}",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
}
