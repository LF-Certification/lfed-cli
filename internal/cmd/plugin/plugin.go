package plugin

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install, list, upgrade and remove plugins",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newInstallCmd(f), newListCmd(f), newUpgradeCmd(f), newRemoveCmd(f), newSearchCmd(f), newCatalogCmd(f))
	return cmd
}

func addSignerFlags(cmd *cobra.Command, opts *pluginmgr.Options) {
	fl := cmd.Flags()
	fl.BoolVar(&opts.AllowUnsigned, "allow-unsigned", false, "Skip the signature check of the plugin catalog and the plugin. Use this only for a registry that you control.")
	fl.StringVar(&opts.CertificateIdentityRegexp, "certificate-identity-regexp", "", "Regular expression that the signer certificate identity MUST match. Set it with --certificate-oidc-issuer.")
	fl.StringVar(&opts.CertificateOIDCIssuer, "certificate-oidc-issuer", "", "OIDC issuer URL of the signer certificate. Set it with --certificate-identity-regexp.")
	fl.StringVar(&opts.Catalog, "catalog", "", "Default plugin catalog: an OCI reference, a file path, or a file:// URL. Default: LFED_CATALOG, then config.yaml catalogs.default.ref, then "+pluginmgr.DefaultCatalog+". A <catalog>/<name> argument ignores it.")
}

func newInstallCmd(f *cmdutil.Factory) *cobra.Command {
	var opts pluginmgr.Options
	cmd := &cobra.Command{
		Use:   "install <plugin>...",
		Short: "Install plugins from a plugin catalog, an OCI reference, or a local file",
		Long: cmdutil.Long(`
			Install one or more plugins. Each <plugin> is
			[<catalog>/]<name>[@<version>|@sha256:<digest>],
			<registry>/<repository>[:<version>|@sha256:<digest>], or <path>.

			lfed installs each <plugin> in order. A failure does not stop the
			others. lfed reports each failure and exits non-zero at the end.
			The signer flags and --catalog apply to every <plugin>.

			A bare <name> resolves through the default plugin catalog to an OCI
			repository. <catalog>/<name> resolves through the catalog with that
			alias in config.yaml; see "lfed plugin catalog". lfed installs the
			highest version that supports this lfed. <name>@<version> pins a
			version. <name>@sha256:<digest> pins an OCI index digest.
			"lfed plugin upgrade" skips a pinned plugin. A full OCI reference
			installs a plugin that is not in a plugin catalog.

			A plugin keeps the catalog it came from. To install the same name
			from another catalog, run "lfed plugin remove <name>" first.

			A plugin from a registry MUST carry a Sigstore signature. Run
			"lfed help plugins" for the trust model.

			A local <path> MUST start with ./, ../, / or ~/ and name the plugin
			executable. lfed does not verify a local plugin.
		`),
		Example: cmdutil.Examples(`
			# Install the newest version that supports this lfed
			$ lfed plugin install monkeysay

			# Pin a version
			$ lfed plugin install monkeysay@1.2.0

			# Install from a plugin catalog that "lfed plugin catalog add acme ..." registered
			$ lfed plugin install acme/reports

			# Install a plugin that is not in a plugin catalog and name its signer
			$ lfed plugin install ghcr.io/example/lfed-cli-hello:1.0.0 \
			    --certificate-identity-regexp '^https://github.com/example/' \
			    --certificate-oidc-issuer https://token.actions.githubusercontent.com

			# Verify entries of a plugin catalog that is not published yet
			$ lfed plugin install hello --catalog ./catalog.json

			# Install a local build
			$ lfed plugin install ./lfed-hello

			# Install several plugins from different plugin catalogs
			$ lfed plugin install monkeysay acme/reports ./lfed-hello
		`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Reserved = reservedNames(cmd.Root())
			ctx := cmd.Context()
			var errs []error
			for _, arg := range args {
				if ctx.Err() != nil {
					break
				}
				p, err := pluginmgr.Install(ctx, f, arg, opts)
				if err != nil {
					if len(args) == 1 {
						return err
					}
					if ctx.Err() != nil {
						break
					}
					errs = append(errs, fmt.Errorf("%s: %w", arg, err))
					continue
				}
				if f.IOStreams.IsStdoutTTY() {
					cs := f.IOStreams.ColorScheme()
					fmt.Fprintf(f.IOStreams.Out, "%s Installed plugin %s %s from %s\n", cs.SuccessIcon(), p.Manifest.Name, p.Manifest.Version, p.Source)
				}
			}
			// main prints nothing for an error that wraps context.Canceled,
			// so the failures collected before the interrupt go back alone.
			if len(errs) == 0 && ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.Join(errs...)
		},
	}
	addSignerFlags(cmd, &opts)
	return cmd
}

