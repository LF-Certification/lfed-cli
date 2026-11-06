package context

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Select the deployment context: dev, staging or prod",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newListCmd(f), newUseCmd(f), newCurrentCmd(f))
	return cmd
}

type listRow struct {
	Name    string `json:"name"`
	APIBase string `json:"api_base_url"`
	Issuer  string `json:"issuer"`
	Current bool   `json:"current"`
}

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the contexts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			current, err := f.Context()
			if err != nil && err != contexts.ErrNoContext {
				return err
			}
			rows := make([]listRow, 0, len(contexts.All()))
			for _, c := range contexts.All() {
				rows = append(rows, listRow{
					Name:    string(c.Name),
					APIBase: c.APIBaseURL(),
					Issuer:  c.Issuer,
					Current: c.Name == current.Name,
				})
			}
			return output.Print(p, rows, []output.Column[listRow]{
				{Header: "current", Value: func(r listRow) string {
					if r.Current {
						return "*"
					}
					return ""
				}},
				{Header: "name", Value: func(r listRow) string { return r.Name }},
				{Header: "api", Value: func(r listRow) string { return r.APIBase }},
			})
		},
	}
}

type useResult struct {
	CurrentContext string `json:"current_context"`
}

func newUseCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:       "use <dev|staging|prod>",
		Short:     "Set the current context in the config file",
		Args:      cobra.ExactArgs(1),
		ValidArgs: contexts.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			c, err := contexts.Lookup(args[0])
			if err != nil {
				return err
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			cfg.CurrentContext = string(c.Name)
			if err := cfg.Save(); err != nil {
				return err
			}
			if p.Mode.Structured() {
				return p.PrintValue(useResult{CurrentContext: string(c.Name)})
			}
			_, err = fmt.Fprintf(f.IOStreams.ErrOut, "Switched to context %q.\n", c.Name)
			return err
		},
	}
}

type currentResult struct {
	Name string `json:"name"`
}

func newCurrentCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the active context name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			c, err := f.Context()
			if err != nil {
				return err
			}
			if p.Mode.Structured() {
				return p.PrintValue(currentResult{Name: string(c.Name)})
			}
			_, err = fmt.Fprintln(f.IOStreams.Out, c.Name)
			return err
		},
	}
}
