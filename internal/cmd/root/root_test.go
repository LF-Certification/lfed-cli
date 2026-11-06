package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/config"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

var lfedBin string

func TestMain(m *testing.M) {
	goBin, err := exec.LookPath("go")
	if err == nil && runtime.GOOS != "windows" {
		dir, err := os.MkdirTemp("", "lfed-root-test")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		lfedBin = filepath.Join(dir, "lfed")
		build := exec.Command(goBin, "build", "-ldflags", "-X go.ed.linuxfoundation.org/lfed/internal/build.Version=0.1.0", "-o", lfedBin, "../../../cmd/lfed")
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "build lfed:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if lfedBin != "" {
		os.RemoveAll(filepath.Dir(lfedBin))
	}
	os.Exit(code)
}

func newFactory() (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		Version:   "0.1.0",
		Context:   func() (contexts.Context, error) { return contexts.Context{}, contexts.ErrNoContext },
	}
	return f, out, errOut
}

func writePlugin(t *testing.T, dir, name, summary, script string) {
	t.Helper()
	pdir := filepath.Join(dir, name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if script != "" {
		if err := os.WriteFile(filepath.Join(pdir, pluginmgr.BinaryName(name)), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := sdkplugin.Manifest{SchemaVersion: "1", Name: name, Version: "1.0.0", Summary: summary, RequiresLfed: ">=0.1.0 <1"}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(pdir, pluginmgr.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

const echoScript = `#!/bin/sh
if [ "$1" = "__complete" ]; then
  echo "greet	Say hello"
  echo ":4"
  exit 0
fi
env | grep '^LFED_' | sort
echo "args: $*"
`

func installEcho(t *testing.T) string {
	t.Helper()
	if lfedBin == "" {
		t.Skip("lfed binary not built")
	}
	dir := t.TempDir()
	writePlugin(t, dir, "echo", "Echo the contract", echoScript)
	return dir
}

func runLfed(t *testing.T, pluginDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(lfedBin, args...)
	cmd.Env = append(os.Environ(),
		"LFED_PLUGIN_DIR="+pluginDir,
		"LFED_CONFIG_DIR="+t.TempDir(),
		"LFED_CONTEXT=",
		"LFED_OUTPUT=stale",
		"LFED_JQ=stale",
	)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("run lfed: %v", err)
	}
	return out.String(), errOut.String(), cmd.ProcessState.ExitCode()
}

func TestExecForwardsArgsAndContract(t *testing.T) {
	dir := installEcho(t)
	out, errOut, code := runLfed(t, dir, "-o", "json", "--jq", ".x", "echo", "a", "b")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"LFED_CLI_VERSION=",
		"LFED_CONTEXT=\n",
		"LFED_API_BASE_URL=\n",
		"LFED_OUTPUT=json\n",
		"LFED_JQ=.x\n",
		"LFED_REEXEC=" + lfedBin + "\n",
		"LFED_DEBUG=\n",
		"args: a b\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestExecLeavesPluginFlagsAlone(t *testing.T) {
	dir := installEcho(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"echo", "--output", "file.bin"}, "args: --output file.bin\n"},
		{[]string{"echo", "-o", "yaml", "--jq", "."}, "args: -o yaml --jq .\n"},
		{[]string{"echo", "-h"}, "args: -h\n"},
		{[]string{"echo", "--help"}, "args: --help\n"},
		{[]string{"echo", "-c", "prod", "--context=dev"}, "args: -c prod --context=dev\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, errOut, code := runLfed(t, dir, tc.args...)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
			if !strings.Contains(out, "LFED_OUTPUT=table\n") {
				t.Errorf("host consumed a plugin flag:\n%s", out)
			}
		})
	}
}

func TestExecDebugFlag(t *testing.T) {
	dir := installEcho(t)
	cmd := exec.Command(lfedBin, "echo")
	cmd.Env = append(os.Environ(), "LFED_PLUGIN_DIR="+dir, "LFED_CONFIG_DIR="+t.TempDir(), "LFED_CONTEXT=", "LFED_DEBUG=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "LFED_DEBUG=1\n") {
		t.Errorf("output lacks LFED_DEBUG=1:\n%s", out)
	}
}

func TestExecPassesExitCode(t *testing.T) {
	if lfedBin == "" {
		t.Skip("lfed binary not built")
	}
	dir := t.TempDir()
	writePlugin(t, dir, "fail", "Always fails", "#!/bin/sh\nexit 3\n")
	if _, _, code := runLfed(t, dir, "fail"); code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
}

func TestCompletePassesThroughPlugin(t *testing.T) {
	dir := installEcho(t)
	out, _, code := runLfed(t, dir, "__complete", "echo", "gr")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out, "greet\tSay hello\n:4\n") {
		t.Errorf("completion = %q", out)
	}
}

func TestUnknownCommandUsageHint(t *testing.T) {
	if lfedBin == "" {
		t.Skip("lfed binary not built")
	}
	_, errOut, code := runLfed(t, t.TempDir(), "contxt")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	for _, want := range []string{`lfed: unknown command "contxt" for "lfed"`, "Did you mean this?\n\tcontext", "Run 'lfed --help' for usage."} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}

	_, errOut, code = runLfed(t, t.TempDir(), "context", "use")
	if code != 2 || !strings.Contains(errOut, "Run 'lfed context use --help' for usage.") {
		t.Errorf("exit %d, stderr:\n%s", code, errOut)
	}
}

