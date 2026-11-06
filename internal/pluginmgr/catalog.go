package pluginmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/config"
)

// The catalog is the list of plugins that a bare <name> can resolve to.
// It is authored in catalog/plugins of this repository and published by
// .github/workflows/catalog-publish.yml as a signed OCI artifact. The host
// pins the identity of that workflow.
const (
	DefaultCatalog = "ghcr.io/lf-certification/lfed-cli-plugins/catalog:latest"

	// DefaultCatalogAlias is the config.yaml catalogs key that overrides
	// DefaultCatalog. A bare plugin name resolves through this catalog.
	DefaultCatalogAlias = "default"

	CatalogArtifactType = "application/vnd.linuxfoundation.ed.cli.catalog.v1"

	CatalogMediaType = "application/vnd.linuxfoundation.ed.cli.catalog.v1+json"

	CatalogSchemaVersion = 1

	DefaultCatalogIssuer = githubIssuer

	DefaultCatalogIdentity = "https://github.com/LF-Certification/lfed-cli/.github/workflows/catalog-publish.yml@refs/heads/main"

	maxCatalogBytes = 4 << 20
)

// CatalogAliasRe bounds a catalog alias. The alias MUST NOT contain "."
// or ":" so that <alias>/<name> never parses as an OCI reference.
var CatalogAliasRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidCatalogAlias reports whether alias can key config.yaml catalogs.
func ValidCatalogAlias(alias string) error {
	if !CatalogAliasRe.MatchString(alias) {
		return fmt.Errorf("plugin catalog alias %q must match %s", alias, CatalogAliasRe)
	}
	if alias == "localhost" {
		return fmt.Errorf("plugin catalog alias %q is reserved for OCI references", alias)
	}
	return nil
}

// CatalogLabel names a catalog alias in messages.
func CatalogLabel(alias string) string {
	if alias == "" {
		return DefaultCatalogAlias
	}
	return alias
}

type Catalog struct {
	SchemaVersion int            `json:"schema_version"`
	Plugins       []CatalogEntry `json:"plugins"`
}

type CatalogEntry struct {
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Homepage string `json:"homepage,omitempty"`
	Owner    string `json:"owner"`
	Ref      string `json:"ref"`
	Signer   Signer `json:"signer"`
}

// Signer names the certificate that MUST have signed a plugin. Identity
// is an exact SAN with the placeholder {version}, and optionally {name}.
type Signer struct {
	Issuer   string `json:"issuer"`
	Identity string `json:"identity"`
}

func (s Signer) expand(name, version string) string {
	r := strings.NewReplacer("{version}", version, "{name}", name)
	return r.Replace(s.Identity)
}

type CatalogNotFoundError struct {
	Name    string
	Alias   string
	Catalog string
}

func (e *CatalogNotFoundError) Error() string {
	if e.Alias != "" {
		return fmt.Sprintf("plugin %q is not in plugin catalog %s (%s)", e.Name, e.Alias, e.Catalog)
	}
	return fmt.Sprintf("plugin %q is not in the plugin catalog %s; use <catalog>/<name> for a plugin catalog in config.yaml, or pass a full OCI reference", e.Name, e.Catalog)
}

func (c *Catalog) Lookup(name string) (CatalogEntry, bool) {
	for _, e := range c.Plugins {
		if e.Name == name {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

var ownerRe = regexp.MustCompile(`^[A-Za-z0-9-]+(/[A-Za-z0-9._-]+)?$`)

func parseCatalog(data []byte, from string) (*Catalog, error) {
	var c Catalog
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse plugin catalog %s: %w", from, err)
	}
	if c.SchemaVersion != CatalogSchemaVersion {
		return nil, fmt.Errorf("plugin catalog %s has schema_version %d; this lfed reads %d", from, c.SchemaVersion, CatalogSchemaVersion)
	}
	seen := map[string]bool{}
	for i, e := range c.Plugins {
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("plugin catalog %s: plugins[%d]: %w", from, i, err)
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("plugin catalog %s: plugin %q is listed twice", from, e.Name)
		}
		seen[e.Name] = true
	}
	sort.Slice(c.Plugins, func(i, j int) bool { return c.Plugins[i].Name < c.Plugins[j].Name })
	return &c, nil
}

func (e CatalogEntry) validate() error {
	if !nameRe.MatchString(e.Name) {
		return fmt.Errorf("name %q does not match %s", e.Name, nameRe)
	}
	if e.Summary == "" {
		return fmt.Errorf("%s: summary is empty", e.Name)
	}
	if !ownerRe.MatchString(e.Owner) {
		return fmt.Errorf("%s: owner %q is not <org> or <org>/<team>", e.Name, e.Owner)
	}
	ref, err := registry.ParseReference(e.Ref)
	if err != nil {
		return fmt.Errorf("%s: ref %q: %w", e.Name, e.Ref, err)
	}
	if ref.Reference != "" {
		return fmt.Errorf("%s: ref %q MUST NOT carry a tag or digest", e.Name, e.Ref)
	}
	if path.Base(ref.Repository) != e.Name {
		return fmt.Errorf("%s: ref %q MUST end in /%s", e.Name, e.Ref, e.Name)
	}
	if u, err := url.Parse(e.Signer.Issuer); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s: signer.issuer %q is not an https URL", e.Name, e.Signer.Issuer)
	}
	if !strings.Contains(e.Signer.Identity, "{version}") {
		return fmt.Errorf("%s: signer.identity %q has no {version} placeholder", e.Name, e.Signer.Identity)
	}
	if e.Homepage != "" {
		if u, err := url.Parse(e.Homepage); err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%s: homepage %q is not an https URL", e.Name, e.Homepage)
		}
	}
	return nil
}

