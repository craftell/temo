package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

func main() {
	base, err := os.UserConfigDir()
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	dir := flag.String("config-dir", filepath.Join(base, "temo"), "private directory for definitions and ownership")
	flag.Parse()
	if err := run(*dir); err != nil { fmt.Fprintln(os.Stderr, "temo:", safeText(err.Error())); os.Exit(1) }
}

func run(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil { return err }
	store, err := OpenStore(absolute)
	if err != nil { return err }
	defer store.Close()
	config, err := store.Load()
	if err != nil { return err }
	engine := &Engine{Store: store, Runner: ExecRunner{}}
	_, err = tea.NewProgram(NewModel(config, store, engine)).Run()
	return err
}
