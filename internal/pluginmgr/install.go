package pluginmgr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type Options struct {
	Reserved []string

	AllowUnsigned bool

	CertificateIdentityRegexp string
	CertificateOIDCIssuer     string

	// Catalog overrides the default catalog location: an OCI reference,
	// a file path, or a file:// URL. Empty selects LFED_CATALOG, then
	// config.yaml catalogs.default.ref, then DefaultCatalog. A qualified
	// <catalog>/<name> ignores it.
	Catalog string
}

type fetchFunc func(ctx context.Context, bin string) (Source, sdkplugin.Manifest, error)

func Install(ctx context.Context, f *cmdutil.Factory, arg string, opts Options) (Installed, error) {
	src, err := ParseSource(arg)
	if err != nil {
		return Installed{}, err
	}
	switch src.Type {
	case SourcePath:
		return install(ctx, f, opts, func(ctx context.Context, bin string) (Source, sdkplugin.Manifest, error) {
			if err := fetchPath(src.Path, bin); err != nil {
				return src, sdkplugin.Manifest{}, err
			}
			m, err := metadata(ctx, bin)
			return src, m, err
		})
	case SourceOCI, SourceCatalog:
		if slices.Contains(opts.Reserved, src.Name()) {
			return Installed{}, fmt.Errorf("plugin name %q is a core lfed command", src.Name())
		}
		var entry *CatalogEntry
		if src.Type == SourceCatalog {
			if err := checkCatalogReplace(src); err != nil {
				return Installed{}, err
			}
			var err error
			if src, entry, err = resolveCatalog(ctx, f, src, opts); err != nil {
				return Installed{}, err
			}
		}
		repo, err := ociRepository(src)
		if err != nil {
			return Installed{}, err
		}
		res, err := resolveOCI(ctx, repo, src, f.Version)
		if err != nil {
			return Installed{}, err
		}
		return installOCI(ctx, f, repo, src, res, opts, entry)
	}
	return Installed{}, fmt.Errorf("unknown source type %q", src.Type)
}

// checkCatalogReplace refuses to replace a plugin that another catalog
// installed. The user removes it first, so the change of trust is explicit.
// A missing or unreadable record does not block: "lfed plugin install"
// is the repair for it.
func checkCatalogReplace(src Source) error {
	p, err := Lookup(src.Name())
	if err != nil {
		return nil
	}
	if p.Source.Type == SourceCatalog && p.Source.Catalog != src.Catalog {
		return fmt.Errorf("plugin %q is installed from plugin catalog %s; run \"lfed plugin remove %s\" to install it from plugin catalog %s",
			src.Name(), CatalogLabel(p.Source.Catalog), src.Name(), CatalogLabel(src.Catalog))
	}
	return nil
}

// resolveCatalog turns a catalog-name source into its OCI repository and
// returns the catalog entry that names the plugin's signer.
func resolveCatalog(ctx context.Context, f *cmdutil.Factory, src Source, opts Options) (Source, *CatalogEntry, error) {
	c, from, err := loadCatalog(ctx, f, opts, src.Catalog)
	if err != nil {
		return src, nil, err
	}
	entry, ok := c.Lookup(src.Name())
	if !ok {
		return src, nil, &CatalogNotFoundError{Name: src.Name(), Alias: src.Catalog, Catalog: from}
	}
	src.Ref = entry.Ref
	return src, &entry, nil
}

func installOCI(ctx context.Context, f *cmdutil.Factory, repo *remote.Repository, src Source, res resolved, opts Options, entry *CatalogEntry) (Installed, error) {
	return install(ctx, f, opts, func(ctx context.Context, bin string) (Source, sdkplugin.Manifest, error) {
		return fetchOCI(ctx, f.Version, repo, src, res, opts, entry, bin)
	})
}

func install(ctx context.Context, f *cmdutil.Factory, opts Options, fetch fetchFunc) (Installed, error) {
	dir, err := Dir()
	if err != nil {
		return Installed{}, err
	}
	// #nosec G301
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Installed{}, fmt.Errorf("create plugin dir: %w", err)
	}

	stage, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return Installed{}, fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stage)

	bin := filepath.Join(stage, "plugin")
	src, m, err := fetch(ctx, bin)
	if err != nil {
		return Installed{}, err
	}
	if err := validate(m, f.Version, opts.Reserved); err != nil {
		return Installed{}, fmt.Errorf("plugin manifest invalid: %w", err)
	}
	if src.Type == SourceOCI || src.Type == SourceCatalog {
		if m.Name != src.Name() {
			return Installed{}, fmt.Errorf("%s publishes plugin %q, not %q", src.Ref, m.Name, src.Name())
		}
		if !src.digestPin() && m.Version != src.Version {
			return Installed{}, fmt.Errorf("%s:%s reports version %q; the tag and the manifest disagree", src.Ref, src.Version, m.Version)
		}
	}

	var manifest bytes.Buffer
	enc := json.NewEncoder(&manifest)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(record{Manifest: m, Source: src}); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(bin, filepath.Join(stage, BinaryName(m.Name))); err != nil {
		return Installed{}, err
	}
	// #nosec G306
	if err := os.WriteFile(filepath.Join(stage, ManifestFile), manifest.Bytes(), 0o644); err != nil {
		return Installed{}, fmt.Errorf("write manifest: %w", err)
	}

	final := filepath.Join(dir, m.Name)
	if err := replaceDir(stage, final); err != nil {
		return Installed{}, err
	}
	if src.Type != SourcePath && opts.AllowUnsigned {
		fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s installed without signature verification\n", src)
	}
	return Installed{Manifest: m, Source: src, Dir: final, Binary: filepath.Join(final, BinaryName(m.Name))}, nil
}

