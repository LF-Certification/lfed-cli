package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

const (
	exitError       = 1
	exitUsage       = 2
	exitInterrupted = 130
)

func Serve(root *cobra.Command, m Manifest) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, root, m, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(ctx context.Context, root *cobra.Command, m Manifest, args []string) int {
	Prepare(root, m)
	exec, prefix := root, "lfed-"+m.Name
	if os.Getenv("LFED_REEXEC") != "" {
		exec, args = mount(root, m.Name, args)
		prefix = "lfed " + m.Name
	}
	exec.SetArgs(args)
	err := exec.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return exitInterrupted
	}
	fmt.Fprintf(root.ErrOrStderr(), "%s: %v\n", prefix, err)
	var usage *flagError
	if errors.As(err, &usage) {
		return exitUsage
	}
	if ctx.Err() != nil {
		return exitInterrupted
	}
	return exitError
}

type flagError struct{ err error }

func (e *flagError) Error() string { return e.err.Error() }

func (e *flagError) Unwrap() error { return e.err }

func wrapFlagError(_ *cobra.Command, err error) error { return &flagError{err: err} }

func mount(root *cobra.Command, name string, args []string) (*cobra.Command, []string) {
	root.Use = name
	if !root.Runnable() {
		if root.Args == nil {
			root.Args = cobra.NoArgs
		}
		root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}
	parent := &cobra.Command{Use: "lfed", SilenceUsage: true, SilenceErrors: true}
	parent.SetFlagErrorFunc(wrapFlagError)
	parent.SetIn(root.InOrStdin())
	parent.SetOut(root.OutOrStdout())
	parent.SetErr(root.ErrOrStderr())
	parent.AddCommand(root)
	if len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd) {
		return parent, append([]string{args[0], name}, args[1:]...)
	}
	return parent, append([]string{name}, args...)
}

func Prepare(root *cobra.Command, m Manifest) {
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetFlagErrorFunc(wrapFlagError)
	if root.Use == "" {
		root.Use = "lfed-" + m.Name
	}
	mode, err := output.ParseMode(os.Getenv("LFED_OUTPUT"))
	if err != nil {
		mode = output.Table
	}
	pf := root.PersistentFlags()
	if pf.Lookup("output") == nil {
		pf.StringP("output", "o", string(mode), "Output format: "+output.Modes)
	}
	if pf.Lookup("jq") == nil {
		pf.String("jq", os.Getenv("LFED_JQ"), "Filter the JSON output with a jq expression. Implies -o json.")
	}
	root.AddCommand(newMetadataCmd(m))
}

func newMetadataCmd(m Manifest) *cobra.Command {
	return &cobra.Command{
		Use:    "__metadata",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m.SchemaVersion = ManifestSchemaVersion
			return output.PrintJSON(cmd.OutOrStdout(), m)
		},
	}
}

func Printer(cmd *cobra.Command) (output.Printer, error) {
	p := output.Printer{Out: cmd.OutOrStdout()}
	raw, err := cmd.Flags().GetString("output")
	if err != nil {
		return p, errors.New("output flag not registered; call plugin.Serve")
	}
	mode, err := output.ParseMode(raw)
	if err != nil {
		return p, err
	}
	jq, err := cmd.Flags().GetString("jq")
	if err != nil {
		return p, errors.New("jq flag not registered; call plugin.Serve")
	}
	p.Mode = mode
	if jq != "" {
		if mode == output.YAML {
			return p, &flagError{err: errors.New("--jq cannot be combined with -o yaml")}
		}
		p.Mode = output.JSON
		p.JQ = jq
	}
	return p, nil
}
