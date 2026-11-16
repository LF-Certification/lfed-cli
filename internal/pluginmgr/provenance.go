package pluginmgr

import (
	"net/url"
	"regexp"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Provenance is what lfed learned about a plugin while it installed it.
// install() is the only writer. A nil *Provenance means the record predates
// provenance.
type Provenance struct {
	InstalledAt time.Time `json:"installed_at"`

	// Code is nil when nothing named the source code.
	Code *SourceCode `json:"source_code,omitempty"`

	// Signature is nil for a path install and for --allow-unsigned.
	Signature *Signature `json:"signature,omitempty"`

	// CatalogEntry is nil unless a catalog resolved the plugin.
	CatalogEntry *CatalogSnapshot `json:"catalog_entry,omitempty"`
}

// CatalogSnapshot is the part of the catalog entry that info shows, as it
// was at install time.
type CatalogSnapshot struct {
	Owner    string `json:"owner"`
	Homepage string `json:"homepage,omitempty"`
}

// Basis says who vouches for SourceCode.Repository and Revision.
type Basis string

const (
	// BasisCertificate: the Fulcio certificate of the verified signature.
	// The OIDC issuer sets these values, not the publisher.
	BasisCertificate Basis = "certificate"
	// BasisSignedAnnotation: the publisher's index annotations, covered by
	// the verified signature.
	BasisSignedAnnotation Basis = "signed-annotation"
	// BasisUnsignedAnnotation: the same annotations, installed with
	// --allow-unsigned.
	BasisUnsignedAnnotation Basis = "unsigned-annotation"
)

type SourceCode struct {
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision,omitempty"`
	// Link is the publisher's image.source. Under a certificate it is kept
	// only when it is <Repository>/tree/<Revision>[/<dir>], which can name
	// the plugin's folder in a monorepo.
	Link  string `json:"link,omitempty"`
	Basis Basis  `json:"basis"`
}

// URL is the page to open for the source code.
func (s SourceCode) URL() string {
	switch {
	case s.Link != "":
		return s.Link
	case s.Revision != "" && strings.HasPrefix(strings.ToLower(s.Repository), "https://github.com/"):
		return s.Repository + "/tree/" + s.Revision
	}
	return s.Repository
}

// Signature is the certificate that actually signed. Signer is what a
// catalog expects: its Identity is a template with {version}.
type Signature struct {
	Issuer string `json:"issuer"`
	// Identity is the certificate SAN, not the regexp it matched.
	Identity string `json:"identity"`
	BuildRun string `json:"build_run,omitempty"`
}

// attestation is what verifySignature read from the certificate that passed.
type attestation struct {
	Signature
	Repository string
	Revision   string
}

var revisionRe = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// sourceCode decides where a plugin's source code came from. A certificate
// repository wins over the publisher's annotations. Annotation values are
// untrusted: a malformed one counts as absent and never fails an install.
func sourceCode(att *attestation, annotations map[string]string) *SourceCode {
	link := httpsURL(annotations[ocispec.AnnotationSource])
	if att != nil && att.Repository != "" {
		c := &SourceCode{Repository: att.Repository, Revision: att.Revision, Basis: BasisCertificate}
		if link != "" && linkMatches(link, c.Repository, c.Revision) {
			c.Link = link
		}
		return c
	}
	if link == "" {
		return nil
	}
	c := &SourceCode{Link: link, Basis: BasisUnsignedAnnotation}
	if att != nil {
		c.Basis = BasisSignedAnnotation
	}
	if rev := annotations[ocispec.AnnotationRevision]; revisionRe.MatchString(rev) {
		c.Revision = rev
	}
	return c
}

var linkDirRe = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)*$`)

// linkMatches reports whether link is <repo>/tree/<revision>[/<dir>]. A
// link to the repository root adds nothing and would hide the revision.
// The directory is held to plain path segments: a browser resolves "..",
// "%2e%2e" and "\" into another repository on the same host.
func linkMatches(link, repo, revision string) bool {
	repo = strings.TrimSuffix(repo, "/")
	prefix := repo + "/tree/" + revision
	if revision == "" || len(link) < len(prefix) || !strings.EqualFold(link[:len(repo)], repo) || link[len(repo):len(prefix)] != "/tree/"+revision {
		return false
	}
	dir := link[len(prefix):]
	if !linkDirRe.MatchString(dir) {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func httpsURL(s string) string {
	if strings.ContainsFunc(s, func(r rune) bool { return r <= 0x20 || r >= 0x7f }) {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return s
}
