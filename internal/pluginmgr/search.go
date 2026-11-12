package pluginmgr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
)

// SearchResult is one catalog entry that matched a search. Catalog is the
// alias that holds it, empty for the default catalog.
type SearchResult struct {
	CatalogEntry
	Catalog   string
	Installed string
}

// InstallName is the argument that "lfed plugin install" takes for r.
func (r SearchResult) InstallName() string {
	if r.Catalog == "" {
		return r.Name
	}
	return r.Catalog + "/" + r.Name
}

// CatalogWarning reports a catalog that could not be searched. Search
// keeps going with the other catalogs.
type CatalogWarning struct {
	Alias string
	Err   error
}

// ParseQuery splits "acme/text" into a catalog alias and the text. A query
// without "/" searches every catalog.
func ParseQuery(q string) (alias, text string, scoped bool, err error) {
	alias, text, scoped = strings.Cut(q, "/")
	if !scoped {
		return "", q, false, nil
	}
	if err := ValidCatalogAlias(alias); err != nil {
		return "", "", false, err
	}
	if alias == DefaultCatalogAlias {
		alias = ""
	}
	return alias, text, true, nil
}

// Search matches query against the name, summary and owner of each entry
// in the configured catalogs, case-insensitively. Name matches sort first.
func Search(ctx context.Context, f *cmdutil.Factory, query string, opts Options) ([]SearchResult, []CatalogWarning, error) {
	alias, text, scoped, err := ParseQuery(query)
	if err != nil {
		return nil, nil, err
	}
	if !scoped && strings.TrimSpace(text) == "" {
		return nil, nil, &cmdutil.UsageError{Err: errors.New("search text is empty; pass <text>, or <catalog>/ to list a catalog")}
	}
	aliases := []string{alias}
	if !scoped {
		if aliases, err = catalogAliases(f); err != nil {
			return nil, nil, err
		}
	}
	installed := map[string]Installed{}
	if list, err := List(); err == nil {
		for _, p := range list {
			installed[p.Manifest.Name] = p
		}
	}
	text = strings.ToLower(text)
	var results []SearchResult
	var warnings []CatalogWarning
	for _, a := range aliases {
		c, _, err := loadCatalog(ctx, f, opts, a)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, nil, ctxErr
			}
			if scoped {
				return nil, nil, err
			}
			warnings = append(warnings, CatalogWarning{Alias: a, Err: err})
			continue
		}
		for _, e := range c.Plugins {
			if !matches(e, text) {
				continue
			}
			r := SearchResult{CatalogEntry: e, Catalog: a}
			if p, ok := installed[e.Name]; ok && p.Source.Type == SourceCatalog && p.Source.Catalog == a {
				r.Installed = p.Manifest.Version
			}
			results = append(results, r)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		ni, nj := strings.Contains(results[i].Name, text), strings.Contains(results[j].Name, text)
		if ni != nj {
			return ni
		}
		return results[i].InstallName() < results[j].InstallName()
	})
	return results, warnings, nil
}

func matches(e CatalogEntry, text string) bool {
	for _, s := range []string{e.Name, e.Summary, e.Owner} {
		if strings.Contains(strings.ToLower(s), text) {
			return true
		}
	}
	return false
}

// catalogAliases lists the default catalog, as "", then each alias in
// config.yaml catalogs in order.
func catalogAliases(f *cmdutil.Factory) ([]string, error) {
	aliases := []string{""}
	if f.Config == nil {
		return aliases, nil
	}
	cfg, err := f.Config()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var named []string
	for a := range cfg.Catalogs {
		if a != DefaultCatalogAlias {
			named = append(named, a)
		}
	}
	sort.Strings(named)
	return append(aliases, named...), nil
}
