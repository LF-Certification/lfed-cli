package plugin

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

type searchRow struct {
	Name      string `json:"name"`
	Catalog   string `json:"catalog"`
	Summary   string `json:"summary"`
	Owner     string `json:"owner"`
	Homepage  string `json:"homepage,omitempty"`
	Ref       string `json:"ref"`
	Installed string `json:"installed"`
}

func newSearchCmd(f *cmdutil.Factory) *cobra.Command {
	var opts pluginmgr.Options
	cmd := &cobra.Command{
		Use:   "search [<catalog>/]<text> | <catalog>/",
		Short: "Search plugin catalogs",
		Long: cmdutil.Long(`
			Search the plugin catalogs for plugins whose name, summary or owner
			contains <text>, ignoring case.

			A <catalog>/ prefix searches only that catalog: "acme/" lists every
			plugin in catalog acme. The NAME column is the argument that
			"lfed plugin install" takes.
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			query := args[0]
			stop := f.IOStreams.StartProgress("Searching plugin catalogs")
			results, warnings, err := pluginmgr.Search(cmd.Context(), f, query, opts)
			stop()
			if err != nil {
				return err
			}
			for _, w := range warnings {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: skipped plugin catalog %s: %v\n", pluginmgr.CatalogLabel(w.Alias), w.Err)
			}
			if len(results) == 0 {
				return fmt.Errorf("no plugins match %q", query)
			}
			rows := make([]searchRow, 0, len(results))
			for _, r := range results {
				rows = append(rows, searchRow{
					Name: r.InstallName(), Catalog: pluginmgr.CatalogLabel(r.Catalog), Summary: r.Summary,
					Owner: r.Owner, Homepage: r.Homepage, Ref: r.Ref, Installed: r.Installed,
				})
			}
			return output.Print(p, rows, []output.Column[searchRow]{
				{Header: "name", Value: func(r searchRow) string { return r.Name }},
				{Header: "installed", Value: func(r searchRow) string { return dash(r.Installed) }},
				{Header: "owner", Value: func(r searchRow) string { return r.Owner }},
				{Header: "summary", Value: func(r searchRow) string { return r.Summary }},
			})
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&opts.AllowUnsigned, "allow-unsigned", false, "Skip the signature check of the plugin catalogs. Use this only for a registry that you control.")
	fl.StringVar(&opts.Catalog, "catalog", "", "Default plugin catalog: an OCI reference, a file path, or a file:// URL. Default: LFED_CATALOG, then config.yaml catalogs.default.ref, then "+pluginmgr.DefaultCatalog+".")
	return cmd
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
