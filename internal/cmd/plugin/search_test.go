package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.ed.linuxfoundation.org/lfed/internal/config"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
)

func writeCatalog(t *testing.T, dir, file string, names ...string) string {
	t.Helper()
	c := pluginmgr.Catalog{SchemaVersion: pluginmgr.CatalogSchemaVersion}
	for _, n := range names {
		c.Plugins = append(c.Plugins, pluginmgr.CatalogEntry{
			Name: n, Summary: "Summary of " + n, Owner: "org/team",
			Ref:    "registry.example/plugins/" + n,
			Signer: pluginmgr.Signer{Issuer: "https://issuer.example", Identity: "id@v{version}"},
		})
	}
	data, _ := json.Marshal(c)
	p := filepath.Join(dir, file)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func installedFrom(t *testing.T, pluginDir, name, version, catalog string) {
	t.Helper()
	writeFake(t, pluginDir, name, version, "x")
	rec := map[string]any{
		"schema_version": "1", "name": name, "version": version, "summary": "x", "requires_lfed": ">=0.1.0 <1",
		"source": pluginmgr.Source{Type: pluginmgr.SourceCatalog, Ref: "registry.example/plugins/" + name, Catalog: catalog, Version: version},
	}
	data, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(pluginDir, name, pluginmgr.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func searchSetup(t *testing.T) (pluginDir string, cfg *config.Config) {
	t.Helper()
	dir := t.TempDir()
	pluginDir = t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", pluginDir)
	t.Setenv("LFED_CATALOG", writeCatalog(t, dir, "default.json", "monkeysay", "exam-lint"))
	cfg = &config.Config{Catalogs: map[string]config.Catalog{
		"acme": {Ref: writeCatalog(t, dir, "acme.json", "exam-tools", "deploy")},
	}}
	return pluginDir, cfg
}

func search(t *testing.T, cfg *config.Config, outputFlag string, args ...string) (string, string, error) {
	t.Helper()
	f, out, errOut := newFactory(outputFlag)
	f.Config = func() (*config.Config, error) { return cfg, nil }
	err := execute(t, f, append([]string{"search"}, args...)...)
	return out.String(), errOut.String(), err
}

func names(t *testing.T, jsonOut string) []string {
	t.Helper()
	var rows []searchRow
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("parse %q: %v", jsonOut, err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestSearchAcrossCatalogsUsesInstallNames(t *testing.T) {
	_, cfg := searchSetup(t)
	out, _, err := search(t, cfg, "json", "exam")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, out), ","); got != "acme/exam-tools,exam-lint" {
		t.Errorf("names = %s", got)
	}
}

func TestSearchCatalogPrefix(t *testing.T) {
	_, cfg := searchSetup(t)
	out, _, err := search(t, cfg, "json", "acme/")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, out), ","); got != "acme/deploy,acme/exam-tools" {
		t.Errorf("acme/: names = %s", got)
	}
	out, _, err = search(t, cfg, "json", "default/monkey")
	if err != nil || strings.Join(names(t, out), ",") != "monkeysay" {
		t.Errorf("default/monkey: %s %v", out, err)
	}
	if _, _, err := search(t, cfg, "json", "nope/x"); err == nil || !strings.Contains(err.Error(), `no plugin catalog "nope"`) {
		t.Errorf("unknown catalog: err = %v", err)
	}
}

func TestSearchNameMatchesSortFirst(t *testing.T) {
	_, cfg := searchSetup(t)
	out, _, err := search(t, cfg, "json", "team")
	if err != nil || len(names(t, out)) != 4 {
		t.Fatalf("owner match: %s %v", out, err)
	}
	out, _, _ = search(t, cfg, "json", "DEPLOY")
	if got := strings.Join(names(t, out), ","); got != "acme/deploy" {
		t.Errorf("case-insensitive: %s", got)
	}
}

func TestSearchMarksInstalledFromSameCatalog(t *testing.T) {
	pluginDir, cfg := searchSetup(t)
	installedFrom(t, pluginDir, "monkeysay", "0.2.0", "")
	installedFrom(t, pluginDir, "deploy", "1.0.0", "other")
	out, _, err := search(t, cfg, "table", "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"monkeysay        0.2.0", "acme/deploy      -"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestSearchSkipsBrokenCatalog(t *testing.T) {
	_, cfg := searchSetup(t)
	cfg.Catalogs["broken"] = config.Catalog{Ref: "/does/not/exist.json"}
	out, errOut, err := search(t, cfg, "json", "deploy")
	if err != nil || strings.Join(names(t, out), ",") != "acme/deploy" {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if !strings.Contains(errOut, "warning: skipped plugin catalog broken") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestSearchNoMatchFails(t *testing.T) {
	_, cfg := searchSetup(t)
	if _, _, err := search(t, cfg, "table", "zzz"); err == nil || !strings.Contains(err.Error(), `no plugins match "zzz"`) {
		t.Errorf("err = %v", err)
	}
}

func TestSearchJQ(t *testing.T) {
	_, cfg := searchSetup(t)
	f, out, _ := newFactory("")
	f.JQ = ".[].name"
	f.Config = func() (*config.Config, error) { return cfg, nil }
	if err := execute(t, f, "search", "acme/"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "acme/deploy\nacme/exam-tools" {
		t.Errorf("jq output = %q", got)
	}
}

func TestSearchRequiresText(t *testing.T) {
	_, cfg := searchSetup(t)
	if _, _, err := search(t, cfg, "table"); err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Errorf("no argument: err = %v", err)
	}
	for _, q := range []string{"", "  "} {
		if _, _, err := search(t, cfg, "table", q); err == nil || !strings.Contains(err.Error(), "search text is empty") {
			t.Errorf("%q: err = %v", q, err)
		}
	}
}
