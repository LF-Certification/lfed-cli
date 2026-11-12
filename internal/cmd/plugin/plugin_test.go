package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/config"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

func writeFake(t *testing.T, dir, name, version, summary string) {
	t.Helper()
	writeFakeRequiring(t, dir, name, version, summary, ">=0.1.0 <1")
}

func writeFakeRequiring(t *testing.T, dir, name, version, summary, requires string) {
	t.Helper()
	pdir := filepath.Join(dir, name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, pluginmgr.BinaryName(name)), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := sdkplugin.Manifest{SchemaVersion: "1", Name: name, Version: version, Summary: summary, RequiresLfed: requires}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(pdir, pluginmgr.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFactory(outputFlag string) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	ios, _, out, errOut := iostreams.Test()
	return &cmdutil.Factory{IOStreams: ios, Version: "0.1.0", OutputFlag: outputFlag}, out, errOut
}

func execute(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	return executeContext(context.Background(), t, f, args...)
}

func executeContext(ctx context.Context, t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := NewCmd(f)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	cmd.SetOut(f.IOStreams.Out)
	cmd.SetErr(f.IOStreams.ErrOut)
	return cmd.ExecuteContext(ctx)
}

func run(t *testing.T, outputFlag string, args ...string) string {
	t.Helper()
	f, out, _ := newFactory(outputFlag)
	if err := execute(t, f, args...); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out.String()
}

func TestListEmpty(t *testing.T) {
	t.Setenv("LFED_PLUGIN_DIR", t.TempDir())
	got := run(t, "table", "list")
	if strings.TrimSpace(got) != "NAME  VERSION  STATUS  CATALOG  SOURCE  SUMMARY" {
		t.Errorf("table = %q", got)
	}
	got = run(t, "json", "list")
	if strings.TrimSpace(got) != "[]" {
		t.Errorf("json = %q", got)
	}
}

func TestListTwo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeFakeRequiring(t, dir, "placement", "0.3.0", "Placement reference data", ">=9.0.0")
	writeFake(t, dir, "hello", "0.0.1", "Say hello")

	got := run(t, "table", "list")
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("table = %q", got)
	}
	if !strings.HasPrefix(lines[1], "hello ") || !strings.Contains(lines[1], "0.0.1") || !strings.Contains(lines[1], " ok ") || !strings.Contains(lines[1], "Say hello") {
		t.Errorf("row 1 = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "placement ") || !strings.Contains(lines[2], "needs lfed >=9.0.0") {
		t.Errorf("row 2 = %q", lines[2])
	}

	var rows []struct {
		sdkplugin.Manifest
		Compatible bool `json:"compatible"`
	}
	if err := json.Unmarshal([]byte(run(t, "json", "list")), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "hello" || !rows[0].Compatible || rows[1].RequiresLfed != ">=9.0.0" || rows[1].Compatible {
		t.Errorf("json = %+v", rows)
	}

	f, out, _ := newFactory("table")
	f.JQ = ".[].name"
	if err := execute(t, f, "list"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\nplacement\n" {
		t.Errorf("jq output = %q", out.String())
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeFake(t, dir, "hello", "0.0.1", "Say hello")
	writeFake(t, dir, "bye", "0.0.1", "Say bye")

	f, out, errOut := newFactory("table")
	if err := execute(t, f, "remove", "hello"); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("non-TTY output: stdout %q, stderr %q", out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "hello")); !os.IsNotExist(err) {
		t.Errorf("plugin dir still exists: %v", err)
	}

	f, out, _ = newFactory("table")
	f.IOStreams.SetStdoutTTY(true)
	if err := execute(t, f, "remove", "bye"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed plugin bye") {
		t.Errorf("TTY output = %q", out.String())
	}

	f, _, _ = newFactory("table")
	err := execute(t, f, "remove", "nosuch")
	var notFound *pluginmgr.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("remove nosuch: err = %v, want *NotFoundError", err)
	}
}

func TestRemoveRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a", "plugins")
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeFake(t, dir, "hello", "0.0.1", "Say hello")
	for _, name := range []string{"..", "../..", "a/../..", "/"} {
		f, _, _ := newFactory("table")
		err := execute(t, f, "remove", name)
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("remove %q: err = %v, want unknown command", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "hello", pluginmgr.ManifestFile)); err != nil {
		t.Fatalf("plugin removed: %v", err)
	}
}

func TestUpgradeReportsFailures(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writeFake(t, dir, "hello", "0.0.1", "Say hello")

	f, out, _ := newFactory("json")
	err := execute(t, f, "upgrade", "nosuch", "hello")
	var notFound *pluginmgr.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
	var results []pluginmgr.UpgradeResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("stdout %q: %v", out.String(), err)
	}
	if len(results) != 2 || results[0].Name != "nosuch" || results[0].Error == "" || results[1].Name != "hello" || results[1].Error != "" {
		t.Errorf("results = %+v", results)
	}

	f, out, _ = newFactory("table")
	_ = execute(t, f, "upgrade", "nosuch", "hello")
	if !strings.Contains(out.String(), "failed: ") || !strings.Contains(out.String(), "source unknown") {
		t.Errorf("table = %q", out.String())
	}
}

func TestGroupWithoutSubcommandPrintsHelp(t *testing.T) {
	f, out, _ := newFactory("table")
	if err := execute(t, f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Available Commands") {
		t.Errorf("output = %q, want help", out.String())
	}
}