func reservedNames(root *cobra.Command) []string {
	names := []string{"help", "completion"}
	for _, c := range root.Commands() {
		if c.GroupID == "plugin" {
			continue
		}
		names = append(names, c.Name())
	}
	return names
}

type listRow struct {
	sdkplugin.Manifest
	Source pluginmgr.Source `json:"source"`

	Compatible bool `json:"compatible"`
}

func (r listRow) status() string {
	if r.Compatible {
		return "ok"
	}
	return "needs lfed " + r.RequiresLfed
}

func (r listRow) catalog() string {
	if r.Source.Type != pluginmgr.SourceCatalog {
		return "-"
	}
	return pluginmgr.CatalogLabel(r.Source.Catalog)
}

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			installed, err := pluginmgr.List()
			if err != nil {
				return err
			}
			rows := make([]listRow, 0, len(installed))
			for _, p := range installed {
				rows = append(rows, listRow{
					Manifest:   p.Manifest,
					Source:     p.Source,
					Compatible: pluginmgr.CheckLfedVersion(p.Manifest.RequiresLfed, f.Version) == nil,
				})
			}
			return output.Print(p, rows, []output.Column[listRow]{
				{Header: "name", Value: func(r listRow) string { return r.Name }},
				{Header: "version", Value: func(r listRow) string { return r.Version }},
				{Header: "status", Value: listRow.status},
				{Header: "catalog", Value: listRow.catalog},
				{Header: "source", Value: func(r listRow) string { return r.Source.String() }},
				{Header: "summary", Value: func(r listRow) string { return r.Summary }},
			})
		},
	}
}

func newRemoveCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed plugin",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := pluginmgr.Remove(args[0]); err != nil {
				return err
			}
			if f.IOStreams.IsStdoutTTY() {
				cs := f.IOStreams.ColorScheme()
				fmt.Fprintf(f.IOStreams.Out, "%s Removed plugin %s\n", cs.SuccessIcon(), args[0])
			}
			return nil
		},
	}
}

func newUpgradeCmd(f *cmdutil.Factory) *cobra.Command {
	var dryRun bool
	var opts pluginmgr.Options
	cmd := &cobra.Command{
		Use:   "upgrade [<name>...]",
		Short: "Upgrade installed plugins",
		Long: cmdutil.Long(`
			Upgrade each named plugin to the newest version that supports this
			lfed. With no names, lfed checks every installed plugin.

			lfed resolves each plugin against the source recorded at install
			time. A pinned plugin and a local plugin are listed and not changed.
			A failure for one plugin does not stop the others. The command
			exits 1 after it reports all results.
		`),
		Example: cmdutil.Examples(`
			# Show the upgrade plan
			$ lfed plugin upgrade --dry-run

			# Upgrade every plugin
			$ lfed plugin upgrade

			# Upgrade one plugin
			$ lfed plugin upgrade placement
		`),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			opts.Reserved = reservedNames(cmd.Root())
			results, upgradeErr := pluginmgr.Upgrade(cmd.Context(), f, args, dryRun, opts)
			if err := output.Print(p, results, []output.Column[pluginmgr.UpgradeResult]{
				{Header: "name", Value: func(r pluginmgr.UpgradeResult) string { return r.Name }},
				{Header: "from", Value: func(r pluginmgr.UpgradeResult) string { return r.From }},
				{Header: "to", Value: func(r pluginmgr.UpgradeResult) string { return r.To }},
				{Header: "result", Value: upgradeResult},
			}); err != nil {
				return err
			}
			return upgradeErr
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the upgrade plan. Do not install.")
	addSignerFlags(cmd, &opts)
	return cmd
}

func upgradeResult(r pluginmgr.UpgradeResult) string {
	switch {
	case r.Error != "":
		return "failed: " + r.Error
	case r.Upgraded:
		return "upgraded"
	}
	return r.Note
}

func Names() []string {
	installed, err := pluginmgr.List()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(installed))
	for _, p := range installed {
		names = append(names, p.Manifest.Name+"\t"+p.Manifest.Summary)
	}
	return names
}