var renameDir = os.Rename

func replaceDir(stage, final string) error {
	prev := ""
	if _, err := os.Lstat(final); err == nil {
		prev = filepath.Join(filepath.Dir(final), ".prev-"+filepath.Base(final)+"-"+rand.Text())
		if err := renameDir(final, prev); err != nil {
			return fmt.Errorf("move existing plugin aside: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect existing plugin: %w", err)
	}
	if err := renameDir(stage, final); err != nil {
		if prev != "" {
			if rerr := renameDir(prev, final); rerr != nil {
				return fmt.Errorf("install plugin: %w; the previous install is in %s: %v", err, prev, rerr)
			}
		}
		return fmt.Errorf("install plugin: %w", err)
	}
	if prev != "" {
		_ = os.RemoveAll(prev)
	}
	return nil
}

func fetchPath(path, bin string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; pass the plugin executable", path)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return copyFile(path, bin)
}

func fetchOCI(ctx context.Context, running string, repo *remote.Repository, src Source, res resolved, opts Options, entry *CatalogEntry, bin string) (Source, sdkplugin.Manifest, error) {
	label := src.Name() + "@" + res.Version
	if src.digestPin() {
		label = src.Name() + "@" + src.Digest
	}
	if requires := res.Index.Annotations[AnnotationRequiresLfed]; requires != "" {
		if err := CheckLfedVersion(requires, running); err != nil {
			var incompatible *IncompatibleError
			if errors.As(err, &incompatible) {
				incompatible.Name = label
			}
			return src, sdkplugin.Manifest{}, err
		}
	}
	if !src.digestPin() {
		src.Version = res.Version
	}
	src.Digest = res.Desc.Digest.String()
	ref := repoRef(repo, src.Digest)
	if !opts.AllowUnsigned {
		s, err := signerFor(src, res.Version, opts, entry)
		if err != nil {
			return src, sdkplugin.Manifest{}, err
		}
		if err := verifySignature(ctx, repo, ref, res.Desc, s); err != nil {
			return src, sdkplugin.Manifest{}, err
		}
	}
	m, err := pullOCI(ctx, repo, res.Index, src.Name(), ref, bin)
	if err != nil {
		return src, m, err
	}
	return src, m, checkAnnotations(ref, res.Index.Annotations, m)
}

func Remove(name string) error {
	if !nameRe.MatchString(name) {
		return &NotFoundError{Name: name}
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		return &NotFoundError{Name: name}
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove plugin %q: %w", name, err)
	}
	return nil
}

func metadata(ctx context.Context, bin string) (sdkplugin.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "__metadata")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("run __metadata: %w%s", err, trailer(stderr.String()))
	}
	var m sdkplugin.Manifest
	if err := json.Unmarshal(out, &m); err != nil {
		return sdkplugin.Manifest{}, fmt.Errorf("parse __metadata output: %w", err)
	}
	return m, nil
}

func validate(m sdkplugin.Manifest, running string, reserved []string) error {
	if m.SchemaVersion != sdkplugin.ManifestSchemaVersion {
		return fmt.Errorf("schema_version %q; expected %q", m.SchemaVersion, sdkplugin.ManifestSchemaVersion)
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("name %q must match %s", m.Name, nameRe)
	}
	if slices.Contains(reserved, m.Name) {
		return fmt.Errorf("name %q is a core lfed command", m.Name)
	}
	if m.Version == "" {
		return errors.New("version is empty")
	}
	if m.Summary == "" {
		return errors.New("summary is empty")
	}
	err := CheckLfedVersion(m.RequiresLfed, running)
	var incompatible *IncompatibleError
	if errors.As(err, &incompatible) {
		incompatible.Name = m.Name
	}
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// #nosec G302
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", src, err)
	}
	return out.Close()
}

func trailer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return ": " + s
}
