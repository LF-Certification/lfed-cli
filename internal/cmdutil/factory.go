package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.ed.linuxfoundation.org/lfed/internal/config"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

type Factory struct {
	IOStreams *iostreams.IOStreams

	Version string

	ContextFlag string
	OutputFlag  string
	JQ          string

	Debug bool

	Config func() (*config.Config, error)

	Context func() (contexts.Context, error)

	Token func(ctx context.Context) (string, error)

	Executable func() (string, error)
}

func New(version string) *Factory {
	f := &Factory{
		IOStreams:  iostreams.System(),
		Version:    version,
		Debug:      os.Getenv("LFED_DEBUG") != "",
		Config:     config.Load,
		Executable: os.Executable,
	}
	f.Context = func() (contexts.Context, error) {
		cfg, err := f.Config()
		if err != nil {
			return contexts.Context{}, err
		}
		return contexts.Resolve(f.ContextFlag, os.Getenv("LFED_CONTEXT"), cfg.CurrentContext)
	}
	return f
}

func (f *Factory) Printer() (output.Printer, error) {
	mode, err := output.ParseMode(f.OutputFlag)
	if err != nil {
		return output.Printer{}, &UsageError{Err: err}
	}
	p := output.Printer{Out: f.IOStreams.Out, Mode: mode}
	if f.JQ != "" {
		if mode == output.YAML {
			return output.Printer{}, &UsageError{Err: errors.New("--jq cannot be combined with -o yaml")}
		}
		p.Mode = output.JSON
		p.JQ = f.JQ
	}
	return p, nil
}

func (f *Factory) Debugf(format string, args ...any) {
	if f.Debug {
		fmt.Fprintf(f.IOStreams.ErrOut, "lfed: debug: "+format+"\n", args...)
	}
}
