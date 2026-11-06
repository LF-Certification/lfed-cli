package pluginmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

var helloBin string

func TestMain(m *testing.M) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "skipping pluginmgr tests: go not on PATH")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "lfed-hello-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	helloBin = filepath.Join(dir, "lfed-hello")
	build := exec.Command(goTool, "build", "-o", helloBin, "./testdata/helloplugin")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build helloplugin:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func setPluginDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	return dir
}

func writeFake(t *testing.T, dir, name, summary string) {
	t.Helper()
	writeFakeManifest(t, dir, name, sdkplugin.Manifest{SchemaVersion: "1", Name: name, Version: "1.2.3", Summary: summary, RequiresLfed: ">=0.1.0 <1"})
}

func writeFakeManifest(t *testing.T, dir, name string, m sdkplugin.Manifest) {
	t.Helper()
	pdir := filepath.Join(dir, name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, BinaryName(name)), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(pdir, ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func testFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, _, errOut := iostreams.Test()
	return &cmdutil.Factory{IOStreams: ios, Version: "0.1.0", OutputFlag: "json"}, errOut
}

func TestLookup(t *testing.T) {
	dir := setPluginDir(t)
	writeFake(t, dir, "alpha", "Alpha plugin")

	p, err := Lookup("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "alpha", "lfed-alpha"); p.Binary != want {
		t.Errorf("Binary = %q, want %q", p.Binary, want)
	}
	if p.Manifest.Summary != "Alpha plugin" {
		t.Errorf("Summary = %q", p.Manifest.Summary)
	}

	_, err = Lookup("nosuch")
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
	if notFound.Name != "nosuch" {
		t.Errorf("name = %q", notFound.Name)
	}
}

var badNames = []string{"..", "../..", "a/../..", "/", "foo/..", "-x", "Foo", ".", "", "a/b", `a\b`, ".install-x", ".prev-hello-x"}

func TestLookupRejectsBadNames(t *testing.T) {
	dir := setPluginDir(t)
	writeFake(t, dir, "alpha", "Alpha")
	for _, name := range badNames {
		_, err := Lookup(name)
		var notFound *NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("Lookup(%q): err = %v, want *NotFoundError", name, err)
		}
	}
}

func TestOtherSchemaVersion(t *testing.T) {
	dir := setPluginDir(t)
	writeFake(t, dir, "alpha", "Alpha")
	writeFakeManifest(t, dir, "old", sdkplugin.Manifest{SchemaVersion: "0", Name: "old", Version: "1.2.3", Summary: "Old", RequiresLfed: ">=0.1.0 <1"})

	_, err := Lookup("old")
	if err == nil || !strings.Contains(err.Error(), "install\" again") {
		t.Fatalf("Lookup: err = %v, want a reinstall hint", err)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Manifest.Name != "alpha" {
		t.Errorf("List = %+v, want only alpha", got)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "plugins")
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeFake(t, dir, "alpha", "Alpha")
	canary := filepath.Join(root, "canary")
	if err := os.WriteFile(canary, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range badNames {
		err := Remove(name)
		var notFound *NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("Remove(%q): err = %v, want *NotFoundError", name, err)
		}
	}
	for _, p := range []string{canary, filepath.Join(dir, "alpha", ManifestFile)} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s removed by a bad name: %v", p, err)
		}
	}

	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "broken"} {
		if err := Remove(name); err != nil {
			t.Fatalf("Remove(%q): %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still present: %v", name, err)
		}
	}
	var notFound *NotFoundError
	if err := Remove("alpha"); !errors.As(err, &notFound) {
		t.Errorf("second Remove: err = %v, want *NotFoundError", err)
	}
}

func TestCheckLfedVersion(t *testing.T) {
	err := CheckLfedVersion(">=2", "0.1.0")
	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) {
		t.Fatalf("err = %v, want *IncompatibleError", err)
	}
	if !strings.Contains(err.Error(), ">=2") || !strings.Contains(err.Error(), "0.1.0") {
		t.Errorf("message %q lacks both versions", err.Error())
	}
	if err := CheckLfedVersion(">=0.1.0 <1", "0.1.0"); err != nil {
		t.Errorf("0.1.0 rejected: %v", err)
	}
	if err := CheckLfedVersion(">=0.1.0 <1", "0.1.0-dev"); err != nil {
		t.Errorf("dev build rejected: %v", err)
	}
	if err := CheckLfedVersion(">=2", "dev"); err != nil {
		t.Errorf("non-semver dev build rejected: %v", err)
	}
	if err := CheckLfedVersion("not a range", "0.1.0"); err == nil {
		t.Error("invalid range accepted")
	}
}