func TestCatalogAddListRemove(t *testing.T) {
	t.Setenv("LFED_CONFIG_DIR", t.TempDir())
	f, out, _ := newFactory("table")
	f.Config = config.Load

	cases := []struct {
		args []string
		err  string
	}{
		{[]string{"catalog", "add", "Acme", "ghcr.io/acme/catalog:latest"}, "must match"},
		{[]string{"catalog", "add", "localhost", "ghcr.io/acme/catalog:latest"}, "reserved"},
		{[]string{"catalog", "add", "acme", "ghcr.io/acme/catalog"}, "needs a tag or digest"},
		{[]string{"catalog", "add", "acme", "ghcr.io/acme/catalog:latest"}, "needs --signer-issuer"},
		{[]string{"catalog", "add", "acme", "ghcr.io/acme/catalog:latest", "--signer-issuer", "https://i"}, "together"},
		{[]string{"catalog", "remove", "acme"}, `no plugin catalog "acme"`},
	}
	for _, c := range cases {
		if err := execute(t, f, c.args...); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%v: err = %v, want %q", c.args, err, c.err)
		}
	}

	if err := execute(t, f, "catalog", "add", "acme", "ghcr.io/acme/catalog:latest", "--signer-issuer", "https://i", "--signer-identity", "https://id"); err != nil {
		t.Fatal(err)
	}
	if err := execute(t, f, "catalog", "add", "team", "./team.json"); err != nil {
		t.Fatal(err)
	}
	if err := execute(t, f, "catalog", "add", "acme", "ghcr.io/other/catalog:latest", "--signer-issuer", "https://i", "--signer-identity", "https://id"); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("duplicate alias: err = %v", err)
	}
	cfg, err := config.Load()
	if err != nil || len(cfg.Catalogs) != 2 || cfg.Catalogs["acme"].Signer.Identity != "https://id" || cfg.Catalogs["team"].Ref != "./team.json" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}

	out.Reset()
	if err := execute(t, f, "catalog", "list"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "default ") || !strings.Contains(lines[1], pluginmgr.DefaultCatalog) ||
		!strings.HasPrefix(lines[2], "acme ") || !strings.Contains(lines[2], "https://id") ||
		!strings.HasPrefix(lines[3], "team ") || !strings.HasSuffix(strings.TrimSpace(lines[3]), "-") {
		t.Errorf("list = %q", out.String())
	}

	if err := execute(t, f, "catalog", "remove", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := execute(t, f, "catalog", "remove", "team"); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil || cfg.Catalogs != nil {
		t.Errorf("config after remove = %+v, %v", cfg, err)
	}
}

func buildPlugin(t *testing.T, name string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "lfed-"+name)
	build := exec.Command("go", "build", "-ldflags", "-X main.name="+name, "-o", bin, "./testdata/namedplugin")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return bin
}

func installed(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name, pluginmgr.ManifestFile))
	return err == nil
}

func TestInstallSeveral(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	alpha, beta := buildPlugin(t, "alpha"), buildPlugin(t, "beta")

	f, _, _ := newFactory("table")
	if err := execute(t, f, "install", alpha, beta); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !installed(t, dir, name) {
			t.Errorf("%s not installed", name)
		}
	}
}

func TestInstallContinuesPastFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	alpha, beta := buildPlugin(t, "alpha"), buildPlugin(t, "beta")
	bogus := filepath.Join(t.TempDir(), "lfed-missing")

	f, _, _ := newFactory("table")
	err := execute(t, f, "install", alpha, bogus, beta)
	if err == nil || !strings.HasPrefix(err.Error(), bogus+": ") {
		t.Fatalf("err = %v, want prefix %q", err, bogus+": ")
	}
	for _, name := range []string{"alpha", "beta"} {
		if !installed(t, dir, name) {
			t.Errorf("%s not installed", name)
		}
	}
}

func TestInstallOneFailureKeepsBareError(t *testing.T) {
	t.Setenv("LFED_PLUGIN_DIR", t.TempDir())
	bogus := filepath.Join(t.TempDir(), "lfed-missing")

	f, _, _ := newFactory("table")
	err := execute(t, f, "install", bogus)
	_, want := pluginmgr.Install(context.Background(), f, bogus, pluginmgr.Options{})
	if err == nil || want == nil || err.Error() != want.Error() {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestInstallRequiresArgument(t *testing.T) {
	t.Setenv("LFED_PLUGIN_DIR", t.TempDir())
	f, _, _ := newFactory("table")
	if err := execute(t, f, "install"); err == nil {
		t.Error("install with no argument succeeded")
	}
}

func TestInstallStopsWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	alpha, beta := buildPlugin(t, "alpha"), buildPlugin(t, "beta")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f, _, _ := newFactory("table")
	err := executeContext(ctx, t, f, "install", alpha, beta)
	if err != context.Canceled {
		t.Errorf("err = %v, want only %v", err, context.Canceled)
	}
	if installed(t, dir, "alpha") || installed(t, dir, "beta") {
		t.Error("a plugin was installed after cancellation")
	}
}

func TestInstallKeepsEarlierFailuresWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	src := t.TempDir()
	bogus := filepath.Join(src, "lfed-missing")
	started := filepath.Join(src, "started")
	blocker := filepath.Join(src, "lfed-blocker")
	t.Setenv("LFED_TEST_STARTED", started)
	script := "#!/bin/sh\n: > \"$LFED_TEST_STARTED\"\nexec sleep 60\n"
	if err := os.WriteFile(blocker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	beta := buildPlugin(t, "beta")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	f, _, _ := newFactory("table")
	err := executeContext(ctx, t, f, "install", bogus, blocker, beta)
	if err == nil || !strings.HasPrefix(err.Error(), bogus+": ") || strings.Contains(err.Error(), blocker) {
		t.Errorf("err = %v, want only the failure of %s", err, bogus)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("err wraps context.Canceled, so main would print nothing: %v", err)
	}
	if installed(t, dir, "beta") {
		t.Error("beta was installed after cancellation")
	}
}
