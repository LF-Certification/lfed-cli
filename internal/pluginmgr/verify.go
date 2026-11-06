package pluginmgr

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	bundleArtifactType = "application/vnd.dev.sigstore.bundle.v0.3+json"

	maxBundleBytes = 1 << 20

	maxReferrerPages = 4

	maxReferrers = 16

	githubIssuer = "https://token.actions.githubusercontent.com"
)

type signer struct {
	Issuer   string
	SANRegex string
}

// signerFor picks the identity that MUST have signed a plugin. Flags win.
// A catalog entry supplies an exact identity for its version. A full OCI
// reference with no flags has no trust anchor and is an error.
func signerFor(src Source, version string, opts Options, entry *CatalogEntry) (signer, error) {
	id, issuer := opts.CertificateIdentityRegexp, opts.CertificateOIDCIssuer
	switch {
	case id != "" && issuer != "":
		if _, err := regexp.Compile(id); err != nil {
			return signer{}, fmt.Errorf("--certificate-identity-regexp: %w", err)
		}
		return signer{Issuer: issuer, SANRegex: id}, nil
	case id != "" || issuer != "":
		return signer{}, errors.New("--certificate-identity-regexp and --certificate-oidc-issuer must be set together")
	case entry != nil:
		if version == "" {
			return signer{}, fmt.Errorf("%s has no %s annotation; the signer identity needs the version", src, ocispec.AnnotationVersion)
		}
		san := entry.Signer.expand(entry.Name, version)
		return signer{Issuer: entry.Signer.Issuer, SANRegex: "^" + regexp.QuoteMeta(san) + "$"}, nil
	}
	return signer{}, fmt.Errorf("%s is not in a plugin catalog; pass --certificate-identity-regexp and --certificate-oidc-issuer to verify its signature, or --allow-unsigned for a registry you control",
		src.Ref)
}

func verifySignature(ctx context.Context, repo *remote.Repository, ref string, desc ocispec.Descriptor, s signer) error {
	if desc.Digest.Algorithm() != digest.SHA256 {
		return fmt.Errorf("%s: index digest %s is not sha256", ref, desc.Digest)
	}
	want, err := hex.DecodeString(desc.Digest.Encoded())
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	bundles, err := referrerBundles(ctx, repo, desc)
	if err != nil {
		return fmt.Errorf("%s: read signatures: %w", ref, err)
	}
	if len(bundles) == 0 {
		return fmt.Errorf("%s has no Sigstore signature; pass --allow-unsigned only for a registry you control", ref)
	}
	trusted, err := trustedRoot(ctx)
	if err != nil {
		return fmt.Errorf("fetch Sigstore trusted root: %w", err)
	}
	v, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1))
	if err != nil {
		return err
	}
	id, err := verify.NewShortCertificateIdentity(s.Issuer, "", "", s.SANRegex)
	if err != nil {
		return err
	}
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", want), verify.WithCertificateIdentity(id))
	var errs []error
	for _, b := range bundles {
		_, err := v.Verify(b, policy)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	return fmt.Errorf("%s: no signature from %s (issuer %s) is valid: %w", ref, s.SANRegex, s.Issuer, errors.Join(errs...))
}

func referrerBundles(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) ([]*bundle.Bundle, error) {
	var referrers []ocispec.Descriptor
	err := repo.Referrers(ctx, desc, bundleArtifactType, func(page []ocispec.Descriptor) error {
		if len(referrers)+len(page) > maxReferrers {
			return fmt.Errorf("more than %d signature referrers", maxReferrers)
		}
		referrers = append(referrers, page...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []*bundle.Bundle
	for _, r := range referrers {
		data, err := content.FetchAll(ctx, repo.Manifests(), r)
		if err != nil {
			return nil, fmt.Errorf("fetch referrer %s: %w", r.Digest, err)
		}
		layer, err := parseBundleReferrer(data)
		if err != nil {
			return nil, fmt.Errorf("referrer %s: %w", r.Digest, err)
		}
		raw, err := content.FetchAll(ctx, repo.Blobs(), layer)
		if err != nil {
			return nil, fmt.Errorf("fetch bundle %s: %w", layer.Digest, err)
		}
		var b bundle.Bundle
		if err := b.UnmarshalJSON(raw); err != nil {
			return nil, fmt.Errorf("parse bundle %s: %w", layer.Digest, err)
		}
		out = append(out, &b)
	}
	return out, nil
}

func parseBundleReferrer(data []byte) (ocispec.Descriptor, error) {
	var m ocispec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parse manifest: %w", err)
	}
	var layers []ocispec.Descriptor
	for _, l := range m.Layers {
		if l.MediaType == bundleArtifactType {
			layers = append(layers, l)
		}
	}
	if len(layers) != 1 {
		return ocispec.Descriptor{}, fmt.Errorf("manifest has %d Sigstore bundle layers; expected 1", len(layers))
	}
	if layers[0].Size > maxBundleBytes {
		return ocispec.Descriptor{}, fmt.Errorf("bundle is %d bytes; the limit is %d", layers[0].Size, maxBundleBytes)
	}
	return layers[0], nil
}

func trustedRoot(ctx context.Context) (*root.TrustedRoot, error) {
	opts := tuf.DefaultOptions().WithContext(ctx)
	if dir, err := os.UserCacheDir(); err == nil {
		opts = opts.WithCachePath(filepath.Join(dir, "lfed", "sigstore"))
	}
	return root.FetchTrustedRootWithOptions(opts)
}
