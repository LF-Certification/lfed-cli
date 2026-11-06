package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/sdk/output"
)

func tree() *cobra.Command {
	root := &cobra.Command{Use: "lfed-demo"}
	targets := &cobra.Command{Use: "targets", Short: "Targets"}
	targets.AddCommand(
		&cobra.Command{Use: "list", Short: "List targets", RunE: func(*cobra.Command, []string) error { return nil }},
		&cobra.Command{Use: "secret", Hidden: true, RunE: func(*cobra.Command, []string) error { return nil }},
	)
	root.AddCommand(targets, &cobra.Command{Use: "capabilities", Short: "Caps", RunE: func(*cobra.Command, []string) error { return nil }})
	return root
}

func TestMetadataPrintsManifest(t *testing.T) {
	root := tree()
	var out bytes.Buffer
	root.SetOut(&out)
	in := Manifest{Name: "demo", Version: "0.1.0", RequiresLfed: ">=0.1.0 <1", Summary: "Demo"}
	if code := run(context.Background(), root, in, []string{"__metadata"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var m Manifest
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	in.SchemaVersion = ManifestSchemaVersion
	if m != in {
		t.Errorf("manifest %+v, want %+v", m, in)
	}
	for _, key := range []string{"commands", "sdk_version"} {
		if strings.Contains(out.String(), key) {
			t.Errorf("manifest leaks %q:\n%s", key, out.String())
		}
	}
}

func TestPreparePersistentFlags(t *testing.T) {
	root := tree()
	Prepare(root, Manifest{Name: "demo"})
	for _, name := range []string{"output", "jq"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("flag %q missing", name)
		}
	}
	for _, name := range []string{"context", "yes", "insecure-storage"} {
		if root.PersistentFlags().Lookup(name) != nil {
			t.Errorf("flag %q should not exist", name)
		}
	}
}

func TestPrepareKeepsPluginFlags(t *testing.T) {
	root := tree()
	root.PersistentFlags().StringP("output", "o", "yaml", "custom")
	root.PersistentFlags().String("jq", ".custom", "custom")
	Prepare(root, Manifest{Name: "demo"})
	if got := root.PersistentFlags().Lookup("output").DefValue; got != "yaml" {
		t.Errorf("output default %q", got)
	}
	if got := root.PersistentFlags().Lookup("jq").DefValue; got != ".custom" {
		t.Errorf("jq default %q", got)
	}
}

func TestFlagErrorExits2(t *testing.T) {
	for _, reexec := range []string{"", "/usr/local/bin/lfed"} {
		t.Setenv("LFED_REEXEC", reexec)
		root := tree()
		var errOut bytes.Buffer
		root.SetErr(&errOut)
		code := run(context.Background(), root, Manifest{Name: "demo"}, []string{"targets", "list", "--bogus"})
		if code != 2 || !strings.Contains(errOut.String(), "unknown flag: --bogus") {
			t.Fatalf("LFED_REEXEC=%q: exit %d stderr %q", reexec, code, errOut.String())
		}
	}
}

func TestCanceledRunExits130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := tree()
	root.SetErr(&bytes.Buffer{})
	root.AddCommand(&cobra.Command{Use: "wait", RunE: func(cmd *cobra.Command, _ []string) error {
		<-cmd.Context().Done()
		return cmd.Context().Err()
	}})
	if code := run(ctx, root, Manifest{Name: "demo"}, []string{"wait"}); code != 130 {
		t.Fatalf("exit %d, want 130", code)
	}
}

func TestOutputFlagDefaultsFromEnv(t *testing.T) {
	t.Setenv("LFED_OUTPUT", "json")
	var seen output.Printer
	if code := run(context.Background(), probeTree(&seen), Manifest{Name: "demo"}, []string{"probe"}); code != 0 || seen.Mode != output.JSON {
		t.Fatalf("env default: exit %d mode %q", code, seen.Mode)
	}
	if code := run(context.Background(), probeTree(&seen), Manifest{Name: "demo"}, []string{"probe", "-o", "table"}); code != 0 || seen.Mode != output.Table {
		t.Fatalf("flag override: exit %d mode %q", code, seen.Mode)
	}
	if code := run(context.Background(), probeTree(&seen), Manifest{Name: "demo"}, []string{"probe", "-o", "xml"}); code != 1 {
		t.Fatalf("invalid mode accepted: exit %d", code)
	}
}

