package plugin

import (
	"time"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

// infoRow is listRow plus what only info shows. Shared keys keep the
// names and meanings that "lfed plugin list -o json" gives them.
type infoRow struct {
	listRow
	// CatalogAlias, Owner and Homepage are search's keys. Owner and Homepage
	// are the catalog entry at install time, absent when none was recorded,
	// and can differ from what search shows now.
	CatalogAlias string                `json:"catalog,omitempty"`
	Owner        string                `json:"owner,omitempty"`
	Homepage     string                `json:"homepage,omitempty"`
	Binary       string                `json:"binary"`
	SourceURL    string                `json:"source_url,omitempty"`
	Verification string                `json:"verification"`
	Provenance   *pluginmgr.Provenance `json:"provenance"`
}

func newInfoRow(p pluginmgr.Installed, running string) infoRow {
	r := infoRow{
		listRow: listRow{
			Manifest:   p.Manifest,
			Source:     p.Source,
			Compatible: pluginmgr.CheckLfedVersion(p.Manifest.RequiresLfed, running) == nil,
		},
		Binary:     p.Binary,
		Provenance: p.Provenance,
	}
	switch {
	case p.Source.Type == pluginmgr.SourcePath:
		r.Verification = "local"
	case p.Provenance == nil:
		r.Verification = "not-recorded"
	case p.Provenance.Signature != nil:
		r.Verification = "signed"
	default:
		r.Verification = "unsigned"
	}
	if p.Source.Type == pluginmgr.SourceCatalog {
		r.CatalogAlias = pluginmgr.CatalogLabel(p.Source.Catalog)
	}
	if p.Provenance != nil && p.Provenance.CatalogEntry != nil {
		r.Owner, r.Homepage = p.Provenance.CatalogEntry.Owner, p.Provenance.CatalogEntry.Homepage
	}
	if c := r.code(); c != nil {
		r.SourceURL = c.URL()
	}
	return r
}

func (r infoRow) code() *pluginmgr.SourceCode {
	if r.Provenance == nil {
		return nil
	}
	return r.Provenance.Code
}

func (r infoRow) codeText() string {
	switch {
	case r.SourceURL != "":
		return r.SourceURL
	case r.Source.Type == pluginmgr.SourcePath:
		return "unknown: a local file carries no source facts"
	case r.Provenance == nil:
		return `not recorded: run "lfed plugin install ` + installArg(r.Source) + `" again`
	}
	return "unknown: the publisher did not state it"
}

// installArg is the "lfed plugin install" argument that installs src
// again, unpinned.
func installArg(src pluginmgr.Source) string {
	switch {
	case src.Type != pluginmgr.SourceCatalog:
		return src.Ref
	case src.Catalog != "":
		return src.Catalog + "/" + src.Name()
	}
	return src.Name()
}

func (r infoRow) basis() string {
	c := r.code()
	if c == nil {
		return ""
	}
	switch c.Basis {
	case pluginmgr.BasisCertificate:
		return "signing certificate"
	case pluginmgr.BasisSignedAnnotation:
		return "publisher annotation, signed"
	}
	return "publisher annotation, NOT verified"
}

func (r infoRow) signature() pluginmgr.Signature {
	if r.Provenance == nil || r.Provenance.Signature == nil {
		return pluginmgr.Signature{}
	}
	return *r.Provenance.Signature
}

var infoColumns = []output.Column[infoRow]{
	{Header: "name", Value: func(r infoRow) string { return r.Name }},
	{Header: "version", Value: func(r infoRow) string { return r.Version }},
	{Header: "status", Value: func(r infoRow) string { return r.status() }},
	{Header: "catalog", Value: func(r infoRow) string { return r.catalog() }},
	{Header: "source", Value: func(r infoRow) string { return r.Source.String() }},
	{Header: "summary", Value: func(r infoRow) string { return r.Summary }},
	{Header: "requires lfed", Value: func(r infoRow) string { return r.RequiresLfed }},
	{Header: "owner", Value: func(r infoRow) string { return r.Owner }},
	{Header: "homepage", Value: func(r infoRow) string { return r.Homepage }},
	{Header: "installed", Value: func(r infoRow) string {
		if r.Provenance == nil {
			return ""
		}
		return r.Provenance.InstalledAt.Format(time.RFC3339)
	}},
	{Header: "digest", Value: func(r infoRow) string { return r.Source.Digest }},
	{Header: "binary", Value: func(r infoRow) string { return r.Binary }},
	{Header: "code", Value: infoRow.codeText},
	{Header: "revision", Value: func(r infoRow) string {
		if c := r.code(); c != nil {
			return c.Revision
		}
		return ""
	}},
	{Header: "basis", Value: infoRow.basis},
	{Header: "signer", Value: func(r infoRow) string { return r.signature().Identity }},
	{Header: "issuer", Value: func(r infoRow) string { return r.signature().Issuer }},
	{Header: "build run", Value: func(r infoRow) string { return r.signature().BuildRun }},
}

func newInfoCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show where an installed plugin and its source code came from",
		Long: cmdutil.Long(`
			Show what lfed recorded when it installed a plugin: its source,
			catalog owner, OCI digest, verified signer, and the repository
			and commit of its source code.

			CODE comes from the signing certificate when the certificate
			names a repository. Otherwise it is the publisher's
			org.opencontainers.image.source annotation, and BASIS says
			whether a signature covered it. lfed reads only the install
			record. A plugin installed by an older lfed has no record of
			these facts until it is installed again.

			These facts are what lfed verified when it installed the
			plugin. info does not contact the registry or verify again.
			The record sits in the plugin directory beside the binary, so
			it is as trustworthy as that directory.
		`),
		Example: cmdutil.Examples(`
			$ lfed plugin info monkeysay

			# Print only the source code URL
			$ lfed plugin info monkeysay --jq .source_url
		`),
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			inst, err := pluginmgr.Lookup(args[0])
			if err != nil {
				return err
			}
			return output.PrintDetail(p, newInfoRow(inst, f.Version), infoColumns)
		},
	}
}
