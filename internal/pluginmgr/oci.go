package pluginmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"
	"oras.land/oras-go/v2/registry/remote/retry"

	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

const (
	ArtifactType = "application/vnd.linuxfoundation.ed.cli.plugin.v1"

	ConfigMediaType = "application/vnd.linuxfoundation.ed.cli.plugin.config.v1+json"

	LayerMediaType = "application/vnd.linuxfoundation.ed.cli.plugin.layer.v1.tar+gzip"

	AnnotationName = "org.linuxfoundation.ed.cli.plugin.name"

	AnnotationRequiresLfed = "org.linuxfoundation.ed.cli.plugin.requires_lfed"
)

const (
	maxConfigBytes = 64 << 10

	maxTarEntries = 64
)

var maxLayerBytes int64 = 512 << 20

type NoReleaseError struct {
	Ref     string
	Running string

	Newest, Requires string
}

func (e *NoReleaseError) Error() string {
	if e.Newest == "" {
		return fmt.Sprintf("%s has no releases", e.Ref)
	}
	return fmt.Sprintf("no release of %s supports lfed %s; newest is %s (requires %s)", e.Ref, e.Running, e.Newest, e.Requires)
}

func ociRepository(src Source) (*remote.Repository, error) {
	repo, err := remote.NewRepository(src.Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI reference %q: %w", src.Ref, err)
	}
	repo.PlainHTTP = plainHTTP(repo.Reference.Registry)

	repo.ReferrerListMaxPages = maxReferrerPages
	repo.Client = &auth.Client{
		Client: &http.Client{Transport: retry.NewTransport(&http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ForceAttemptHTTP2:     true,
		})},
		Cache:      auth.NewCache(),
		Credential: credential,
	}
	return repo, nil
}

func plainHTTP(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

type resolved struct {
	Version string

	Desc  ocispec.Descriptor
	Index ocispec.Index
}

func resolveOCI(ctx context.Context, repo *remote.Repository, src Source, running string) (resolved, error) {
	switch {
	case src.Digest != "" && src.Version == "":
		desc, idx, err := fetchIndex(ctx, repo, src.Digest)
		return resolved{Version: idx.Annotations[ocispec.AnnotationVersion], Desc: desc, Index: idx}, err
	case src.Version != "":
		desc, idx, err := fetchIndex(ctx, repo, src.Version)
		return resolved{Version: src.Version, Desc: desc, Index: idx}, err
	}
	var versions []*semver.Version

	err := repo.Tags(ctx, "", func(tags []string) error {
		for _, t := range tags {
			if v, err := semver.StrictNewVersion(t); err == nil && v.Prerelease() == "" && v.Metadata() == "" {
				versions = append(versions, v)
			}
		}
		return nil
	})
	if err != nil {
		return resolved{}, ociError(src.Ref, err)
	}
	sort.Sort(sort.Reverse(semver.Collection(versions)))
	notFound := &NoReleaseError{Ref: src.Ref, Running: running}
	for _, v := range versions {
		tag := v.Original()
		desc, idx, err := fetchIndex(ctx, repo, tag)
		if err != nil {
			return resolved{}, err
		}
		requires := idx.Annotations[AnnotationRequiresLfed]
		if requires == "" || CheckLfedVersion(requires, running) == nil {
			return resolved{Version: tag, Desc: desc, Index: idx}, nil
		}
		if notFound.Newest == "" {
			notFound.Newest, notFound.Requires = tag, requires
		}
	}
	return resolved{}, notFound
}

func fetchIndex(ctx context.Context, repo *remote.Repository, reference string) (ocispec.Descriptor, ocispec.Index, error) {
	ref := repoRef(repo, reference)
	desc, data, err := fetchReference(ctx, repo, reference)
	if err != nil {
		return ocispec.Descriptor{}, ocispec.Index{}, ociError(ref, err)
	}
	if desc.MediaType != ocispec.MediaTypeImageIndex {
		return ocispec.Descriptor{}, ocispec.Index{}, fmt.Errorf("%s is %s; expected an OCI index", ref, desc.MediaType)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return ocispec.Descriptor{}, ocispec.Index{}, fmt.Errorf("parse index %s: %w", ref, err)
	}
	return desc, idx, nil
}

func repoRef(repo *remote.Repository, reference string) string {
	sep := ":"
	if strings.Contains(reference, ":") {
		sep = "@"
	}
	return repo.Reference.Registry + "/" + repo.Reference.Repository + sep + reference
}

func fetchReference(ctx context.Context, repo *remote.Repository, ref string) (ocispec.Descriptor, []byte, error) {
	desc, rc, err := repo.Manifests().FetchReference(ctx, ref)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	defer rc.Close()
	data, err := content.ReadAll(rc, desc)
	return desc, data, err
}

func pullOCI(ctx context.Context, repo *remote.Repository, idx ocispec.Index, name, ref, dst string) (sdkplugin.Manifest, error) {
	var platform *ocispec.Descriptor
	for i := range idx.Manifests {
		p := idx.Manifests[i].Platform
		if p != nil && p.OS == runtime.GOOS && p.Architecture == runtime.GOARCH {
			platform = &idx.Manifests[i]
			break
		}
	}
	if platform == nil {
		return sdkplugin.Manifest{}, fmt.Errorf("%s has no build for %s/%s", ref, runtime.GOOS, runtime.GOARCH)
	}
	data, err := content.FetchAll(ctx, repo.Manifests(), *platform)
	if err != nil {
		return sdkplugin.Manifest{}, ociError(ref, err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("parse manifest %s: %w", ref, err)
	}
	if m.ArtifactType != ArtifactType {
		return sdkplugin.Manifest{}, fmt.Errorf("%s: platform manifest has artifactType %q; expected %q", ref, m.ArtifactType, ArtifactType)
	}
	pm, err := fetchConfig(ctx, repo, m.Config)
	if err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("%s: %w", ref, err)
	}
	var layer *ocispec.Descriptor
	for i := range m.Layers {
		if m.Layers[i].MediaType == LayerMediaType {
			layer = &m.Layers[i]
			break
		}
	}
	if layer == nil {
		return sdkplugin.Manifest{}, fmt.Errorf("%s has no %s layer", ref, LayerMediaType)
	}
	if layer.Size > maxLayerBytes {
		return sdkplugin.Manifest{}, fmt.Errorf("%s: layer is %d bytes; the limit is %d", ref, layer.Size, maxLayerBytes)
	}
	rc, err := repo.Blobs().Fetch(ctx, *layer)
	if err != nil {
		return sdkplugin.Manifest{}, ociError(ref, err)
	}
	defer rc.Close()
	vr := content.NewVerifyReader(rc, *layer)
	if err := extractBinary(vr, BinaryName(name), dst); err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("%s: %w", ref, err)
	}
	if err := vr.Verify(); err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("%s: layer %w", ref, err)
	}
	return pm, nil
}

