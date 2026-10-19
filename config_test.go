package main

import (
	"os"
	"path/filepath"
	"testing"
)

func exampleSSH() Entry {
	return Entry{ID: "0123456789abcdef", Name: "dev", Kind: "ssh", SSHHost: "my-server", TargetHost: "127.0.0.1", LocalPort: 8080, TargetPort: 3000}
}
func exampleServe() Entry {
	e := exampleSSH()
	e.Kind, e.ServePort, e.SSHHost = "serve", 8443, ""
	return e
}
func testStore(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp("", "temo-")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := OpenStore(dir)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { s.Close() })
	return s
}
func TestConfigRoundTripAndLock(t *testing.T) {
	s := testStore(t)
	c := Config{Version: 1, Entries: []Entry{exampleSSH()}}
	if err := s.Save(c); err != nil { t.Fatal(err) }
	got, err := s.Load()
	if err != nil || len(got.Entries) != 1 || got.Entries[0] != c.Entries[0] { t.Fatalf("roundtrip: %#v %v", got, err) }
	info, err := os.Stat(filepath.Join(s.Dir, "config.json"))
	if err != nil || info.Mode().Perm() != 0600 { t.Fatalf("private config: %v %v", info, err) }
	if other, err := OpenStore(s.Dir); err == nil { other.Close(); t.Fatal("second writer accepted") }
}
func TestInvalidDefinitions(t *testing.T) {
	tests := []struct { name string; change func(*Entry) }{
		{"argument injection", func(e *Entry) { e.SSHHost = "-oProxyCommand=evil" }},
		{"shell injection", func(e *Entry) { e.SSHHost = "host;evil" }},
		{"zero port", func(e *Entry) { e.LocalPort = 0 }},
		{"path ID", func(e *Entry) { e.ID = "../owner" }},
		{"control name", func(e *Entry) { e.Name = "bad\x1b[2J" }},
		{"unknown kind", func(e *Entry) { e.Kind = "funnel" }},
	}
	for _, tt := range tests { t.Run(tt.name, func(t *testing.T) { e := exampleSSH(); tt.change(&e); if e.Validate() == nil { t.Fatal("invalid definition accepted") } }) }
	e := exampleServe(); e.TargetHost = "example.com"
	if e.Validate() == nil { t.Fatal("non-local Serve target accepted") }
	c := Config{Version: 1, Entries: []Entry{exampleSSH(), exampleSSH()}}
	if c.Validate() == nil { t.Fatal("duplicate accepted") }
	c.Version = 2
	if c.Validate() == nil { t.Fatal("future schema accepted") }
}
func TestMalformedConfigFailsClosed(t *testing.T) {
	s := testStore(t)
	for _, raw := range []string{`{"version":1,"entries":[],"secret":"x"}`, `{"version":1} {}`, `{"version":2,"entries":[]}`} {
		if err := os.WriteFile(filepath.Join(s.Dir, "config.json"), []byte(raw), 0600); err != nil { t.Fatal(err) }
		if _, err := s.Load(); err == nil { t.Fatalf("accepted %s", raw) }
	}
}
