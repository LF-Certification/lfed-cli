package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/config"
)

func catalogJSON(t *testing.T, entries ...CatalogEntry) []byte {
	t.Helper()
	data, err := json.Marshal(Catalog{SchemaVersion: CatalogSchemaVersion, Plugins: entries})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseCatalogRejectsInvalidEntries(t *testing.T) {
	good := testEntry("ghcr.io", "hello")
	cases := []struct {
		name string
		edit func(e *CatalogEntry)
		want string
	}{
		{"ref with tag", func(e *CatalogEntry) { e.Ref += ":1.0.0" }, "MUST NOT carry a tag"},
		{"ref not ending in name", func(e *CatalogEntry) { e.Ref = "ghcr.io/acme/other" }, "MUST end in /hello"},
		{"identity without version", func(e *CatalogEntry) {
			e.Signer.Identity = "https://github.com/acme/x/.github/workflows/r.yml@refs/tags/v1"
		}, "{version}"},
		{"issuer not https", func(e *CatalogEntry) { e.Signer.Issuer = "http://issuer" }, "signer.issuer"},
		{"bad name", func(e *CatalogEntry) { e.Name = "Hello"; e.Ref = "ghcr.io/acme/Hello" }, "does not match"},
		{"empty summary", func(e *CatalogEntry) { e.Summary = "" }, "summary is empty"},
		{"bad owner", func(e *CatalogEntry) { e.Owner = "acme/team/sub" }, "owner"},
	}
	for _, c := range cases {
		e := good
		c.edit(&e)
		_, err := parseCatalog(catalogJSON(t, e), "test")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}

	if _, err := parseCatalog(catalogJSON(t, good, good), "test"); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Errorf("duplicate: err = %v", err)
	}
	if _, err := parseCatalog([]byte(`{"schema_version":2,"plugins":[]}`), "test"); err == nil || !strings.Contains(err.Error(), "schema_version 2") {
		t.Errorf("schema version: err = %v", err)
	}
	if _, err := parseCatalog([]byte(`{"schema_version":1,"plugins":[],"extra":1}`), "test"); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown field: err = %v", err)
	}

	c, err := parseCatalog(catalogJSON(t, testEntry("ghcr.io", "zeta"), good), "test")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plugins[0].Name != "hello" || c.Plugins[1].Name != "zeta" {
		t.Errorf("plugins not sorted: %+v", c.Plugins)
	}
	if _, ok := c.Lookup("zeta"); !ok {
		t.Error("Lookup(zeta) = false")
	}
	if _, ok := c.Lookup("nope"); ok {
		t.Error("Lookup(nope) = true")
	}
}

func TestSignerExpand(t *testing.T) {
	s := Signer{Identity: "https://github.com/acme/{name}/.github/workflows/r.yml@refs/tags/{name}/v{version}"}
	got := s.expand("hello", "1.2.3")
	want := "https://github.com/acme/hello/.github/workflows/r.yml@refs/tags/hello/v1.2.3"
	if got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}
}

// publishCatalog pushes a catalog artifact to the test registry and
// returns its reference with tag.
func publishCatalog(t *testing.T, host string, data []byte, artifactType, mediaType string) string {
	t.Helper()
	ctx := context.Background()
	repo, err := remote.NewRepository(host + "/lfed-cli-plugins/catalog")
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	layer, err := oras.PushBytes(ctx, repo, mediaType, data)
	if err != nil {
		t.Fatal(err)
	}
	desc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, artifactType, oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{layer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(ctx, desc, "latest"); err != nil {
		t.Fatal(err)
	}
	return host + "/lfed-cli-plugins/catalog:latest"
}

func TestInstallFromOCICatalog(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	ctx := context.Background()
	f, _ := testFactory(t)
	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, tarGz(t, helloBin, "lfed-hello"))

	ref := publishCatalog(t, host, catalogJSON(t, testEntry(host, "hello")), CatalogArtifactType, CatalogMediaType)

	// --catalog wins over LFED_CATALOG, which testRegistry set to a file.
	p, err := Install(ctx, f, "hello", Options{AllowUnsigned: true, Catalog: ref})
	if err != nil {
		t.Fatalf("Install via OCI catalog: %v", err)
	}
	if p.Source.Type != SourceCatalog || p.Source.Ref != host+"/lfed-cli-plugins/hello" {
		t.Errorf("Source = %+v", p.Source)
	}
	// An unsigned catalog is not cached: the cache stands in for a
	// successful signature check.
	if entries, _ := os.ReadDir(filepath.Join(cache, "lfed", "catalog")); len(entries) != 0 {
		t.Errorf("unsigned catalog was cached: %v", entries)
	}

	t.Setenv("LFED_CATALOG", ref)
	if _, err := Install(ctx, f, "hello", Options{AllowUnsigned: true}); err != nil {
		t.Fatalf("Install via LFED_CATALOG: %v", err)
	}

	// Without --allow-unsigned the catalog MUST carry a signature.
	_, err = Install(ctx, f, "hello", Options{})
	if err == nil || !strings.Contains(err.Error(), "catalog:") || !strings.Contains(err.Error(), "no Sigstore signature") {
		t.Errorf("unsigned catalog accepted: err = %v", err)
	}

	wrongType := publishCatalog(t, host, catalogJSON(t, testEntry(host, "hello")), "application/vnd.example.other", CatalogMediaType)
	_, err = Install(ctx, f, "hello", Options{AllowUnsigned: true, Catalog: wrongType})
	if err == nil || !strings.Contains(err.Error(), "artifactType") {
		t.Errorf("wrong artifactType: err = %v", err)
	}
	wrongLayer := publishCatalog(t, host, catalogJSON(t, testEntry(host, "hello")), CatalogArtifactType, "application/json")
	_, err = Install(ctx, f, "hello", Options{AllowUnsigned: true, Catalog: wrongLayer})
	if err == nil || !strings.Contains(err.Error(), "has no "+CatalogMediaType) {
		t.Errorf("wrong layer mediaType: err = %v", err)
	}
	_, err = Install(ctx, f, "hello", Options{AllowUnsigned: true, Catalog: host + "/lfed-cli-plugins/catalog"})
	if err == nil || !strings.Contains(err.Error(), "needs a tag or digest") {
		t.Errorf("catalog without tag: err = %v", err)
	}
}

