package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		if err := runUpdateCLI(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "temo:", safeText(err.Error()))
			os.Exit(1)
		}
		return
	}
	base, configErr := os.UserConfigDir()
	dir := flag.String("config-dir", filepath.Join(base, "temo"), "private directory for definitions and ownership")
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Parse()
	if showVersion {
		fmt.Println("temo", buildVersion())
		return
	}
	if configErr != nil {
		fmt.Fprintln(os.Stderr, configErr)
		os.Exit(1)
	}
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "temo:", safeText(err.Error()))
		os.Exit(1)
	}
}

func run(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	store, err := OpenStore(absolute)
	if err != nil {
		return err
	}
	defer store.Close()
	config, err := store.Load()
	if err != nil {
		return err
	}
	engine := &Engine{Store: store, Runner: ExecRunner{}}
	_, err = tea.NewProgram(NewModel(config, store, engine)).Run()
	return err
}
