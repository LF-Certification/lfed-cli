package iostreams

import (
	"strings"
	"testing"
)

func TestStartProgressSilentWithoutTTY(t *testing.T) {
	s, _, _, errOut := Test()
	stop := s.StartProgress("Searching")
	stop()
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q; want nothing when stderr is not a terminal", errOut)
	}
}

func TestStartProgressClearsLine(t *testing.T) {
	s, _, _, errOut := Test()
	s.SetStderrTTY(true)
	stop := s.StartProgress("Searching")
	stop()
	stop()
	got := errOut.String()
	if !strings.Contains(got, "Searching") || !strings.HasSuffix(got, "\r\x1b[K") {
		t.Errorf("stderr = %q; want the label, then an erased line", got)
	}
}
