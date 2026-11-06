package version

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
)

type result struct {
	Version string `json:"version"`
}

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the lfed version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			if p.Mode.Structured() {
				return p.PrintValue(result{Version: f.Version})
			}
			_, err = fmt.Fprintf(f.IOStreams.Out, "lfed %s\n", f.Version)
			return err
		},
	}
}
