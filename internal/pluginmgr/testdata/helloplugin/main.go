package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.ed.linuxfoundation.org/lfed/sdk/plugin"
)

var manifest = plugin.Manifest{
	Name:         "hello",
	Version:      "0.0.1",
	Summary:      "Test plugin for pluginmgr",
	RequiresLfed: ">=0.1.0 <1",
}

func main() {
	root := &cobra.Command{
		Use:  "lfed-hello",
		Args: cobra.ArbitraryArgs,
		RunE: greet,
	}
	root.AddCommand(
		&cobra.Command{Use: "fail", Run: func(*cobra.Command, []string) { os.Exit(3) }},
		&cobra.Command{Use: "kill", Run: func(*cobra.Command, []string) {
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Kill()
			time.Sleep(time.Hour)
		}},
		&cobra.Command{Use: "wait", Run: func(*cobra.Command, []string) {
			fmt.Println("ready")
			time.Sleep(time.Hour)
		}},
	)
	plugin.Serve(root, manifest)
}

func greet(cmd *cobra.Command, args []string) error {
	p, err := plugin.Printer(cmd)
	if err != nil {
		return err
	}
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LFED_") {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	if p.Mode.Structured() {
		return p.PrintValue(map[string]any{"env": env, "args": args})
	}
	for _, kv := range env {
		fmt.Fprintln(p.Out, kv)
	}
	fmt.Fprintln(p.Out, "args:", strings.Join(args, " "))
	return nil
}
