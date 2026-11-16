package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
)

// writeRecord writes a plugin whose lfed-plugin.json is record.
func writeRecord(t *testing.T, dir, name, record string) string {
	t.Helper()
	pdir := filepath.Join(dir, name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(pdir, pluginmgr.BinaryName(name))
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, pluginmgr.ManifestFile), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}

const manifestJSON = `"schema_version": "1", "version": "0.1.0", "summary": "Say it", "requires_lfed": ">=0.1.0 <1"`

const catalogRecord = `{"name": "monkeysay", ` + manifestJSON + `,
  "source": {"type": "catalog", "ref": "ghcr.io/acme/monkeysay", "version": "0.1.0", "digest": "sha256:abc"},
  "provenance": {
    "installed_at": "2026-11-16T01:02:03Z",
    "source_code": {"repository": "https://github.com/acme/monkeysay", "revision": "ea90769d", "basis": "certificate"},
    "signature": {"issuer": "https://token.actions.githubusercontent.com", "identity": "https://github.com/acme/monkeysay/.github/workflows/release.yml@refs/tags/v0.1.0", "build_run": "https://github.com/acme/monkeysay/actions/runs/1/attempts/1"},
    "catalog_entry": {"owner": "acme/tools", "homepage": "https://example.com/monkeysay"}
  }}`

func TestInfoCatalogPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	bin := writeRecord(t, dir, "monkeysay", catalogRecord)

	got := run(t, "table", "info", "monkeysay")
	want := strings.Join([]string{
		"NAME           monkeysay",
		"VERSION        0.1.0",
		"STATUS         ok",
		"CATALOG        default",
		"SOURCE         ghcr.io/acme/monkeysay@0.1.0",
		"SUMMARY        Say it",
		"REQUIRES LFED  >=0.1.0 <1",
		"OWNER          acme/tools",
		"HOMEPAGE       https://example.com/monkeysay",
		"INSTALLED      2026-11-16T01:02:03Z",
		"DIGEST         sha256:abc",
		"BINARY         " + bin,
		"CODE           https://github.com/acme/monkeysay/tree/ea90769d",
		"REVISION       ea90769d",
		"BASIS          signing certificate",
		"SIGNER         https://github.com/acme/monkeysay/.github/workflows/release.yml@refs/tags/v0.1.0",
		"ISSUER         https://token.actions.githubusercontent.com",
		"BUILD RUN      https://github.com/acme/monkeysay/actions/runs/1/attempts/1",
	}, "\n") + "\n"
	if got != want {
		t.Errorf("table =\n%s\nwant\n%s", got, want)
	}

	var row map[string]any
	if err := json.Unmarshal([]byte(run(t, "json", "info", "monkeysay")), &row); err != nil {
		t.Fatal(err)
	}
	src, _ := row["source"].(map[string]any)
	prov, _ := row["provenance"].(map[string]any)
	if row["name"] != "monkeysay" || row["compatible"] != true || src["ref"] != "ghcr.io/acme/monkeysay" ||
		row["source_url"] != "https://github.com/acme/monkeysay/tree/ea90769d" || row["verification"] != "signed" ||
		row["binary"] != bin || prov["installed_at"] != "2026-11-16T01:02:03Z" ||
		row["catalog"] != "default" || row["owner"] != "acme/tools" || row["homepage"] != "https://example.com/monkeysay" {
		t.Errorf("json = %v", row)
	}

	f, out, _ := newFactory("table")
	f.JQ = ".source_url"
	if err := execute(t, f, "info", "monkeysay"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "https://github.com/acme/monkeysay/tree/ea90769d\n" {
		t.Errorf("jq output = %q", out.String())
	}
}

func TestInfoPathPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeRecord(t, dir, "hello", `{"name": "hello", `+manifestJSON+`,
	  "source": {"type": "path", "path": "/src/lfed-hello"},
	  "provenance": {"installed_at": "2026-11-16T01:02:03Z"}}`)

	got := run(t, "table", "info", "hello")
	for _, line := range []string{
		"CATALOG        -\n",
		"SOURCE         path:/src/lfed-hello\n",
		"CODE           unknown: a local file carries no source facts\n",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("table lacks %q:\n%s", line, got)
		}
	}
	for _, header := range []string{"OWNER", "DIGEST", "BASIS", "SIGNER"} {
		if strings.Contains(got, header) {
			t.Errorf("table has %s for a path plugin:\n%s", header, got)
		}
	}

	var row map[string]any
	if err := json.Unmarshal([]byte(run(t, "json", "info", "hello")), &row); err != nil {
		t.Fatal(err)
	}
	if _, ok := row["source_url"]; ok || row["verification"] != "local" || row["provenance"] == nil {
		t.Errorf("json = %v", row)
	}
	for _, key := range []string{"catalog", "owner", "homepage"} {
		if _, ok := row[key]; ok {
			t.Errorf("json has %s for a path plugin: %v", key, row)
		}
	}
}

func TestInfoOldRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeRecord(t, dir, "reports", `{"name": "reports", `+manifestJSON+`,
	  "source": {"type": "catalog", "ref": "ghcr.io/acme/reports", "catalog": "acme", "version": "1.2.0", "digest": "sha256:abc", "pinned": true}}`)

	got := run(t, "table", "info", "reports")
	if want := `CODE           not recorded: run "lfed plugin install acme/reports@1.2.0" again` + "\n"; !strings.Contains(got, want) {
		t.Errorf("table lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "INSTALLED") {
		t.Errorf("table has INSTALLED for an old record:\n%s", got)
	}

	var row map[string]any
	if err := json.Unmarshal([]byte(run(t, "json", "info", "reports")), &row); err != nil {
		t.Fatal(err)
	}
	if prov, ok := row["provenance"]; !ok || prov != nil || row["verification"] != "not-recorded" {
		t.Errorf("json = %v, want provenance null", row)
	}
}

func TestInfoUnknownPlugin(t *testing.T) {
	t.Setenv("LFED_PLUGIN_DIR", t.TempDir())
	f, _, _ := newFactory("table")
	err := execute(t, f, "info", "nosuch")
	var notFound *pluginmgr.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("err = %v, want *NotFoundError", err)
	}
}

func TestInstallArg(t *testing.T) {
	tests := []struct {
		src  pluginmgr.Source
		want string
	}{
		{pluginmgr.Source{Type: pluginmgr.SourceCatalog, Ref: "ghcr.io/x/hello", Version: "1.0.0"}, "hello"},
		{pluginmgr.Source{Type: pluginmgr.SourceCatalog, Ref: "ghcr.io/x/hello", Catalog: "acme", Digest: "sha256:abc", Pinned: true}, "acme/hello@sha256:abc"},
		{pluginmgr.Source{Type: pluginmgr.SourceOCI, Ref: "ghcr.io/x/hello", Version: "1.0.0", Pinned: true}, "ghcr.io/x/hello:1.0.0"},
		{pluginmgr.Source{Type: pluginmgr.SourceOCI, Ref: "ghcr.io/x/hello", Version: "1.0.0"}, "ghcr.io/x/hello"},
	}
	for _, tt := range tests {
		if got := installArg(tt.src); got != tt.want {
			t.Errorf("installArg(%+v) = %q, want %q", tt.src, got, tt.want)
		}
	}
}
