package plugin

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.ed.linuxfoundation.org/lfed/sdk/iostreams"
)

func Confirm(ios *iostreams.IOStreams, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !ios.IsStdinTTY() {
		return errors.New("confirmation required; re-run with --yes")
	}
	fmt.Fprintf(ios.ErrOut, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(ios.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("aborted")
}