func TestInvalidOutputEnvFallsBackToTable(t *testing.T) {
	t.Setenv("LFED_OUTPUT", "xml")
	var seen output.Printer
	if code := run(context.Background(), probeTree(&seen), Manifest{Name: "demo"}, []string{"probe"}); code != 0 || seen.Mode != output.Table {
		t.Fatalf("exit %d mode %q", code, seen.Mode)
	}
}

func TestPrinterJQImpliesJSON(t *testing.T) {
	t.Setenv("LFED_JQ", "")
	var seen output.Printer
	if code := run(context.Background(), probeTree(&seen), Manifest{Name: "demo"}, []string{"probe", "--jq", ".x"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if seen.Mode != output.JSON || seen.JQ != ".x" {
		t.Fatalf("printer %+v", seen)
	}

	t.Setenv("LFED_JQ", ".env")
	if code := run(context.Background(), probeTree(&seen), Manifest{Name: "demo"}, []string{"probe"}); code != 0 {
		t.Fatalf("env: exit %d", code)
	}
	if seen.Mode != output.JSON || seen.JQ != ".env" {
		t.Fatalf("env printer %+v", seen)
	}
}

func TestPrinterJQWithYAMLIsUsageError(t *testing.T) {
	var seen output.Printer
	root := probeTree(&seen)
	var errOut bytes.Buffer
	root.SetErr(&errOut)
	code := run(context.Background(), root, Manifest{Name: "demo"}, []string{"probe", "-o", "yaml", "--jq", ".x"})
	if code != 2 || !strings.Contains(errOut.String(), "--jq cannot be combined with -o yaml") {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
}

func TestPrinterWritesToCommandOut(t *testing.T) {
	var seen output.Printer
	root := probeTree(&seen)
	var out bytes.Buffer
	root.SetOut(&out)
	if code := run(context.Background(), root, Manifest{Name: "demo"}, []string{"probe"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if seen.Out != &out {
		t.Fatalf("printer out %T, want command out", seen.Out)
	}
}

func probeTree(seen *output.Printer) *cobra.Command {
	root := tree()
	root.SetErr(&bytes.Buffer{})
	root.AddCommand(&cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		p, err := Printer(cmd)
		*seen = p
		return err
	}})
	return root
}

func TestRunPrintsPrefixedErrorAndExits1(t *testing.T) {
	root := tree()
	var errOut bytes.Buffer
	root.SetErr(&errOut)
	code := run(context.Background(), root, Manifest{Name: "demo"}, []string{"nosuch"})
	if code != 1 || !strings.HasPrefix(errOut.String(), "lfed-demo: ") {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
}

func TestUnderLfedUsageAndErrorsReadLfedName(t *testing.T) {
	t.Setenv("LFED_REEXEC", "/usr/local/bin/lfed")
	root := tree()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	if code := run(context.Background(), root, Manifest{Name: "demo"}, []string{"targets", "list", "--help"}); code != 0 {
		t.Fatalf("help exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "lfed demo targets list") {
		t.Fatalf("usage does not read as typed:\n%s", out.String())
	}
	if strings.Contains(out.String(), "lfed-demo") {
		t.Fatalf("binary name leaked into help:\n%s", out.String())
	}

	root = tree()
	root.SetErr(&errOut)
	errOut.Reset()
	if code := run(context.Background(), root, Manifest{Name: "demo"}, []string{"nosuch"}); code != 1 || !strings.HasPrefix(errOut.String(), "lfed demo: ") {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}

	root = tree()
	out.Reset()
	root.SetOut(&out)
	if code := run(context.Background(), root, Manifest{Name: "demo"}, []string{cobra.ShellCompNoDescRequestCmd, "targets", ""}); code != 0 {
		t.Fatalf("completion exit %d", code)
	}
	if !strings.Contains(out.String(), "list") {
		t.Fatalf("completion lost the subtree:\n%s", out.String())
	}
}