func TestRunUnknownCommandExits2(t *testing.T) {
	t.Setenv("LFED_PLUGIN_DIR", t.TempDir())
	f, _, _ := newFactory()
	err := Run(context.Background(), f, []string{"nosuch", "x"})
	var usage *cmdutil.UsageError
	if !errors.As(err, &usage) || usage.Cmd == nil || !strings.HasPrefix(err.Error(), `unknown command "nosuch" for "lfed"`) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if pluginmgr.ExitCode(err) != 2 {
		t.Errorf("exit = %d, want 2", pluginmgr.ExitCode(err))
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	t.Setenv("LFED_PLUGIN_DIR", t.TempDir())
	tests := []struct {
		args []string
		cmd  string
	}{
		{[]string{"--bogus"}, "lfed"},
		{[]string{"context", "nosuchcmd"}, "lfed context"},
		{[]string{"context", "list", "--bogus"}, "lfed context list"},
		{[]string{"context", "use"}, "lfed context use"},
		{[]string{"-c"}, "lfed"},
		{[]string{"-o", "xml", "version"}, ""},
		{[]string{"-o", "yaml", "--jq", ".", "context", "list"}, ""},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f, _, _ := newFactory()
			err := Run(context.Background(), f, tc.args)
			var usage *cmdutil.UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("err = %v (%T), want *cmdutil.UsageError", err, err)
			}
			if code := pluginmgr.ExitCode(err); code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			got := ""
			if usage.Cmd != nil {
				got = usage.Cmd.CommandPath()
			}
			if got != tc.cmd {
				t.Errorf("Cmd = %q, want %q", got, tc.cmd)
			}
		})
	}
}

func TestVersionFlagMatchesVersionCommand(t *testing.T) {
	ff, flag, _ := newFactory()
	if err := Run(context.Background(), ff, []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	cf, cmd, _ := newFactory()
	if err := Run(context.Background(), cf, []string{"version"}); err != nil {
		t.Fatal(err)
	}
	if flag.String() != cmd.String() || !strings.HasPrefix(flag.String(), "lfed ") {
		t.Fatalf("--version %q, version %q", flag.String(), cmd.String())
	}
}

func TestJQFiltersCoreCommandOutput(t *testing.T) {
	f, out, _ := newFactory()
	if err := Run(context.Background(), f, []string{"context", "list", "--jq", ".[].name"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "dev\nstaging\nprod\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestContextCommandsHonourOutput(t *testing.T) {
	t.Setenv("LFED_CONFIG_DIR", t.TempDir())
	dev, err := contexts.Lookup("dev")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		args   []string
		want   string
		errOut string
	}{
		{name: "current table", args: []string{"context", "current"}, want: "dev\n"},
		{name: "current json", args: []string{"context", "current", "-o", "json"}, want: "{\n  \"name\": \"dev\"\n}\n"},
		{name: "current jq", args: []string{"context", "current", "--jq", ".name"}, want: "dev\n"},
		{name: "use table", args: []string{"context", "use", "staging"}, want: "", errOut: "Switched to context \"staging\".\n"},
		{name: "use json", args: []string{"-o", "json", "context", "use", "prod"}, want: "{\n  \"current_context\": \"prod\"\n}\n"},
		{name: "use jq", args: []string{"context", "use", "dev", "--jq", ".current_context"}, want: "dev\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, out, errOut := newFactory()
			f.Config = config.Load
			f.Context = func() (contexts.Context, error) { return dev, nil }
			if err := Run(context.Background(), f, tc.args); err != nil {
				t.Fatalf("%v: %s", err, errOut.String())
			}
			if out.String() != tc.want {
				t.Errorf("stdout = %q, want %q", out.String(), tc.want)
			}
			if errOut.String() != tc.errOut {
				t.Errorf("stderr = %q, want %q", errOut.String(), tc.errOut)
			}
		})
	}
}

func TestHelpListsPlugins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	writePlugin(t, dir, "placement", "Manage placement reference data", "")

	f, out, _ := newFactory()
	if err := Run(context.Background(), f, []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	cmds := strings.Index(help, "Available Commands:")
	plugins := strings.Index(help, "Plugins:\n  placement")
	flags := strings.Index(help, "Flags:")
	if cmds < 0 || plugins < 0 || flags < 0 || !(cmds < plugins && plugins < flags) {
		t.Fatalf("help sections out of order or missing:\n%s", help)
	}
	if !strings.Contains(help, "Manage placement reference data") {
		t.Errorf("summary missing:\n%s", help)
	}
	if !strings.Contains(help, `(default "table")`) {
		t.Errorf("output flag default missing:\n%s", help)
	}

	f, out, _ = newFactory()
	if err := Run(context.Background(), f, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != help {
		t.Errorf("bare lfed differs from --help:\n%s", out.String())
	}

	f, out, _ = newFactory()
	if err := Run(context.Background(), f, []string{"context", "--help"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Plugins:") {
		t.Errorf("subcommand help lists plugins:\n%s", out.String())
	}
}
