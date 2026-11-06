package pluginmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestSignerFor(t *testing.T) {
	entry := &CatalogEntry{
		Name: "cluster",
		Signer: Signer{
			Issuer:   "https://token.actions.githubusercontent.com",
			Identity: "https://github.com/LF-Certification/p3-platform/.github/workflows/lfed-cli-plugins-release.yml@refs/tags/lfed-cli-{name}/v{version}",
		},
	}
	src := Source{Type: SourceCatalog, Ref: "ghcr.io/lf-certification/lfed-cli-plugins/cluster"}
	s, err := signerFor(src, "1.2.0", Options{}, entry)
	if err != nil {
		t.Fatal(err)
	}
	want := `^https://github\.com/LF-Certification/p3-platform/\.github/workflows/lfed-cli-plugins-release\.yml@refs/tags/lfed-cli-cluster/v1\.2\.0$`
	if s.SANRegex != want || s.Issuer != "https://token.actions.githubusercontent.com" {
		t.Errorf("signer = %+v, want SAN %s", s, want)
	}
	re := regexp.MustCompile(s.SANRegex)
	for san, ok := range map[string]bool{
		"https://github.com/LF-Certification/p3-platform/.github/workflows/lfed-cli-plugins-release.yml@refs/tags/lfed-cli-cluster/v1.2.0":  true,
		"https://github.com/LF-Certification/p3-platform/.github/workflows/lfed-cli-plugins-release.yml@refs/tags/lfed-cli-cluster/v1.2.01": false,
		"https://github.com/LF-Certification/p3-platform/.github/workflows/lfed-cli-plugins-release.yml@refs/tags/lfed-cli-labpro/v1.2.0":   false,
		"https://github.com/LF-Certification/p3-platform/.github/workflows/lfed-cli-plugins-release.yml@refs/tags/lfed-cli-cluster/v1x2x0":  false,
		"https://github.com/evil/p3-platform/.github/workflows/lfed-cli-plugins-release.yml@refs/tags/lfed-cli-cluster/v1.2.0":              false,
	} {
		if re.MatchString(san) != ok {
			t.Errorf("SAN %s: match = %v, want %v", san, !ok, ok)
		}
	}

	if _, err := signerFor(src, "", Options{}, entry); err == nil {
		t.Error("catalog entry without version accepted")
	}

	other := Source{Type: SourceOCI, Ref: "ghcr.io/acme/cluster"}
	_, err = signerFor(other, "1.2.0", Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "--certificate-identity-regexp") || !strings.Contains(err.Error(), "--certificate-oidc-issuer") || !strings.Contains(err.Error(), "--allow-unsigned") {
		t.Errorf("OCI reference without flags: err = %v", err)
	}

	_, err = signerFor(other, "1.2.0", Options{CertificateIdentityRegexp: `^https://github\.com/acme/`}, nil)
	if err == nil {
		t.Error("identity flag without issuer accepted")
	}
	policy := Options{CertificateIdentityRegexp: `^https://github\.com/acme/`, CertificateOIDCIssuer: "https://issuer.example.com"}
	s, err = signerFor(other, "1.2.0", policy, nil)
	if err != nil || s.SANRegex != `^https://github\.com/acme/` || s.Issuer != "https://issuer.example.com" {
		t.Errorf("flags: signer = %+v, err = %v", s, err)
	}
	s, err = signerFor(src, "1.2.0", policy, entry)
	if err != nil || s.SANRegex != `^https://github\.com/acme/` {
		t.Errorf("flags do not override the catalog entry: signer = %+v, err = %v", s, err)
	}
	policy.CertificateIdentityRegexp = "("
	if _, err := signerFor(other, "1.2.0", policy, nil); err == nil {
		t.Error("invalid identity regexp accepted")
	}
}

func TestParseBundleReferrer(t *testing.T) {
	bundleLayer := ocispec.Descriptor{
		MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json",
		Digest:    digest.FromString("bundle"),
		Size:      1234,
	}
	manifest := func(layers ...ocispec.Descriptor) []byte {
		m := ocispec.Manifest{
			Versioned:    specs.Versioned{SchemaVersion: 2},
			MediaType:    ocispec.MediaTypeImageManifest,
			ArtifactType: bundleArtifactType,
			Config:       ocispec.DescriptorEmptyJSON,
			Layers:       layers,
			Subject:      &ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromString("index"), Size: 10},
		}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	other := ocispec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromString("x"), Size: 1}
	big := bundleLayer
	big.Size = maxBundleBytes + 1

	got, err := parseBundleReferrer(manifest(other, bundleLayer))
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != bundleLayer.Digest || got.Size != bundleLayer.Size {
		t.Errorf("layer = %+v, want %+v", got, bundleLayer)
	}

	for name, c := range map[string]struct {
		data []byte
		err  string
	}{
		"no bundle":   {manifest(other), "0 Sigstore bundle layers"},
		"two bundles": {manifest(bundleLayer, bundleLayer), "2 Sigstore bundle layers"},
		"too large":   {manifest(big), "the limit is"},
		"not json":    {[]byte("{"), "parse manifest"},
	} {
		if _, err := parseBundleReferrer(c.data); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: err = %v, want containing %q", name, err, c.err)
		}
	}
}

func TestReferrerBundlesBounded(t *testing.T) {
	var manifestFetches int
	index := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
	indexDesc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromBytes(index), Size: int64(len(index))}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/v2/acme/foo/referrers/"):
			var page ocispec.Index
			page.SchemaVersion = 2
			page.MediaType = ocispec.MediaTypeImageIndex
			for i := range maxReferrers + 1 {
				body := []byte(fmt.Sprintf(`{"n":%d}`, i))
				page.Manifests = append(page.Manifests, ocispec.Descriptor{
					MediaType:    ocispec.MediaTypeImageManifest,
					ArtifactType: bundleArtifactType,
					Digest:       digest.FromBytes(body),
					Size:         int64(len(body)),
				})
			}
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			_ = json.NewEncoder(w).Encode(page)
		case strings.HasPrefix(r.URL.Path, "/v2/acme/foo/manifests/"):
			manifestFetches++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo, err := ociRepository(Source{Type: SourceOCI, Ref: host + "/acme/foo"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = referrerBundles(context.Background(), repo, indexDesc)
	if err == nil || !strings.Contains(err.Error(), "signature referrers") {
		t.Fatalf("err = %v, want the referrer bound", err)
	}
	if manifestFetches != 0 {
		t.Errorf("fetched %d referrer manifests before the bound applied", manifestFetches)
	}
}
