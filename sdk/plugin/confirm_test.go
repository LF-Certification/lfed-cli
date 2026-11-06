package plugin

import (
	"strings"
	"testing"

	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
)

func TestConfirm(t *testing.T) {
	tests := []struct {
		name       string
		yes        bool
		tty        bool
		input      string
		wantErr    string
		wantPrompt bool
	}{
		{name: "yes flag skips prompt", yes: true, tty: false},
		{name: "non-tty requires --yes", wantErr: "confirmation required; re-run with --yes"},
		{name: "y accepts", tty: true, input: "y\n", wantPrompt: true},
		{name: "YES accepts", tty: true, input: " YES \n", wantPrompt: true},
		{name: "n aborts", tty: true, input: "n\n", wantErr: "aborted", wantPrompt: true},
		{name: "empty line aborts", tty: true, input: "\n", wantErr: "aborted", wantPrompt: true},
		{name: "EOF aborts", tty: true, input: "", wantErr: "aborted", wantPrompt: true},
		{name: "y without newline accepts", tty: true, input: "y", wantPrompt: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ios, in, out, errOut := iostreams.Test()
			ios.SetStdinTTY(tc.tty)
			in.WriteString(tc.input)
			err := Confirm(ios, tc.yes, "Delete target x?")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("err %v", err)
			}
			if tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
				t.Fatalf("err %v, want %q", err, tc.wantErr)
			}
			if got := strings.Contains(errOut.String(), "Delete target x? [y/N]: "); got != tc.wantPrompt {
				t.Errorf("prompt on stderr = %v, want %v (%q)", got, tc.wantPrompt, errOut.String())
			}
			if out.Len() != 0 {
				t.Errorf("stdout must stay clean: %q", out.String())
			}
		})
	}
}
