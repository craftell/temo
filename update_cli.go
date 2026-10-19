package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

func runUpdateCLI(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("temo update", flag.ContinueOnError)
	flags.SetOutput(out)
	check := flags.Bool("check", false, "check for a stable release without changing files")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: temo update [--check]")
	}
	result, err := newUpdater().Run(context.Background(), *check)
	if err != nil {
		return err
	}
	switch {
	case result.Updated:
		fmt.Fprintf(out, "Updated temo %s → %s.\n", result.Current, result.Latest)
	case result.CurrentUnknown:
		fmt.Fprintf(out, "Current build: %s. Latest stable release: %s.\n", result.Current, result.Latest)
	case result.Available:
		fmt.Fprintf(out, "Update available: %s → %s. Run temo update to install.\n", result.Current, result.Latest)
	default:
		fmt.Fprintf(out, "temo %s is up to date (latest stable: %s).\n", result.Current, result.Latest)
	}
	return nil
}