// catalogSource returns the catalog location and its expected signer.
//
// For the default catalog (alias "" or "default") the location precedence
// is: --catalog, LFED_CATALOG, config.yaml catalogs.default.ref, built-in
// default. The config signer applies when the location is the config
// catalog, or when the config names a signer but no ref. A flag or
// variable that points at another catalog gets the default signer.
//
// Any other alias MUST be a key of config.yaml catalogs. Its ref is the
// location. An OCI ref needs the configured signer unless
// opts.AllowUnsigned is set.
func catalogSource(f *cmdutil.Factory, opts Options, alias string) (string, Signer, error) {
	var cfg *config.Config
	if f.Config != nil {
		var err error
		if cfg, err = f.Config(); err != nil {
			return "", Signer{}, err
		}
	}
	if alias != "" && alias != DefaultCatalogAlias {
		var c config.Catalog
		if cfg != nil {
			c = cfg.Catalogs[alias]
		}
		if c.Ref == "" {
			return "", Signer{}, fmt.Errorf("no plugin catalog %q in config.yaml catalogs; add it with \"lfed plugin catalog add %s <ref>\"", alias, alias)
		}
		if err := checkSigner(alias, c.Signer); err != nil {
			return "", Signer{}, err
		}
		if c.Signer.IsZero() && !CatalogIsFile(c.Ref) && !opts.AllowUnsigned {
			return "", Signer{}, fmt.Errorf("plugin catalog %s has no signer in config.yaml; set catalogs.%s.signer or pass --allow-unsigned", alias, alias)
		}
		return c.Ref, Signer{Issuer: c.Signer.Issuer, Identity: c.Signer.Identity}, nil
	}
	ref := opts.Catalog
	if ref == "" {
		ref = os.Getenv("LFED_CATALOG")
	}
	s := Signer{Issuer: DefaultCatalogIssuer, Identity: DefaultCatalogIdentity}
	if cfg != nil {
		c := cfg.Catalogs[DefaultCatalogAlias]
		if ref == "" {
			ref = c.Ref
		}
		if err := checkSigner(DefaultCatalogAlias, c.Signer); err != nil {
			return "", Signer{}, err
		}
		if !c.Signer.IsZero() && (c.Ref == "" || ref == c.Ref) {
			s = Signer{Issuer: c.Signer.Issuer, Identity: c.Signer.Identity}
		}
	}
	if ref == "" {
		ref = DefaultCatalog
	}
	return ref, s, nil
}

func checkSigner(alias string, s config.CatalogSigner) error {
	if (s.Issuer == "") != (s.Identity == "") {
		return fmt.Errorf("config catalogs.%s.signer needs both issuer and identity", alias)
	}
	return nil
}

// CheckCatalogRef validates a catalog location before it is saved: a
// file path, or an OCI reference with a tag or digest.
func CheckCatalogRef(ref string) error {
	if CatalogIsFile(ref) {
		return nil
	}
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("invalid plugin catalog reference %q: %w", ref, err)
	}
	if parsed.Reference == "" {
		return fmt.Errorf("plugin catalog reference %q needs a tag or digest", ref)
	}
	return nil
}

func CatalogIsFile(ref string) bool {
	return strings.HasPrefix(ref, "file://") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") ||
		strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~/") || strings.HasSuffix(ref, ".json")
}