func fetchConfig(ctx context.Context, repo *remote.Repository, cfg ocispec.Descriptor) (sdkplugin.Manifest, error) {
	if cfg.MediaType != ConfigMediaType {
		return sdkplugin.Manifest{}, fmt.Errorf("config has mediaType %q; expected %q", cfg.MediaType, ConfigMediaType)
	}
	if cfg.Size > maxConfigBytes {
		return sdkplugin.Manifest{}, fmt.Errorf("config is %d bytes; the limit is %d", cfg.Size, maxConfigBytes)
	}
	data, err := content.FetchAll(ctx, repo.Blobs(), cfg)
	if err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("fetch config: %w", err)
	}
	var m sdkplugin.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("parse config: %w", err)
	}
	return m, nil
}

func checkAnnotations(ref string, annotations map[string]string, m sdkplugin.Manifest) error {
	for _, c := range []struct{ key, want string }{
		{AnnotationName, m.Name},
		{ocispec.AnnotationVersion, m.Version},
		{AnnotationRequiresLfed, m.RequiresLfed},
	} {
		if got := annotations[c.key]; got != c.want {
			return fmt.Errorf("%s: index annotation %s is %q; the plugin manifest says %q", ref, c.key, got, c.want)
		}
	}
	return nil
}

func extractBinary(r io.Reader, want, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("layer is not gzip: %w", err)
	}
	defer gz.Close()

	expanded := &io.LimitedReader{R: gz, N: maxLayerBytes + 1}
	overflow := func() error {
		if expanded.N <= 0 {
			return fmt.Errorf("layer expands to more than %d bytes", maxLayerBytes)
		}
		return nil
	}
	tr := tar.NewReader(expanded)
	found := false
	for entries := 1; ; entries++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if oerr := overflow(); oerr != nil {
				return oerr
			}
			return fmt.Errorf("read layer tar: %w", err)
		}
		if entries > maxTarEntries {
			return fmt.Errorf("layer holds more than %d entries", maxTarEntries)
		}
		if found || hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != want {
			continue
		}
		// #nosec G302
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		_, cerr := io.CopyN(out, tr, maxLayerBytes+1)
		if errors.Is(cerr, io.EOF) {
			cerr = nil
		}
		if err := out.Close(); cerr == nil {
			cerr = err
		}
		if oerr := overflow(); oerr != nil {
			return oerr
		}
		if cerr != nil {
			return fmt.Errorf("write %s: %w", want, cerr)
		}
		found = true
	}
	if err := overflow(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("layer holds no file named %s", want)
	}

	_, err = io.Copy(io.Discard, r)
	return err
}

func ociError(ref string, err error) error {
	var resp *errcode.ErrorResponse
	if errors.As(err, &resp) {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			host := resp.URL.Host
			hint := fmt.Sprintf(`run "docker login %s"`, host)
			if host == "ghcr.io" {
				hint = `run "gh auth refresh -s read:packages" or "docker login ghcr.io"`
			}
			return fmt.Errorf("%s denied access to %s; %s", host, ref, hint)
		case http.StatusNotFound:
			return fmt.Errorf("%s not found", ref)
		}
	}
	return fmt.Errorf("%s: %w", ref, err)
}
