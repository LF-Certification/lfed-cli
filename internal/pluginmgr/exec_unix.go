//go:build unix

package pluginmgr

import (
	"fmt"
	"syscall"
)

func execPlugin(bin string, args, env []string) error {
	err := syscall.Exec(bin, append([]string{bin}, args...), env)
	return fmt.Errorf("run %s: %w", bin, err)
}
