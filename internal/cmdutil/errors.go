package cmdutil

import "github.com/spf13/cobra"

type UsageError struct {
	Err error
	Cmd *cobra.Command
}

func (e *UsageError) Error() string { return e.Err.Error() }

func (e *UsageError) Unwrap() error { return e.Err }
