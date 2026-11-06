package root

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	authcmd "go.ed.linuxfoundation.org/lfed/internal/cmd/auth"
	contextcmd "go.ed.linuxfoundation.org/lfed/internal/cmd/context"
	plugincmd "go.ed.linuxfoundation.org/lfed/internal/cmd/plugin"
	"go.ed.linuxfoundation.org/lfed/internal/cmd/version"
	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

func Run(ctx context.Context, f *cmdutil.Factory, args []string) error {
	cmd := NewCmd(f)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lfed",
		Short: "Work with the education platform from the command line",
		Long: cmdutil.Long(`
			Work with the education platform from the command line.

			The core commands manage contexts, credentials and plugins. Plugins
			add the platform commands. Run "lfed help plugins" for the plugin
			model.
		`),
		Example: cmdutil.Examples(`
			# Select the deployment context
			$ lfed context use dev

			# Log in
			$ lfed auth login

			# Install a plugin from the plugin catalog
			$ lfed plugin install placement

			# Run a plugin command
			$ lfed placement --help
		`),
		SilenceUsage:               true,
		SilenceErrors:              true,
		TraverseChildren:           true,
		SuggestionsMinimumDistance: 2,
		Version:                    f.Version,
	}
	cmd.SetIn(f.IOStreams.In)
	cmd.SetOut(f.IOStreams.Out)
	cmd.SetErr(f.IOStreams.ErrOut)
	cmd.SetVersionTemplate("lfed {{.Version}}\n")
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &cmdutil.UsageError{Err: err, Cmd: c}
	})

	pf := cmd.PersistentFlags()
	pf.StringVarP(&f.ContextFlag, "context", "c", "", "Deployment context: dev, staging or prod. Overrides LFED_CONTEXT and the config file.")
	pf.StringVarP(&f.OutputFlag, "output", "o", "", "Output format: "+output.Modes+` (default "table")`)
	pf.StringVar(&f.JQ, "jq", "", "Filter the JSON output with a jq expression. Implies -o json.")

	cmd.AddGroup(&cobra.Group{ID: "core", Title: "Available Commands:"})
	cmd.SetHelpCommandGroupID("core")
	cmd.SetCompletionCommandGroupID("core")
	for _, c := range []*cobra.Command{
		version.NewCmd(f),
		contextcmd.NewCmd(f),
		authcmd.NewCmd(f),
		plugincmd.NewCmd(f),
	} {
		c.GroupID = "core"
		cmd.AddCommand(c)
	}
	cmd.AddCommand(helpTopics()...)
	usageErrors(cmd)

	plugins, err := pluginmgr.List()
	if err != nil {
		f.Debugf("list plugins: %v", err)
	}
	if len(plugins) > 0 {
		cmd.AddGroup(&cobra.Group{ID: "plugin", Title: "Plugins:"})
	}
	for _, p := range plugins {
		cmd.AddCommand(pluginStub(f, p))
	}
	return cmd
}

func unknownCommand(cmd *cobra.Command, name string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "unknown command %q for %q", name, cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
		sb.WriteString("\n\nDid you mean this?\n")
		for _, s := range suggestions {
			fmt.Fprintf(&sb, "\t%s\n", s)
		}
	}
	return errors.New(sb.String())
}

func pluginStub(f *cmdutil.Factory, p pluginmgr.Installed) *cobra.Command {
	name := p.Manifest.Name
	return &cobra.Command{
		Use:                name,
		Short:              p.Manifest.Summary,
		GroupID:            "plugin",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return pluginmgr.Run(cmd.Context(), f, name, args)
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			words, directive, err := pluginmgr.Complete(cmd.Context(), f, name, append(args, toComplete))
			if err != nil {
				cobra.CompErrorln(err.Error())
			}
			return words, directive
		},
	}
}

// Traverse does not reject unknown subcommands; a runnable group command must do it.
func groupRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return &cmdutil.UsageError{Cmd: cmd, Err: unknownCommand(cmd, args[0])}
}

func usageErrors(cmd *cobra.Command) {
	if !cmd.Runnable() && cmd.HasSubCommands() {
		cmd.RunE = groupRun
		cmd.SuggestionsMinimumDistance = 2
	}
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := validate(c, args); err != nil {
				return &cmdutil.UsageError{Err: err, Cmd: c}
			}
			return nil
		}
	}
	for _, c := range cmd.Commands() {
		usageErrors(c)
	}
}
