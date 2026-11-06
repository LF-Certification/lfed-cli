package pluginmgr

import (
	"context"
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
)

type UpgradeResult struct {
	Name string `json:"name"`
	From string `json:"from"`

	To       string `json:"to,omitempty"`
	Upgraded bool   `json:"upgraded"`

	Note string `json:"note,omitempty"`

	Error string `json:"error,omitempty"`
}

func Upgrade(ctx context.Context, f *cmdutil.Factory, names []string, dryRun bool, opts Options) ([]UpgradeResult, error) {
	var results []UpgradeResult
	var errs []error
	record := func(r UpgradeResult, err error) {
		if err != nil {
			r.Error = err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
		}
		results = append(results, r)
	}
	if len(names) == 0 {
		plugins, err := List()
		if err != nil {
			return nil, err
		}
		for _, p := range plugins {
			record(upgradeOne(ctx, f, p, dryRun, opts))
		}
		return results, errors.Join(errs...)
	}
	for _, n := range names {
		p, err := Lookup(n)
		if err != nil {
			record(UpgradeResult{Name: n}, err)
			continue
		}
		record(upgradeOne(ctx, f, p, dryRun, opts))
	}
	return results, errors.Join(errs...)
}

func upgradeOne(ctx context.Context, f *cmdutil.Factory, p Installed, dryRun bool, opts Options) (UpgradeResult, error) {
	r := UpgradeResult{Name: p.Manifest.Name, From: p.Manifest.Version}
	switch p.Source.Type {
	case SourceOCI, SourceCatalog:
	case SourcePath:
		r.Note = fmt.Sprintf("installed from %s; reinstall with \"lfed plugin install %s\"", p.Source.Path, p.Source.Path)
		return r, nil
	default:
		r.Note = "source unknown; reinstall with \"lfed plugin install\""
		return r, nil
	}
	if p.Source.Pinned {
		pin := p.Source.Version
		if p.Source.digestPin() {
			pin = p.Source.Digest
		}
		r.Note = fmt.Sprintf("pinned to %s; reinstall to change", pin)
		return r, nil
	}
	want := Source{Type: p.Source.Type, Ref: p.Source.Ref, Catalog: p.Source.Catalog}
	var entry *CatalogEntry
	var err error
	if want.Type == SourceOCI {
		// A host before the catalog recorded bare-name installs as an OCI
		// source. When the default catalog lists the name at the same
		// repository, the plugin is that catalog plugin and upgrades
		// through the catalog.
		c := Source{Type: SourceCatalog, Ref: p.Manifest.Name}
		c, e, err := resolveCatalog(ctx, f, c, opts)
		var notFound *CatalogNotFoundError
		switch {
		case err == nil && c.Ref == p.Source.Ref:
			want, entry = c, e
		case err != nil && !errors.As(err, &notFound):
			return r, err
		}
	} else if want.Type == SourceCatalog {
		// The recorded catalog is the authority: a plugin MAY move
		// repository or change signer between versions.
		want.Ref = p.Manifest.Name
		if want, entry, err = resolveCatalog(ctx, f, want, opts); err != nil {
			return r, err
		}
	}
	repo, err := ociRepository(want)
	if err != nil {
		return r, err
	}
	res, err := resolveOCI(ctx, repo, want, f.Version)
	if err != nil {
		return r, err
	}
	r.To = res.Version
	if !newer(res.Version, p.Source.Version) {
		r.Note = "current"
		return r, nil
	}
	if dryRun {
		r.Note = "dry run"
		return r, nil
	}
	if _, err := installOCI(ctx, f, repo, want, res, opts, entry); err != nil {
		return r, err
	}
	r.Upgraded = true
	return r, nil
}

func newer(candidate, installed string) bool {
	c, err1 := semver.NewVersion(candidate)
	i, err2 := semver.NewVersion(installed)
	if err1 != nil || err2 != nil {
		return candidate != installed
	}
	return c.GreaterThan(i)
}
