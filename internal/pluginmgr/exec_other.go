//go:build !unix

package pluginmgr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func execPlugin(bin string, args, env []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if code < 0 {
			code = 1
		}
		return &ExitError{Code: code}
	}
	if err != nil {
		return fmt.Errorf("run %s: %w", bin, err)
	}
	return nil
}