// loadCatalog fetches and verifies the catalog named by alias. A file
// path or file:// URL loads a local catalog without a signature check:
// the caller put it there. An OCI reference is verified against the
// catalog signer unless opts.AllowUnsigned is set. Verified catalogs are
// cached by digest.
func loadCatalog(ctx context.Context, f *cmdutil.Factory, opts Options, alias string) (*Catalog, string, error) {
	ref, s, err := catalogSource(f, opts, alias)
	if err != nil {
		return nil, "", err
	}
	if CatalogIsFile(ref) {
		p := strings.TrimPrefix(ref, "file://")
		if strings.HasPrefix(p, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, "", err
			}
			p = filepath.Join(home, p[2:])
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("read plugin catalog: %w", err)
		}
		c, err := parseCatalog(data, p)
		return c, p, err
	}
	data, err := fetchCatalog(ctx, ref, s, opts.AllowUnsigned)
	if err != nil {
		return nil, "", err
	}
	c, err := parseCatalog(data, ref)
	return c, ref, err
}

func fetchCatalog(ctx context.Context, ref string, s Signer, allowUnsigned bool) ([]byte, error) {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid plugin catalog reference %q: %w", ref, err)
	}
	if parsed.Reference == "" {
		return nil, fmt.Errorf("plugin catalog reference %q needs a tag or digest", ref)
	}
	repo, err := ociRepository(Source{Ref: parsed.Registry + "/" + parsed.Repository})
	if err != nil {
		return nil, err
	}
	desc, data, err := fetchReference(ctx, repo, parsed.Reference)
	if err != nil {
		return nil, ociError(ref, err)
	}
	if desc.MediaType != ocispec.MediaTypeImageManifest {
		return nil, fmt.Errorf("%s is %s; expected an OCI image manifest", ref, desc.MediaType)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse plugin catalog manifest %s: %w", ref, err)
	}
	if m.ArtifactType != CatalogArtifactType {
		return nil, fmt.Errorf("%s has artifactType %q; expected %s", ref, m.ArtifactType, CatalogArtifactType)
	}
	var layer *ocispec.Descriptor
	for i := range m.Layers {
		if m.Layers[i].MediaType == CatalogMediaType {
			layer = &m.Layers[i]
		}
	}
	if layer == nil {
		return nil, fmt.Errorf("%s has no %s layer", ref, CatalogMediaType)
	}
	if layer.Size > maxCatalogBytes {
		return nil, fmt.Errorf("%s: plugin catalog is %d bytes; the limit is %d", ref, layer.Size, maxCatalogBytes)
	}
	cache := catalogCacheKey(desc, s)
	if !allowUnsigned {
		if cached, ok := readCatalogCache(cache, *layer); ok {
			return cached, nil
		}
	}
	pinned := repoRef(repo, desc.Digest.String())
	if !allowUnsigned {
		sig := signer{Issuer: s.Issuer, SANRegex: "^" + regexp.QuoteMeta(s.Identity) + "$"}
		if err := verifySignature(ctx, repo, pinned, desc, sig); err != nil {
			return nil, fmt.Errorf("plugin catalog: %w", err)
		}
	}
	body, err := content.FetchAll(ctx, repo.Blobs(), *layer)
	if err != nil {
		return nil, fmt.Errorf("fetch plugin catalog %s: %w", pinned, err)
	}
	if !allowUnsigned {
		writeCatalogCache(cache, body)
	}
	return body, nil
}

// catalogCacheKey names a verified catalog: the manifest digest and the
// signer that verified it. A different signer MUST verify again.
func catalogCacheKey(desc ocispec.Descriptor, s Signer) string {
	return digest.FromString(desc.Digest.String() + "\n" + s.Issuer + "\n" + s.Identity).Encoded()
}

func catalogCachePath(key string) (string, bool) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, "lfed", "catalog", key+".json"), true
}

// readCatalogCache returns the cached catalog when its content matches
// the layer digest that the verified manifest names.
func readCatalogCache(key string, layer ocispec.Descriptor) ([]byte, bool) {
	p, ok := catalogCachePath(key)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil || int64(len(data)) != layer.Size || digest.FromBytes(data) != layer.Digest {
		return nil, false
	}
	return data, true
}

// writeCatalogCache is best effort. A failed write costs one more
// verification on the next run.
func writeCatalogCache(key string, data []byte) {
	p, ok := catalogCachePath(key)
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}
