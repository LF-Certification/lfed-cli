package main

import (
	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

// name is set with -ldflags "-X main.name=..." so one source builds several plugins.
var name = "named"

func main() {
	plugin.Serve(&cobra.Command{Use: "lfed-" + name}, plugin.Manifest{
		Name:         name,
		Version:      "0.0.1",
		Summary:      "Test plugin for lfed plugin install",
		RequiresLfed: ">=0.1.0 <1",
	})
}
