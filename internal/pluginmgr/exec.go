package pluginmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("plugin exited with code %d", e.Code)
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	var notFound *NotFoundError
	var incompatible *IncompatibleError
	var noRelease *NoReleaseError
	var usage *cmdutil.UsageError
	if errors.As(err, &notFound) || errors.As(err, &incompatible) || errors.As(err, &noRelease) || errors.As(err, &usage) {
		return 2
	}
	return 1
}

type EnvSpec struct {
	LfedVersion string
	Context     contexts.Context
	HasContext  bool
	Mode        output.Mode
	JQ          string
	Reexec      string
	Debug       bool
}

func Env(s EnvSpec) []string {
	var name, base, debug string
	if s.HasContext {
		name = string(s.Context.Name)
		base = s.Context.APIBaseURL()
	}
	if s.Debug {
		debug = "1"
	}
	return []string{
		"LFED_CLI_VERSION=" + s.LfedVersion,
		"LFED_CONTEXT=" + name,
		"LFED_API_BASE_URL=" + base,
		"LFED_OUTPUT=" + string(s.Mode),
		"LFED_JQ=" + s.JQ,
		"LFED_REEXEC=" + s.Reexec,
		"LFED_DEBUG=" + debug,
	}
}

func Run(ctx context.Context, f *cmdutil.Factory, name string, args []string) error {
	p, err := Lookup(name)
	if err != nil {
		return err
	}
	if err := checkInstalled(p, f.Version); err != nil {
		return err
	}
	return Exec(ctx, f, p, args)
}

func checkInstalled(p Installed, running string) error {
	err := CheckLfedVersion(p.Manifest.RequiresLfed, running)
	var incompatible *IncompatibleError
	if errors.As(err, &incompatible) {
		incompatible.Name = p.Manifest.Name
	}
	return err
}

func Exec(_ context.Context, f *cmdutil.Factory, p Installed, args []string) error {
	env, err := cliEnv(f)
	if err != nil {
		return err
	}
	f.Debugf("exec %s %s", p.Binary, strings.Join(args, " "))
	for _, kv := range env {
		f.Debugf("env %s", kv)
	}
	return execPlugin(p.Binary, args, append(os.Environ(), env...))
}

func Complete(_ context.Context, f *cmdutil.Factory, name string, args []string) ([]string, cobra.ShellCompDirective, error) {
	p, err := Lookup(name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError, err
	}
	if err := checkInstalled(p, f.Version); err != nil {
		return nil, cobra.ShellCompDirectiveError, err
	}
	env, err := cliEnv(f)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError, err
	}
	var out bytes.Buffer
	cmd := exec.Command(p.Binary, append([]string{cobra.ShellCompRequestCmd}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &out
	cmd.Stderr = f.IOStreams.ErrOut
	if err := cmd.Run(); err != nil {
		return nil, cobra.ShellCompDirectiveError, fmt.Errorf("complete %s: %w", p.Binary, err)
	}
	words, directive := parseCompletion(out.String())
	return words, directive, nil
}

func parseCompletion(s string) ([]string, cobra.ShellCompDirective) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	directive := cobra.ShellCompDirectiveDefault
	last := len(lines) - 1
	if last >= 0 && strings.HasPrefix(lines[last], ":") {
		if n, err := strconv.Atoi(lines[last][1:]); err == nil {
			directive = cobra.ShellCompDirective(n)
		}
		lines = lines[:last]
	}
	words := lines[:0]
	for _, l := range lines {
		if l != "" {
			words = append(words, l)
		}
	}
	return words, directive
}

func cliEnv(f *cmdutil.Factory) ([]string, error) {
	spec := EnvSpec{LfedVersion: f.Version, Debug: f.Debug}
	c, err := f.Context()
	switch {
	case err == nil:
		spec.Context, spec.HasContext = c, true
	case !errors.Is(err, contexts.ErrNoContext):
		return nil, err
	}
	p, err := f.Printer()
	if err != nil {
		return nil, err
	}
	spec.Mode, spec.JQ = p.Mode, p.JQ
	spec.Reexec, err = f.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve lfed path: %w", err)
	}
	return Env(spec), nil
}
