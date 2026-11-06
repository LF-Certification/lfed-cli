package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.ed.linuxfoundation.org/lfed/internal/build"
	"go.ed.linuxfoundation.org/lfed/internal/cmd/root"
	"go.ed.linuxfoundation.org/lfed/internal/cmdutil"
	"go.ed.linuxfoundation.org/lfed/internal/pluginmgr"
)

const exitInterrupted = 130

func main() {
	os.Exit(run())
}

func run() int {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	f := cmdutil.New(build.Version)
	f.Token = cmdutil.TokenResolver(f)
	err := root.Run(ctx, f, os.Args[1:])
	if err == nil {
		return 0
	}

	var exit *pluginmgr.ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	if ctx.Err() != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintf(f.IOStreams.ErrOut, "lfed: %v\n", err)
		}
		return exitInterrupted
	}
	fmt.Fprintf(f.IOStreams.ErrOut, "lfed: %v\n", err)
	var usage *cmdutil.UsageError
	if errors.As(err, &usage) && usage.Cmd != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "Run 'lfed %s--help' for usage.\n", strings.TrimPrefix(usage.Cmd.CommandPath()+" ", "lfed "))
	}
	return pluginmgr.ExitCode(err)
}
