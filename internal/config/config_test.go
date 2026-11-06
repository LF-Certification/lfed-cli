package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveRoundTripAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lfed")
	t.Setenv("LFED_CONFIG_DIR", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	in := &Config{CurrentContext: "staging", Catalogs: map[string]Catalog{
		"acme": {Ref: "ghcr.io/acme/catalog:latest", Signer: CatalogSigner{Issuer: "https://i", Identity: "https://id"}},
	}}
	if err := in.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := Load()
	if err != nil || cfg.CurrentContext != "staging" || cfg.Catalogs["acme"] != in.Catalogs["acme"] {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatalf("config dir entries = %v, %v", entries, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, fileName): 0o600} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != want {
			t.Errorf("%s mode = %o, want %o", p, perm, want)
		}
	}
}
