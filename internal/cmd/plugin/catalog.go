package plugin

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/config"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

func newCatalogCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Add, list and remove plugin catalogs",
		Long: cmdutil.Long(`
			Manage the plugin catalogs in config.yaml.

			A plugin catalog is a signed OCI artifact or a local JSON file that
			maps plugin names to OCI repositories and signers. The built-in catalog
			resolves a bare <name>. A catalog that you add resolves
			<alias>/<name>. The alias "default" replaces the built-in catalog.

			Run "lfed help plugins" for the trust model.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newCatalogAddCmd(f), newCatalogListCmd(f), newCatalogRemoveCmd(f))
	return cmd
}

func newCatalogAddCmd(f *cmdutil.Factory) *cobra.Command {
	var signer config.CatalogSigner
	cmd := &cobra.Command{
		Use:   "add <alias> <ref>",
		Short: "Add a plugin catalog to config.yaml",
		Long: cmdutil.Long(`
			Add a plugin catalog under <alias>. <ref> is an OCI reference with a
			tag or digest, a file path, or a file:// URL.

			An OCI plugin catalog MUST name its signer with --signer-issuer and
			--signer-identity. lfed verifies the catalog against that identity
			before it reads an entry. A file catalog has no signer.

			The alias "default" replaces the built-in catalog that a bare
			<name> resolves through. Its signer is optional and defaults to
			the lfed-cli catalog-publish workflow.
		`),
		Example: cmdutil.Examples(`
			# Add a plugin catalog that a GitHub Actions workflow publishes and signs
			$ lfed plugin catalog add acme ghcr.io/acme/lfed-catalog:latest \
			    --signer-issuer https://token.actions.githubusercontent.com \
			    --signer-identity https://github.com/acme/lfed-catalog/.github/workflows/publish.yml@refs/heads/main

			# Add a local plugin catalog file for a team
			$ lfed plugin catalog add team ~/src/team/catalog.json
		`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, ref := args[0], args[1]
			if err := pluginmgr.ValidCatalogAlias(alias); err != nil {
				return &cmdutil.UsageError{Err: err}
			}
			if err := pluginmgr.CheckCatalogRef(ref); err != nil {
				return &cmdutil.UsageError{Err: err}
			}
			if (signer.Issuer == "") != (signer.Identity == "") {
				return &cmdutil.UsageError{Err: errors.New("set --signer-issuer and --signer-identity together")}
			}
			if signer.IsZero() && !pluginmgr.CatalogIsFile(ref) && alias != pluginmgr.DefaultCatalogAlias {
				return &cmdutil.UsageError{Err: fmt.Errorf("plugin catalog %s needs --signer-issuer and --signer-identity", ref)}
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if _, ok := cfg.Catalogs[alias]; ok {
				return fmt.Errorf("plugin catalog %q exists; run \"lfed plugin catalog remove %s\" first", alias, alias)
			}
			if cfg.Catalogs == nil {
				cfg.Catalogs = map[string]config.Catalog{}
			}
			cfg.Catalogs[alias] = config.Catalog{Ref: ref, Signer: signer}
			if err := cfg.Save(); err != nil {
				return err
			}
			if f.IOStreams.IsStdoutTTY() {
				cs := f.IOStreams.ColorScheme()
				fmt.Fprintf(f.IOStreams.Out, "%s Added catalog %s (%s)\n", cs.SuccessIcon(), alias, ref)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&signer.Issuer, "signer-issuer", "", "OIDC issuer URL of the certificate that signed the catalog. Set it with --signer-identity.")
	fl.StringVar(&signer.Identity, "signer-identity", "", "Exact certificate identity (SAN) that signed the catalog. Set it with --signer-issuer.")
	return cmd
}

type catalogRow struct {
	Alias    string `json:"alias"`
	Ref      string `json:"ref"`
	Issuer   string `json:"issuer,omitempty"`
	Identity string `json:"identity,omitempty"`

	// BuiltIn marks the default catalog when config.yaml does not set it.
	BuiltIn bool `json:"built_in,omitempty"`
}

func (r catalogRow) signer() string {
	if r.Identity == "" {
		return "-"
	}
	return r.Identity
}

func newCatalogListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List plugin catalogs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			rows := make([]catalogRow, 0, len(cfg.Catalogs)+1)
			for alias, c := range cfg.Catalogs {
				rows = append(rows, catalogRow{Alias: alias, Ref: c.Ref, Issuer: c.Signer.Issuer, Identity: c.Signer.Identity})
			}
			if _, ok := cfg.Catalogs[pluginmgr.DefaultCatalogAlias]; !ok {
				rows = append(rows, catalogRow{
					Alias:    pluginmgr.DefaultCatalogAlias,
					Ref:      pluginmgr.DefaultCatalog,
					Issuer:   pluginmgr.DefaultCatalogIssuer,
					Identity: pluginmgr.DefaultCatalogIdentity,
					BuiltIn:  true,
				})
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Alias == pluginmgr.DefaultCatalogAlias {
					return true
				}
				if rows[j].Alias == pluginmgr.DefaultCatalogAlias {
					return false
				}
				return rows[i].Alias < rows[j].Alias
			})
			return output.Print(p, rows, []output.Column[catalogRow]{
				{Header: "alias", Value: func(r catalogRow) string { return r.Alias }},
				{Header: "ref", Value: func(r catalogRow) string { return r.Ref }},
				{Header: "signer", Value: catalogRow.signer},
			})
		},
	}
}

func newCatalogRemoveCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <alias>",
		Short: "Remove a plugin catalog from config.yaml",
		Long: cmdutil.Long(`
			Remove the plugin catalog <alias> from config.yaml. Plugins installed
			from it stay installed; "lfed plugin upgrade" reports them until
			the catalog is added again or the plugin is removed. Removing
			"default" restores the built-in catalog.
		`),
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			cfg, err := f.Config()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			aliases := make([]string, 0, len(cfg.Catalogs))
			for a := range cfg.Catalogs {
				aliases = append(aliases, a)
			}
			sort.Strings(aliases)
			return aliases, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if _, ok := cfg.Catalogs[alias]; !ok {
				return fmt.Errorf("no plugin catalog %q in config.yaml", alias)
			}
			delete(cfg.Catalogs, alias)
			if len(cfg.Catalogs) == 0 {
				cfg.Catalogs = nil
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			if f.IOStreams.IsStdoutTTY() {
				cs := f.IOStreams.ColorScheme()
				fmt.Fprintf(f.IOStreams.Out, "%s Removed catalog %s\n", cs.SuccessIcon(), alias)
			}
			return nil
		},
	}
}
