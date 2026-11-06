package pluginmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestParseSource(t *testing.T) {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	cases := []struct {
		arg  string
		want Source
		err  string
	}{
		{arg: "./cli", want: Source{Type: SourcePath, Path: filepath.Join(cwd, "cli")}},
		{arg: "../x", want: Source{Type: SourcePath, Path: filepath.Join(filepath.Dir(cwd), "x")}},
		{arg: "/opt/p", want: Source{Type: SourcePath, Path: "/opt/p"}},
		{arg: "~/p", want: Source{Type: SourcePath, Path: filepath.Join(home, "p")}},
		{arg: ".", want: Source{Type: SourcePath, Path: cwd}},
		{arg: "cluster", want: Source{Type: SourceCatalog, Ref: "cluster"}},
		{arg: "cluster@1.2.0", want: Source{Type: SourceCatalog, Ref: "cluster", Version: "1.2.0", Pinned: true}},
		{arg: "cluster@" + testDigest, want: Source{Type: SourceCatalog, Ref: "cluster", Digest: testDigest, Pinned: true}},
		{arg: "acme/cluster", want: Source{Type: SourceCatalog, Ref: "cluster", Catalog: "acme"}},
		{arg: "acme/cluster@1.2.0", want: Source{Type: SourceCatalog, Ref: "cluster", Catalog: "acme", Version: "1.2.0", Pinned: true}},
		{arg: "default/cluster", want: Source{Type: SourceCatalog, Ref: "cluster"}},
		{arg: "ghcr.io/acme/tools/foo", want: Source{Type: SourceOCI, Ref: "ghcr.io/acme/tools/foo"}},
		{arg: "ghcr.io/acme/tools/foo:2.0.0", want: Source{Type: SourceOCI, Ref: "ghcr.io/acme/tools/foo", Version: "2.0.0", Pinned: true}},
		{arg: "ghcr.io/acme/foo@" + testDigest, want: Source{Type: SourceOCI, Ref: "ghcr.io/acme/foo", Digest: testDigest, Pinned: true}},
		{arg: "localhost:5000/p/foo:1.0.0", want: Source{Type: SourceOCI, Ref: "localhost:5000/p/foo", Version: "1.0.0", Pinned: true}},
		{arg: "services/delivery/cli", err: "unknown plugin source"},
		{arg: "a/b/c", err: "unknown plugin source"},
		{arg: "Acme/cluster", err: "unknown plugin source"},
		{arg: "Cluster", err: "unknown plugin source"},
		{arg: "cluster@1@2", err: "unknown plugin source"},
		{arg: "cluster@sha256:abc", err: "unknown plugin source"},
		{arg: "ghcr.io/acme/foo@sha512:" + strings.Repeat("0", 128), err: "not sha256"},
		{arg: "ghcr.io/acme/Foo", err: "invalid OCI reference"},
	}
	for _, c := range cases {
		got, err := ParseSource(c.arg)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want containing %q", c.arg, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.arg, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %+v, want %+v", c.arg, got, c.want)
		}
	}
}

func TestSourceString(t *testing.T) {
	cases := []struct {
		src  Source
		want string
	}{
		{Source{Type: SourceOCI, Ref: "r/foo"}, "r/foo"},
		{Source{Type: SourceOCI, Ref: "r/foo", Version: "1.0.0", Digest: testDigest}, "r/foo@1.0.0"},
		{Source{Type: SourceOCI, Ref: "r/foo", Digest: testDigest, Pinned: true}, "r/foo@" + testDigest},
		{Source{Type: SourcePath, Path: "/p"}, "path:/p"},
	}
	for _, c := range cases {
		if got := c.src.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.src, got, c.want)
		}
	}
}