func TestInstalled(t *testing.T) {
	dir := setPluginDir(t)
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List = %v, want none", got)
	}

	writeFake(t, dir, "zeta", "Zeta")
	writeFake(t, dir, "alpha", "Alpha")
	got, err = List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Manifest.Name != "alpha" || got[1].Manifest.Name != "zeta" {
		t.Fatalf("List = %+v, want alpha then zeta", got)
	}
}

func installHello(t *testing.T, f *cmdutil.Factory) Installed {
	t.Helper()
	p, err := Install(context.Background(), f, helloBin, Options{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return p
}

func TestInstallFromFile(t *testing.T) {
	dir := setPluginDir(t)
	f, _ := testFactory(t)
	p := installHello(t, f)

	if want := filepath.Join(dir, "hello", "lfed-hello"); p.Binary != want {
		t.Errorf("Binary = %q, want %q", p.Binary, want)
	}
	if p.Source.Type != SourcePath || p.Source.Path != helloBin {
		t.Errorf("Source = %+v", p.Source)
	}
	info, err := os.Stat(p.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("binary mode %v is not executable", info.Mode())
	}
	data, err := os.ReadFile(filepath.Join(dir, "hello", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var m sdkplugin.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != "1" || m.Name != "hello" || m.Version != "0.0.1" || m.Summary == "" {
		t.Errorf("manifest = %+v", m)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".install-") {
			t.Errorf("staging dir %s left behind", e.Name())
		}
	}

	installHello(t, f)
	if _, err := Lookup("hello"); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "hello" {
		t.Errorf("plugin dir after reinstall = %v, want only hello", entries)
	}

	if err := Remove("hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup("hello"); err == nil {
		t.Error("plugin still present after Remove")
	}
}

func TestInstallFromPathRejectsNonExecutable(t *testing.T) {
	setPluginDir(t)
	f, _ := testFactory(t)
	ctx := context.Background()

	_, err := Install(ctx, f, filepath.Dir(helloBin), Options{})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory: err = %v", err)
	}

	plain := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(plain, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Install(ctx, f, plain, Options{})
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Errorf("plain file: err = %v", err)
	}

	script := filepath.Join(t.TempDir(), "lfed-bad")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho not json\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Install(ctx, f, script, Options{})
	if err == nil || !strings.Contains(err.Error(), "__metadata") {
		t.Errorf("bad metadata: err = %v", err)
	}
}

func TestInstallKeepsOldPluginWhenReplaceFails(t *testing.T) {
	dir := setPluginDir(t)
	f, _ := testFactory(t)
	installHello(t, f)
	old, err := os.ReadFile(filepath.Join(dir, "hello", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}

	renameDir = func(from, to string) error {
		if strings.Contains(filepath.Base(from), ".install-") {
			return errors.New("injected rename failure")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { renameDir = os.Rename })

	_, err = Install(context.Background(), f, helloBin, Options{})
	if err == nil || !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "hello", ManifestFile))
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("old install not restored: %v", err)
	}
	if _, err := Lookup("hello"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "hello" {
			t.Errorf("leftover %s in plugin dir", e.Name())
		}
	}
}

func TestInstallRejectsIncompatibleHost(t *testing.T) {
	setPluginDir(t)
	f, _ := testFactory(t)
	f.Version = "5.0.0"
	_, err := Install(context.Background(), f, helloBin, Options{})
	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) {
		t.Fatalf("err = %v, want *IncompatibleError", err)
	}
	if incompatible.Name != "hello" {
		t.Errorf("Name = %q, want hello", incompatible.Name)
	}
}

func TestInstallRejectsReservedName(t *testing.T) {
	setPluginDir(t)
	f, _ := testFactory(t)
	_, err := Install(context.Background(), f, helloBin, Options{Reserved: []string{"auth", "hello"}})
	if err == nil || !strings.Contains(err.Error(), `"hello" is a core lfed command`) {
		t.Fatalf("err = %v", err)
	}
}
