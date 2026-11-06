package iostreams

import (
	"os"
	"runtime"
	"testing"
)

func TestTestStreamsAreNonTTYNoColor(t *testing.T) {
	s, _, out, _ := Test()
	if s.IsStdinTTY() || s.IsStdoutTTY() || s.ColorEnabled() {
		t.Fatal("Test() streams must be non-TTY without color")
	}
	s.Out.Write([]byte("x"))
	if out.String() != "x" {
		t.Fatalf("out = %q", out.String())
	}
	s.SetStdinTTY(true)
	s.SetStdoutTTY(true)
	s.SetColorEnabled(true)
	if !s.IsStdinTTY() || !s.IsStdoutTTY() || !s.ColorEnabled() {
		t.Fatal("setters did not apply")
	}
}

func TestColorRulePrecedence(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "TERM"} {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	cases := []struct {
		name string
		env  map[string]string
		tty  bool
		want bool
	}{
		{"no color beats force", map[string]string{"NO_COLOR": "", "CLICOLOR_FORCE": "1"}, true, false},
		{"clicolor zero beats force", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "1"}, true, false},
		{"force beats dumb term", map[string]string{"CLICOLOR_FORCE": "1", "TERM": "dumb"}, false, true},
		{"force zero is not force", map[string]string{"CLICOLOR_FORCE": "0", "TERM": "xterm"}, true, runtime.GOOS != "windows"},
		{"dumb term off", map[string]string{"TERM": "dumb"}, true, false},
		{"tty on", map[string]string{"TERM": "xterm"}, true, runtime.GOOS != "windows"},
		{"non tty off", map[string]string{"TERM": "xterm"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clear(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := envColorEnabled(tc.tty); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestColorSchemeWrapsOnlyWhenEnabled(t *testing.T) {
	off := (&IOStreams{}).ColorScheme()
	if off.Red("a") != "a" || off.SuccessIcon() != "✓" || off.FailureIcon() != "X" || off.WarningIcon() != "!" {
		t.Fatal("disabled scheme must not add escapes")
	}
	on := (&IOStreams{colorEnabled: true}).ColorScheme()
	if got := on.Bold("a"); got != "\x1b[1ma\x1b[0m" {
		t.Fatalf("bold = %q", got)
	}
	if got := on.Gray("a"); got != "\x1b[90ma\x1b[0m" {
		t.Fatalf("gray = %q", got)
	}
	if got := on.SuccessIcon(); got != "\x1b[32m✓\x1b[0m" {
		t.Fatalf("success = %q", got)
	}
}
