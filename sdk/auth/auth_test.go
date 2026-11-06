package auth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires /bin/sh")
	}
	p := filepath.Join(t.TempDir(), "fake-lfed")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTokenReexecsHost(t *testing.T) {
	t.Setenv("LFED_REEXEC", writeScript(t, `[ "$1 $2" = "auth token" ] || exit 9; printf '  tok-123\n'`))
	tok, err := Token(context.Background())
	if err != nil || tok != "tok-123" {
		t.Fatalf("got %q, %v", tok, err)
	}
}

func TestTokenReportsChildStderr(t *testing.T) {
	t.Setenv("LFED_REEXEC", writeScript(t, `echo 'lfed: not logged in; run "lfed auth login"' >&2; exit 1`))
	_, err := Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), `run "lfed auth login"`) {
		t.Fatalf("got %v", err)
	}
}

func TestTokenMissingBinary(t *testing.T) {
	t.Setenv("LFED_REEXEC", filepath.Join(t.TempDir(), "missing"))
	if _, err := Token(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