func TestCatalogCacheSkipsRefetch(t *testing.T) {
	host := testRegistry(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	data := catalogJSON(t, testEntry(host, "hello"))
	ref := publishCatalog(t, host, data, CatalogArtifactType, CatalogMediaType)
	ctx := context.Background()

	repo, err := remote.NewRepository(host + "/lfed-cli-plugins/catalog")
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	desc, _, err := fetchReference(ctx, repo, "latest")
	if err != nil {
		t.Fatal(err)
	}
	s := Signer{Issuer: "https://issuer", Identity: "x"}

	// A cache entry for this manifest and signer means a previous run
	// verified it. The fetch returns it without a signature check.
	writeCatalogCache(catalogCacheKey(desc, s), data)
	got, err := fetchCatalog(ctx, ref, s, false)
	if err != nil {
		t.Fatalf("cached catalog: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("cached catalog = %s", got)
	}

	// Another signer has its own key: the unsigned artifact fails.
	if _, err := fetchCatalog(ctx, ref, Signer{Issuer: "https://issuer", Identity: "y"}, false); err == nil {
		t.Error("cache served a catalog to a signer that did not verify it")
	}

	// A cache file whose content does not match the layer digest is ignored.
	writeCatalogCache(catalogCacheKey(desc, s), []byte(`{"schema_version":1,"plugins":[]}`))
	if _, err := fetchCatalog(ctx, ref, s, false); err == nil {
		t.Error("tampered cache accepted")
	}
}

func TestCatalogSourceConfigSignerScope(t *testing.T) {
	t.Setenv("LFED_CATALOG", "")
	mirror := "registry.example/mirror/catalog:latest"
	custom := config.CatalogSigner{Issuer: "https://issuer.example", Identity: "https://example/w.yml@refs/heads/main"}
	withConfig := func(c config.Catalog) *cmdutil.Factory {
		f, _ := testFactory(t)
		f.Config = func() (*config.Config, error) {
			return &config.Config{Catalogs: map[string]config.Catalog{DefaultCatalogAlias: c}}, nil
		}
		return f
	}
	def := Signer{Issuer: DefaultCatalogIssuer, Identity: DefaultCatalogIdentity}
	cases := []struct {
		name    string
		cfg     config.Catalog
		opts    Options
		wantRef string
		want    Signer
	}{
		{"default", config.Catalog{}, Options{}, DefaultCatalog, def},
		{"config catalog and signer", config.Catalog{Ref: mirror, Signer: custom}, Options{}, mirror, Signer{Issuer: custom.Issuer, Identity: custom.Identity}},
		{"flag overrides config catalog: default signer", config.Catalog{Ref: mirror, Signer: custom}, Options{Catalog: DefaultCatalog}, DefaultCatalog, def},
		{"flag equals config catalog: config signer", config.Catalog{Ref: mirror, Signer: custom}, Options{Catalog: mirror}, mirror, Signer{Issuer: custom.Issuer, Identity: custom.Identity}},
		{"signer without catalog applies everywhere", config.Catalog{Signer: custom}, Options{Catalog: DefaultCatalog}, DefaultCatalog, Signer{Issuer: custom.Issuer, Identity: custom.Identity}},
	}
	for _, c := range cases {
		for _, alias := range []string{"", DefaultCatalogAlias} {
			ref, s, err := catalogSource(withConfig(c.cfg), c.opts, alias)
			if err != nil || ref != c.wantRef || s != c.want {
				t.Errorf("%s (alias %q): ref=%q signer=%+v err=%v; want %q %+v", c.name, alias, ref, s, err, c.wantRef, c.want)
			}
		}
	}
	if _, _, err := catalogSource(withConfig(config.Catalog{Signer: config.CatalogSigner{Issuer: "https://x"}}), Options{}, ""); err == nil {
		t.Error("half-set config signer accepted")
	}
}

func TestCatalogSourceAlias(t *testing.T) {
	t.Setenv("LFED_CATALOG", "")
	custom := config.CatalogSigner{Issuer: "https://issuer.example", Identity: "https://example/w.yml@refs/heads/main"}
	f, _ := testFactory(t)
	f.Config = func() (*config.Config, error) {
		return &config.Config{Catalogs: map[string]config.Catalog{
			"acme":     {Ref: "registry.example/acme/catalog:latest", Signer: custom},
			"unsigned": {Ref: "registry.example/x/catalog:latest"},
			"half":     {Ref: "registry.example/x/catalog:latest", Signer: config.CatalogSigner{Issuer: "https://x"}},
			"team":     {Ref: "/srv/team/catalog.json"},
		}}, nil
	}
	// The flag and the variable override the default catalog only.
	t.Setenv("LFED_CATALOG", "/elsewhere.json")
	ref, s, err := catalogSource(f, Options{Catalog: "/flag.json"}, "acme")
	if err != nil || ref != "registry.example/acme/catalog:latest" || s != (Signer{Issuer: custom.Issuer, Identity: custom.Identity}) {
		t.Errorf("acme: ref=%q signer=%+v err=%v", ref, s, err)
	}
	if ref, _, err := catalogSource(f, Options{}, "team"); err != nil || ref != "/srv/team/catalog.json" {
		t.Errorf("file catalog without signer: ref=%q err=%v", ref, err)
	}
	if _, _, err := catalogSource(f, Options{}, "unsigned"); err == nil || !strings.Contains(err.Error(), "--allow-unsigned") {
		t.Errorf("OCI catalog without signer: err = %v", err)
	}
	if _, s, err := catalogSource(f, Options{AllowUnsigned: true}, "unsigned"); err != nil || s != (Signer{}) {
		t.Errorf("OCI catalog without signer, --allow-unsigned: signer=%+v err=%v", s, err)
	}
	if _, _, err := catalogSource(f, Options{}, "half"); err == nil || !strings.Contains(err.Error(), "catalogs.half.signer") {
		t.Errorf("half-set alias signer: err = %v", err)
	}
	if _, _, err := catalogSource(f, Options{}, "missing"); err == nil || !strings.Contains(err.Error(), `no plugin catalog "missing"`) {
		t.Errorf("unknown alias: err = %v", err)
	}
}

func TestInstallQualifiedName(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	ctx := context.Background()
	f, _ := testFactory(t)
	unsigned := Options{AllowUnsigned: true}
	archive := tarGz(t, helloBin, "lfed-hello")

	// The private catalog lists hello at its own repository.
	private := helloRelease("hello", "0.0.5", ">=0.1.0 <1", archive)
	private.repo = "acme/hello"
	publishRelease(t, host, private)
	entry := testEntry(host, "hello")
	entry.Ref = host + "/lfed-cli-plugins/acme/hello"
	path := filepath.Join(t.TempDir(), "acme.json")
	if err := os.WriteFile(path, catalogJSON(t, entry), 0o600); err != nil {
		t.Fatal(err)
	}
	f.Config = func() (*config.Config, error) {
		return &config.Config{Catalogs: map[string]config.Catalog{"acme": {Ref: path}}}, nil
	}

	p, err := Install(ctx, f, "acme/hello", unsigned)
	if err != nil {
		t.Fatalf("Install acme/hello: %v", err)
	}
	if p.Source.Type != SourceCatalog || p.Source.Catalog != "acme" || p.Source.Ref != entry.Ref || p.Manifest.Version != "0.0.5" {
		t.Errorf("Source = %+v version %s", p.Source, p.Manifest.Version)
	}

	// The default catalog cannot replace a plugin that another catalog
	// installed; the same catalog can.
	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, archive)
	_, err = Install(ctx, f, "hello", unsigned)
	if err == nil || !strings.Contains(err.Error(), "installed from plugin catalog acme") {
		t.Errorf("cross-catalog replace: err = %v", err)
	}
	if _, err := Install(ctx, f, "acme/hello", unsigned); err != nil {
		t.Errorf("same-catalog reinstall: %v", err)
	}

	// Upgrade resolves through the recorded catalog.
	private.version = "0.0.6"
	private.manifest.Version = "0.0.6"
	publishRelease(t, host, private)
	results, err := Upgrade(ctx, f, []string{"hello"}, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; !r.Upgraded || r.To != "0.0.6" {
		t.Errorf("upgrade = %+v", r)
	}
	if got, _ := Lookup("hello"); got.Source.Catalog != "acme" || got.Source.Ref != entry.Ref {
		t.Errorf("recorded Source = %+v", got.Source)
	}

	// Without the catalog in config, upgrade and install fail by name.
	f.Config = func() (*config.Config, error) { return &config.Config{}, nil }
	if _, err := Upgrade(ctx, f, []string{"hello"}, false, unsigned); err == nil || !strings.Contains(err.Error(), `no plugin catalog "acme"`) {
		t.Errorf("upgrade without catalog: err = %v", err)
	}
	if _, err := Install(ctx, f, "acme/other", unsigned); err == nil || !strings.Contains(err.Error(), `no plugin catalog "acme"`) {
		t.Errorf("install without catalog: err = %v", err)
	}
}

func TestUpgradeFollowsCatalogMove(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	ctx := context.Background()
	f, _ := testFactory(t)
	unsigned := Options{AllowUnsigned: true}
	archive := tarGz(t, helloBin, "lfed-hello")

	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, archive)
	if _, err := Install(ctx, f, "hello", unsigned); err != nil {
		t.Fatal(err)
	}

	// The catalog now points hello at another repository with a newer
	// release. Upgrade MUST follow the catalog, not the recorded ref.
	moved := helloRelease("hello", "0.0.2", ">=0.1.0 <1", archive)
	moved.repo = "moved/hello"
	publishRelease(t, host, moved)
	entry := testEntry(host, "hello")
	entry.Ref = host + "/lfed-cli-plugins/moved/hello"
	p := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(p, catalogJSON(t, entry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LFED_CATALOG", p)

	results, err := Upgrade(ctx, f, []string{"hello"}, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; !r.Upgraded || r.To != "0.0.2" {
		t.Errorf("upgrade = %+v", r)
	}
	got, err := Lookup("hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.Ref != entry.Ref || got.Source.Type != SourceCatalog {
		t.Errorf("recorded Source = %+v", got.Source)
	}

	// A plugin that leaves the catalog cannot upgrade.
	t.Setenv("LFED_CATALOG", writeTestCatalog(t, host, "other"))
	_, err = Upgrade(ctx, f, []string{"hello"}, false, unsigned)
	var notFound *CatalogNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("delisted plugin: err = %v, want *CatalogNotFoundError", err)
	}
}

func TestUpgradeAdoptsLegacyOCIInstall(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	ctx := context.Background()
	f, _ := testFactory(t)
	unsigned := Options{AllowUnsigned: true}
	archive := tarGz(t, helloBin, "lfed-hello")
	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, archive)

	// A host before the catalog recorded a bare name as this OCI source.
	repo := host + "/lfed-cli-plugins/hello"
	p, err := Install(ctx, f, repo, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source.Type != SourceOCI || p.Source.Ref != repo {
		t.Fatalf("Source = %+v", p.Source)
	}

	publish(t, host, "hello", "0.0.2", ">=0.1.0 <1", []platform{hostPlatform}, archive)
	results, err := Upgrade(ctx, f, []string{"hello"}, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; !r.Upgraded || r.To != "0.0.2" {
		t.Errorf("upgrade = %+v", r)
	}
	got, err := Lookup("hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.Type != SourceCatalog || got.Source.Ref != repo {
		t.Errorf("recorded Source = %+v, want catalog at %s", got.Source, repo)
	}

	// An OCI install at a repository the catalog does not name stays OCI.
	other := helloRelease("hello", "0.0.3", ">=0.1.0 <1", archive)
	other.repo = "fork/hello"
	publishRelease(t, host, other)
	if _, err := Install(ctx, f, host+"/lfed-cli-plugins/fork/hello", unsigned); err != nil {
		t.Fatal(err)
	}
	other = helloRelease("hello", "0.0.4", ">=0.1.0 <1", archive)
	other.repo = "fork/hello"
	publishRelease(t, host, other)
	if _, err := Upgrade(ctx, f, []string{"hello"}, false, unsigned); err != nil {
		t.Fatal(err)
	}
	if got, _ := Lookup("hello"); got.Source.Type != SourceOCI || got.Manifest.Version != "0.0.4" {
		t.Errorf("fork upgrade: %+v %s", got.Source, got.Manifest.Version)
	}
}
