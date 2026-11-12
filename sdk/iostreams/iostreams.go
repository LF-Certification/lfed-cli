package iostreams

import (
	"bytes"
	"io"
	"os"
	"runtime"
)

type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	stdinTTY     bool
	stdoutTTY    bool
	stderrTTY    bool
	colorEnabled bool
}

func System() *IOStreams {
	s := &IOStreams{
		In:        os.Stdin,
		Out:       os.Stdout,
		ErrOut:    os.Stderr,
		stdinTTY:  isTerminal(os.Stdin),
		stdoutTTY: isTerminal(os.Stdout),
		stderrTTY: isTerminal(os.Stderr),
	}
	s.colorEnabled = envColorEnabled(s.stdoutTTY)
	return s
}

func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, in, out, errOut
}

func (s *IOStreams) IsStdinTTY() bool  { return s.stdinTTY }
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutTTY }
func (s *IOStreams) IsStderrTTY() bool { return s.stderrTTY }

func (s *IOStreams) SetStdinTTY(v bool)  { s.stdinTTY = v }
func (s *IOStreams) SetStdoutTTY(v bool) { s.stdoutTTY = v }
func (s *IOStreams) SetStderrTTY(v bool) { s.stderrTTY = v }

func (s *IOStreams) ColorEnabled() bool     { return s.colorEnabled }
func (s *IOStreams) SetColorEnabled(v bool) { s.colorEnabled = v }

func (s *IOStreams) ColorScheme() *ColorScheme {
	return &ColorScheme{enabled: s.colorEnabled}
}

func isTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func envColorEnabled(stdoutTTY bool) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("CLICOLOR") == "0" {
		return false
	}
	if v, set := os.LookupEnv("CLICOLOR_FORCE"); set && v != "0" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if runtime.GOOS == "windows" {
		return false
	}
	return stdoutTTY
}
