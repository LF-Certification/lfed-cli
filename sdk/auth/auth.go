package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func Token(ctx context.Context) (string, error) {
	exe := os.Getenv("LFED_REEXEC")
	if exe == "" {
		exe = "lfed"
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "auth", "token")
	cmd.Stdin = nil
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimPrefix(strings.TrimSpace(stderr.String()), "lfed: ")
			if msg == "" {
				msg = fmt.Sprintf("%s auth token: %s", exe, exitErr)
			}
			return "", errors.New(msg)
		}
		return "", fmt.Errorf("run %s auth token: %w", exe, err)
	}
	tok := strings.TrimSpace(stdout.String())
	if tok == "" {
		return "", fmt.Errorf("%s auth token printed no token; run \"lfed auth login\"", exe)
	}
	return tok, nil
}
