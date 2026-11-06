package pluginmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/contexts"
	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
	"go.ed.linuxfoundation.org/lfed/sdk/output"
	sdkplugin "go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

const execHelperEnv = "LFED_TEST_EXEC_HELPER"

func execFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios, Version: "0.1.0", OutputFlag: "table"}
	f.Context = func() (contexts.Context, error) { return contexts.Lookup("staging") }
	f.Token = func(context.Context) (string, error) {
		t.Fatal("plugin dispatch resolved the token")
		return "", nil
	}
	f.Executable = func() (string, error) { return "/opt/lfed/bin/lfed", nil }
	return f, out, errOut
}

func placeHello(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LFED_PLUGIN_DIR", dir)
	pdir := filepath.Join(dir, "hello")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(helloBin)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(pdir, BinaryName("hello")), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	m := sdkplugin.Manifest{SchemaVersion: "1", Name: "hello", Version: "0.0.1", Summary: "Test plugin", RequiresLfed: ">=0.1.0 <1"}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(pdir, ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnv(t *testing.T) {
	c, err := contexts.Lookup("dev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.Audience, "/") {
		t.Fatalf("test needs an audience with a trailing slash, got %q", c.Audience)
	}
	cases := []struct {
		name string
		spec EnvSpec
		want []string
	}{
		{
			name: "context",
			spec: EnvSpec{LfedVersion: "0.1.0", Context: c, HasContext: true, Mode: output.JSON, JQ: ".name", Reexec: "/usr/bin/lfed", Debug: true},
			want: []string{
				"LFED_CLI_VERSION=0.1.0",
				"LFED_CONTEXT=dev",
				"LFED_API_BASE_URL=" + strings.TrimSuffix(c.Audience, "/"),
				"LFED_OUTPUT=json",
				"LFED_JQ=.name",
				"LFED_REEXEC=/usr/bin/lfed",
				"LFED_DEBUG=1",
			},
		},
		{
			name: "no context",
			spec: EnvSpec{LfedVersion: "0.1.0", Mode: output.Table, Reexec: "/usr/bin/lfed"},
			want: []string{
				"LFED_CLI_VERSION=0.1.0",
				"LFED_CONTEXT=",
				"LFED_API_BASE_URL=",
				"LFED_OUTPUT=table",
				"LFED_JQ=",
				"LFED_REEXEC=/usr/bin/lfed",
				"LFED_DEBUG=",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Env(tc.spec); !slices.Equal(got, tc.want) {
				t.Errorf("Env = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExitCodeUsageError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &cmdutil.UsageError{Err: errors.New("unknown flag")})
	if ExitCode(err) != 2 {
		t.Errorf("ExitCode = %d, want 2", ExitCode(err))
	}
}

func TestExecHelper(t *testing.T) {
	mode := os.Getenv(execHelperEnv)
	if mode == "" {
		t.Skip("helper process")
	}
	f, _, _ := execFactory(t)
	f.IOStreams = iostreams.System()
	if mode == "nocontext" {
		f.Context = func() (contexts.Context, error) { return contexts.Context{}, contexts.ErrNoContext }
	}
	p, err := Lookup("hello")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	args := strings.Fields(os.Getenv("LFED_TEST_EXEC_ARGS"))
	err = Exec(context.Background(), f, p, args)
	fmt.Fprintln(os.Stderr, "Exec returned:", err)
	os.Exit(98)
}

func runExecHelper(t *testing.T, mode string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecHelper$")
	cmd.Env = append(os.Environ(), execHelperEnv+"="+mode, "LFED_TEST_EXEC_ARGS="+strings.Join(args, " "))
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("helper: %v", err)
	}
	if strings.Contains(errOut.String(), "Exec returned") {
		t.Fatalf("Exec returned instead of replacing the process:\n%s", errOut.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

func TestExecReplacesProcessWithContractEnv(t *testing.T) {
	placeHello(t)
	t.Setenv("LFED_TOKEN", "secret")

	out, code := runExecHelper(t, "context", "greet", "world")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"LFED_CLI_VERSION=0.1.0\n",
		"LFED_CONTEXT=staging\n",
		"LFED_API_BASE_URL=https://api.staging.ed.linuxfoundation.org\n",
		"LFED_OUTPUT=table\n",
		"LFED_JQ=\n",
		"LFED_REEXEC=/opt/lfed/bin/lfed\n",
		"LFED_DEBUG=\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "args:") || !strings.Contains(out, "world") {
		t.Errorf("plugin did not receive its args:\n%s", out)
	}

	out, code = runExecHelper(t, "nocontext", "greet")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{"LFED_CONTEXT=\n", "LFED_API_BASE_URL=\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if _, code = runExecHelper(t, "context", "fail"); code != 3 {
		t.Errorf("fail: exit %d, want 3", code)
	}
}

func TestRunRejectsIncompatibleHost(t *testing.T) {
	placeHello(t)
	f, _, _ := execFactory(t)
	f.Version = "9.0.0"
	err := Run(context.Background(), f, "hello", nil)
	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) || ExitCode(err) != 2 {
		t.Errorf("Run with lfed CLI 9.0.0: err = %v, want IncompatibleError (exit 2)", err)
	}
}

func TestExecFailsOnContextError(t *testing.T) {
	placeHello(t)
	f, _, _ := execFactory(t)
	boom := errors.New("config unreadable")
	f.Context = func() (contexts.Context, error) { return contexts.Context{}, boom }
	p, err := Lookup("hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := Exec(context.Background(), f, p, nil); !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestCompletePassesThrough(t *testing.T) {
	placeHello(t)
	f, out, _ := execFactory(t)
	words, directive, err := Complete(context.Background(), f, "hello", []string{""})
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("completion leaked to stdout: %q", out.String())
	}
	if !slices.Contains(words, "fail") {
		t.Errorf("words = %q, want fail", words)
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Errorf("directive = %d, want NoFileComp set", directive)
	}
}

func TestParseCompletion(t *testing.T) {
	cases := []struct {
		in        string
		words     []string
		directive cobra.ShellCompDirective
	}{
		{"greet\tSay hi\nfail\n:4\n", []string{"greet\tSay hi", "fail"}, cobra.ShellCompDirectiveNoFileComp},
		{":1\n", []string{}, cobra.ShellCompDirectiveError},
		{"", []string{}, cobra.ShellCompDirectiveDefault},
		{"a\nb", []string{"a", "b"}, cobra.ShellCompDirectiveDefault},
	}
	for _, tc := range cases {
		words, directive := parseCompletion(tc.in)
		if !slices.Equal(words, tc.words) || directive != tc.directive {
			t.Errorf("parseCompletion(%q) = %q, %d; want %q, %d", tc.in, words, directive, tc.words, tc.directive)
		}
	}
}
