package pluginmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"

	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

func testRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("LFED_CATALOG", writeTestCatalog(t, host, "hello", "nothing", "other", "empty", "alias"))
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	return host
}

func testEntry(host, name string) CatalogEntry {
	return CatalogEntry{
		Name:    name,
		Summary: "Test plugin " + name,
		Owner:   "acme/tools",
		Ref:     host + "/lfed-cli-plugins/" + name,
		Signer: Signer{
			Issuer:   "https://token.actions.githubusercontent.com",
			Identity: "https://github.com/acme/" + name + "/.github/workflows/release.yml@refs/tags/v{version}",
		},
	}
}

// writeTestCatalog writes a file catalog that maps each name to the test
// registry. It returns the path.
func writeTestCatalog(t *testing.T, host string, names ...string) string {
	t.Helper()
	c := Catalog{SchemaVersion: CatalogSchemaVersion}
	for _, n := range names {
		c.Plugins = append(c.Plugins, testEntry(host, n))
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func tarGz(t *testing.T, bin, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"LICENSE", []byte("MIT")}, {name, data}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type platform struct{ os, arch string }

var hostPlatform = platform{runtime.GOOS, runtime.GOARCH}

type release struct {
	name, version, requires string

	repo string

	manifest sdkplugin.Manifest

	artifactType, configMediaType string

	platforms []platform
	archive   []byte
}

func helloRelease(name, version, requires string, archive []byte) release {
	return release{
		name: name, version: version, requires: requires,
		manifest: sdkplugin.Manifest{
			SchemaVersion: sdkplugin.ManifestSchemaVersion,
			Name:          name,
			Version:       version,
			Summary:       "Test plugin for pluginmgr",
			RequiresLfed:  requires,
		},
		artifactType:    ArtifactType,
		configMediaType: ConfigMediaType,
		platforms:       []platform{hostPlatform},
		archive:         archive,
	}
}

func publish(t *testing.T, host, name, version, requires string, platforms []platform, archive []byte) {
	t.Helper()
	r := helloRelease(name, version, requires, archive)
	r.platforms = platforms
	publishRelease(t, host, r)
}

func publishRelease(t *testing.T, host string, r release) {
	t.Helper()
	ctx := context.Background()
	name := r.repo
	if name == "" {
		name = r.name
	}
	repo, err := remote.NewRepository(host + "/lfed-cli-plugins/" + name)
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	layer, err := oras.PushBytes(ctx, repo, LayerMediaType, r.archive)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	config, err := oras.PushBytes(ctx, repo, r.configMediaType, manifest)
	if err != nil {
		t.Fatal(err)
	}
	idx := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Annotations: map[string]string{
			ocispec.AnnotationVersion: r.version,
			AnnotationName:            r.name,
		},
	}
	if r.requires != "" {
		idx.Annotations[AnnotationRequiresLfed] = r.requires
	}
	for _, p := range r.platforms {
		desc, err := oras.PackManifest(ctx, repo, oras.PackManifestVersion1_1, r.artifactType, oras.PackManifestOptions{
			ConfigDescriptor:    &config,
			Layers:              []ocispec.Descriptor{layer},
			ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: "2026-01-01T00:00:00Z"},
		})
		if err != nil {
			t.Fatal(err)
		}
		desc.Platform = &ocispec.Platform{OS: p.os, Architecture: p.arch}
		idx.Manifests = append(idx.Manifests, desc)
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromBytes(data), Size: int64(len(data))}
	if err := repo.Manifests().PushReference(ctx, desc, bytes.NewReader(data), r.version); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFromOCIPicksHighestCompatible(t *testing.T) {
	dir := setPluginDir(t)
	host := testRegistry(t)
	archive := tarGz(t, helloBin, "lfed-hello")

	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform, {"plan9", "mips"}}, archive)
	publish(t, host, "hello", "0.0.2", ">=9", []platform{hostPlatform}, archive)
	publish(t, host, "hello", "latest", "", []platform{hostPlatform}, archive)
	publish(t, host, "hello", "0.0.5-rc1", "", []platform{hostPlatform}, archive)
	publish(t, host, "hello", "0.0.5_linux_amd64", "", []platform{hostPlatform}, archive)

	f, errOut := testFactory(t)
	p, err := Install(context.Background(), f, "hello", Options{AllowUnsigned: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if p.Manifest.Version != "0.0.1" || p.Manifest.Summary == "" || p.Source.Version != "0.0.1" || p.Source.Type != SourceCatalog || p.Source.Pinned {
		t.Errorf("installed %+v", p)
	}
	if p.Source.Ref != host+"/lfed-cli-plugins/hello" {
		t.Errorf("Ref = %q", p.Source.Ref)
	}
	if !digestRe.MatchString(p.Source.Digest) {
		t.Errorf("Digest = %q, want sha256:<hex>", p.Source.Digest)
	}
	if want := "warning: " + p.Source.String() + " installed without signature verification\n"; errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
	info, err := os.Stat(filepath.Join(dir, "hello", "lfed-hello"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("binary mode %v is not executable", info.Mode())
	}
	got, err := Lookup("hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != p.Source || got.Manifest != p.Manifest {
		t.Errorf("Lookup = %+v, want %+v", got, p)
	}
}

func TestInstallFromOCIPinned(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	archive := tarGz(t, helloBin, "lfed-hello")
	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, archive)
	publish(t, host, "hello", "0.0.2", ">=9", []platform{hostPlatform}, archive)
	f, _ := testFactory(t)
	ctx := context.Background()
	unsigned := Options{AllowUnsigned: true}

	p, err := Install(ctx, f, "hello@0.0.1", unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source.Version != "0.0.1" || !p.Source.Pinned || !digestRe.MatchString(p.Source.Digest) {
		t.Errorf("Source = %+v", p.Source)
	}
	results, err := Upgrade(ctx, f, nil, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Upgraded || r.Note != "pinned to 0.0.1; reinstall to change" {
		t.Errorf("upgrade of a version pin = %+v", r)
	}

	d := p.Source.Digest
	p, err = Install(ctx, f, "hello@"+d, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	want := Source{Type: SourceCatalog, Ref: host + "/lfed-cli-plugins/hello", Digest: d, Pinned: true}
	if p.Source != want || p.Manifest.Version != "0.0.1" {
		t.Errorf("digest pin: Source = %+v, version %q; want %+v", p.Source, p.Manifest.Version, want)
	}
	if got, _ := Lookup("hello"); got.Source != want {
		t.Errorf("recorded Source = %+v, want %+v", got.Source, want)
	}
	results, err = Upgrade(ctx, f, []string{"hello"}, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Upgraded || r.Note != "pinned to "+d+"; reinstall to change" {
		t.Errorf("upgrade of a digest pin = %+v", r)
	}

	_, err = Install(ctx, f, "hello@0.0.2", unsigned)
	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) {
		t.Fatalf("pinned incompatible: err = %v, want *IncompatibleError", err)
	}

	_, err = Install(ctx, f, "hello@0.0.9", unsigned)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing tag: err = %v", err)
	}

	_, err = Install(ctx, f, "hello@sha256:"+strings.Repeat("1", 64), unsigned)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing digest: err = %v", err)
	}
}

func TestInstallFromOCIRequiresSignature(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	publish(t, host, "hello", "0.0.1", "", []platform{hostPlatform}, tarGz(t, helloBin, "lfed-hello"))
	f, _ := testFactory(t)

	_, err := Install(context.Background(), f, "hello", Options{})
	if err == nil || !strings.Contains(err.Error(), "--allow-unsigned") {
		t.Fatalf("err = %v, want a signer policy error", err)
	}
	if _, err := Lookup("hello"); err == nil {
		t.Error("unsigned plugin installed")
	}
}

func TestInstallFromOCIBounds(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	archive := tarGz(t, helloBin, "lfed-hello")
	f, _ := testFactory(t)
	ctx := context.Background()
	unsigned := Options{AllowUnsigned: true}
	publish(t, host, "hello", "0.0.1", "", []platform{hostPlatform}, archive)

	setMaxLayerBytes(t, int64(len(archive))-1)
	_, err := Install(ctx, f, "hello", unsigned)
	if err == nil || !strings.Contains(err.Error(), "the limit is") {
		t.Errorf("oversize layer descriptor: err = %v", err)
	}

	setMaxLayerBytes(t, int64(len(archive)))
	_, err = Install(ctx, f, "hello", unsigned)
	if err == nil || !strings.Contains(err.Error(), "expands to more than") {
		t.Errorf("oversize expansion: err = %v", err)
	}
	if _, err := Lookup("hello"); err == nil {
		t.Error("plugin installed despite a limit")
	}
}

func TestInstallFromOCIRejectsBadArtifact(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	archive := tarGz(t, helloBin, "lfed-hello")
	f, _ := testFactory(t)
	ctx := context.Background()
	unsigned := Options{AllowUnsigned: true}

	other := helloRelease("hello", "0.0.1", ">=0.1.0 <1", archive)
	other.artifactType = "application/vnd.example.other"
	publishRelease(t, host, other)
	_, err := Install(ctx, f, "hello", unsigned)
	if err == nil || !strings.Contains(err.Error(), "artifactType") {
		t.Errorf("wrong artifactType: err = %v", err)
	}

	cfg := helloRelease("hello", "0.0.2", ">=0.1.0 <1", archive)
	cfg.configMediaType = "application/vnd.oci.image.config.v1+json"
	publishRelease(t, host, cfg)
	_, err = Install(ctx, f, "hello@0.0.2", unsigned)
	if err == nil || !strings.Contains(err.Error(), "config has mediaType") {
		t.Errorf("wrong config mediaType: err = %v", err)
	}

	name := helloRelease("hello", "0.0.3", ">=0.1.0 <1", archive)
	name.manifest.Name = "other"
	publishRelease(t, host, name)
	_, err = Install(ctx, f, "hello@0.0.3", unsigned)
	if err == nil || !strings.Contains(err.Error(), AnnotationName) {
		t.Errorf("name annotation mismatch: err = %v", err)
	}

	version := helloRelease("hello", "0.0.4", ">=0.1.0 <1", archive)
	version.manifest.Version = "0.0.5"
	publishRelease(t, host, version)
	_, err = Install(ctx, f, "hello@0.0.4", unsigned)
	if err == nil || !strings.Contains(err.Error(), ocispec.AnnotationVersion) {
		t.Errorf("version annotation mismatch: err = %v", err)
	}

	requires := helloRelease("hello", "0.0.6", ">=0.1.0 <1", archive)
	requires.manifest.RequiresLfed = ">=0.1.0"
	publishRelease(t, host, requires)
	_, err = Install(ctx, f, "hello@0.0.6", unsigned)
	if err == nil || !strings.Contains(err.Error(), AnnotationRequiresLfed) {
		t.Errorf("requires annotation mismatch: err = %v", err)
	}

	invalid := helloRelease("hello", "0.0.7", ">=0.1.0 <1", archive)
	invalid.manifest.Summary = ""
	publishRelease(t, host, invalid)
	_, err = Install(ctx, f, "hello@0.0.7", unsigned)
	if err == nil || !strings.Contains(err.Error(), "summary is empty") {
		t.Errorf("invalid manifest: err = %v", err)
	}

	if _, err := Lookup("hello"); err == nil {
		t.Error("a rejected release was installed")
	}
}

func TestInstallFromOCIErrors(t *testing.T) {
	setPluginDir(t)
	host := testRegistry(t)
	f, _ := testFactory(t)
	ctx := context.Background()
	unsigned := Options{AllowUnsigned: true}

	publish(t, host, "hello", "0.0.2", ">=9", []platform{hostPlatform}, tarGz(t, helloBin, "lfed-hello"))
	_, err := Install(ctx, f, "hello", unsigned)
	var noRelease *NoReleaseError
	if !errors.As(err, &noRelease) {
		t.Fatalf("err = %v, want *NoReleaseError", err)
	}
	if noRelease.Newest != "0.0.2" || noRelease.Requires != ">=9" {
		t.Errorf("NoReleaseError = %+v", noRelease)
	}

	_, err = Install(ctx, f, "nothing", unsigned)
	if err == nil || !strings.Contains(err.Error(), "lfed-cli-plugins/nothing not found") {
		t.Fatalf("unknown repo: err = %v", err)
	}

	_, err = Install(ctx, f, "unlisted", unsigned)
	var notInCatalog *CatalogNotFoundError
	if !errors.As(err, &notInCatalog) || notInCatalog.Name != "unlisted" {
		t.Fatalf("unlisted name: err = %v, want *CatalogNotFoundError", err)
	}

	publish(t, host, "other", "0.0.1", "", []platform{{"plan9", "mips"}}, tarGz(t, helloBin, "lfed-other"))
	_, err = Install(ctx, f, "other", unsigned)
	if err == nil || !strings.Contains(err.Error(), "no build for "+runtime.GOOS) {
		t.Fatalf("platform: err = %v", err)
	}

	publish(t, host, "empty", "0.0.1", "", []platform{hostPlatform}, tarGz(t, helloBin, "README"))
	_, err = Install(ctx, f, "empty", unsigned)
	if err == nil || !strings.Contains(err.Error(), "no file named lfed-empty") {
		t.Fatalf("missing binary: err = %v", err)
	}

	alias := helloRelease("hello", "0.0.1", ">=0.1.0 <1", tarGz(t, helloBin, "lfed-alias"))
	alias.repo = "alias"
	publishRelease(t, host, alias)
	_, err = Install(ctx, f, "alias", unsigned)
	if err == nil || !strings.Contains(err.Error(), `plugin "hello", not "alias"`) {
		t.Fatalf("name mismatch: err = %v", err)
	}
}

func TestInstallFromOCIRejectsReservedName(t *testing.T) {
	setPluginDir(t)
	testRegistry(t)
	f, _ := testFactory(t)
	_, err := Install(context.Background(), f, "auth", Options{Reserved: []string{"auth"}, AllowUnsigned: true})
	if err == nil || !strings.Contains(err.Error(), `"auth" is a core lfed command`) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpgrade(t *testing.T) {
	dir := setPluginDir(t)
	host := testRegistry(t)
	archive := tarGz(t, helloBin, "lfed-hello")
	f, _ := testFactory(t)
	ctx := context.Background()
	unsigned := Options{AllowUnsigned: true}

	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, archive)
	publish(t, host, "hello", "0.0.2", ">=9", []platform{hostPlatform}, archive)
	if _, err := Install(ctx, f, "hello", unsigned); err != nil {
		t.Fatal(err)
	}

	writeFake(t, dir, "local", "Local")
	rewriteSource(t, dir, "local", Source{Type: SourcePath, Path: "/src/local"})

	writeFake(t, dir, "nosource", "No source")

	results, err := Upgrade(ctx, f, nil, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]UpgradeResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	if r := byName["hello"]; r.Upgraded || r.To != "0.0.1" || r.Note != "current" {
		t.Errorf("hello = %+v", r)
	}
	if r := byName["local"]; r.Upgraded || !strings.Contains(r.Note, `lfed plugin install /src/local`) {
		t.Errorf("local = %+v", r)
	}
	if r := byName["nosource"]; r.Upgraded || !strings.Contains(r.Note, "source unknown") {
		t.Errorf("nosource = %+v", r)
	}

	rewriteSource(t, dir, "hello", Source{Type: SourceOCI, Ref: host + "/lfed-cli-plugins/hello", Version: "0.0.0"})
	results, err = Upgrade(ctx, f, []string{"hello"}, true, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Upgraded || r.To != "0.0.1" || r.Note != "dry run" {
		t.Errorf("dry run = %+v", r)
	}
	if p, _ := Lookup("hello"); p.Source.Version != "0.0.0" {
		t.Errorf("dry run changed the install: %+v", p.Source)
	}
	results, err = Upgrade(ctx, f, []string{"hello"}, false, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; !r.Upgraded || r.To != "0.0.1" {
		t.Errorf("upgrade = %+v", r)
	}
	if p, _ := Lookup("hello"); p.Source.Version != "0.0.1" {
		t.Errorf("after upgrade: %+v", p.Source)
	}
}

func TestUpgradeContinuesAfterFailure(t *testing.T) {
	dir := setPluginDir(t)
	host := testRegistry(t)
	f, _ := testFactory(t)
	ctx := context.Background()
	unsigned := Options{AllowUnsigned: true}

	publish(t, host, "hello", "0.0.1", ">=0.1.0 <1", []platform{hostPlatform}, tarGz(t, helloBin, "lfed-hello"))
	if _, err := Install(ctx, f, "hello", unsigned); err != nil {
		t.Fatal(err)
	}
	writeFake(t, dir, "gone", "Repository vanished")
	rewriteSource(t, dir, "gone", Source{Type: SourceOCI, Ref: host + "/lfed-cli-plugins/gone", Version: "1.2.3"})

	results, err := Upgrade(ctx, f, nil, false, unsigned)
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v, want the failure of gone", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want both plugins", results)
	}
	byName := map[string]UpgradeResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	if r := byName["gone"]; !strings.Contains(r.Error, "not found") || r.Upgraded {
		t.Errorf("gone = %+v", r)
	}
	if r := byName["hello"]; r.Error != "" || r.Note != "current" {
		t.Errorf("hello = %+v", r)
	}

	results, err = Upgrade(ctx, f, []string{"nosuch", "hello"}, false, unsigned)
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("unknown name: err = %v", err)
	}
	if len(results) != 2 || results[0].Error == "" || results[1].Name != "hello" || results[1].Error != "" {
		t.Errorf("results = %+v", results)
	}
}

func rewriteSource(t *testing.T, dir, name string, src Source) {
	t.Helper()
	p, err := load(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record{Manifest: p.Manifest, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
