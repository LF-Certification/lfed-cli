package pluginmgr

import (
	"encoding/json"
	"reflect"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestSourceCode(t *testing.T) {
	const (
		repo = "https://github.com/acme/plugins"
		rev  = "ea90769d0f583f6a8f595db8d7f60139286b3eba"
	)
	cert := &attestation{Repository: repo, Revision: rev}
	signed := &attestation{}
	ann := func(source, revision string) map[string]string {
		return map[string]string{ocispec.AnnotationSource: source, ocispec.AnnotationRevision: revision}
	}
	tests := []struct {
		name        string
		att         *attestation
		annotations map[string]string
		want        *SourceCode
		wantURL     string
	}{
		{
			name:        "certificate and matching folder link",
			att:         cert,
			annotations: ann("https://GitHub.com/acme/plugins/tree/"+rev+"/plugins/hello", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Link: "https://GitHub.com/acme/plugins/tree/" + rev + "/plugins/hello", Basis: BasisCertificate},
			wantURL:     "https://GitHub.com/acme/plugins/tree/" + rev + "/plugins/hello",
		},
		{
			name:        "certificate drops a repository root link",
			att:         cert,
			annotations: ann(repo, ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate wins over a mismatching link",
			att:         cert,
			annotations: ann("https://github.com/evil/plugins/tree/"+rev, "0000000"),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a link at another revision",
			att:         cert,
			annotations: ann(repo+"/tree/main/hello", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a link to a longer repository name",
			att:         cert,
			annotations: ann(repo+"-evil", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a link that climbs out of the repository",
			att:         cert,
			annotations: ann(repo+"/tree/"+rev+"/../../../../evil/x", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a percent-encoded climb",
			att:         cert,
			annotations: ann(repo+"/tree/"+rev+"/%2e%2e/%2e%2e/%2e%2e/%2e%2e/evil/x", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a backslash climb",
			att:         cert,
			annotations: ann(repo+"/tree/"+rev+"/..\\..\\..\\..\\evil/x", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a link with a query",
			att:         cert,
			annotations: ann(repo+"/tree/"+rev+"/hello?x=/../../evil", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "certificate rejects a link at a longer revision",
			att:         cert,
			annotations: ann(repo+"/tree/"+rev+"0/hello", ""),
			want:        &SourceCode{Repository: repo, Revision: rev, Basis: BasisCertificate},
			wantURL:     repo + "/tree/" + rev,
		},
		{
			name:        "signed annotation only",
			att:         signed,
			annotations: ann(repo, rev),
			want:        &SourceCode{Link: repo, Revision: rev, Basis: BasisSignedAnnotation},
			wantURL:     repo,
		},
		{
			name:        "unsigned annotation",
			annotations: ann(repo, "not-a-commit"),
			want:        &SourceCode{Link: repo, Basis: BasisUnsignedAnnotation},
			wantURL:     repo,
		},
		{name: "http link", annotations: ann("http://github.com/acme/plugins", rev)},
		{name: "control character", annotations: ann("https://github.com/acme/plugins\x1b[2J", rev)},
		{name: "C1 control character", annotations: ann("https://github.com/acme/plugins\u009b2J", rev)},
		{name: "space in link", annotations: ann("https://github.com/acme/plug ins", rev)},
		{name: "javascript link", att: signed, annotations: ann("javascript:alert(1)", rev)},
		{name: "credentials in link", annotations: ann("https://user:pw@github.com/acme/plugins", rev)},
		{name: "nothing", att: signed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sourceCode(tt.att, tt.annotations)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("sourceCode = %+v, want %+v", got, tt.want)
			}
			if got != nil && got.URL() != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.URL(), tt.wantURL)
			}
		})
	}
}

// A record whose catalog_entry holds the whole catalog entry still loads.
func TestProvenanceLoadsFullCatalogEntry(t *testing.T) {
	var p Provenance
	if err := json.Unmarshal([]byte(`{"installed_at": "2026-11-16T01:02:03Z",
	  "catalog_entry": {"name": "x", "summary": "X", "owner": "acme/tools", "homepage": "https://example.com/x",
	    "ref": "ghcr.io/acme/x", "signer": {"issuer": "https://issuer", "identity": "https://id/{version}"}}}`), &p); err != nil {
		t.Fatal(err)
	}
	want := &CatalogSnapshot{Owner: "acme/tools", Homepage: "https://example.com/x"}
	if !reflect.DeepEqual(p.CatalogEntry, want) {
		t.Errorf("CatalogEntry = %+v, want %+v", p.CatalogEntry, want)
	}
}
