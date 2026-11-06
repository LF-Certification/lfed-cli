//go:build unix

package plugin

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestServeSignalExits130(t *testing.T) {
	if os.Getenv("LFED_SDK_TEST_CHILD") == "1" {
		root := &cobra.Command{Use: "lfed-demo", RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "ready")
			<-cmd.Context().Done()
			return cmd.Context().Err()
		}}
		root.SetOut(os.Stdout)
		Serve(root, Manifest{Name: "demo"})
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestServeSignalExits130$")
	cmd.Env = append(os.Environ(), "LFED_SDK_TEST_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("read ready: %q, %v", line, err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != exitInterrupted {
			t.Fatalf("child: %v, want exit %d", err, exitInterrupted)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit after SIGTERM")
	}
}
