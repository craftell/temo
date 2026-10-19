package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode"
)

type Entry struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	SSHHost string `json:"ssh_host,omitempty"`
	TargetHost string `json:"target_host"`
	LocalPort int `json:"local_port,omitempty"`
	TargetPort int `json:"target_port"`
	ServePort int `json:"serve_port,omitempty"`
}

type Config struct {
	Version int `json:"version"`
	Entries []Entry `json:"entries"`
}

var idPattern = regexp.MustCompile("^[a-f0-9]{16}$")
var hostPattern = regexp.MustCompile("^[a-zA-Z0-9_][a-zA-Z0-9_.@-]*$")

func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil { panic(err) }
	return hex.EncodeToString(b)
}

func (e Entry) Validate() error {
	if !idPattern.MatchString(e.ID) { return errors.New("invalid entry ID") }
	if strings.TrimSpace(e.Name) == "" || len(e.Name) > 80 { return errors.New("name must contain 1 to 80 bytes") }
	for _, r := range e.Name { if unicode.IsControl(r) { return errors.New("name contains a control character") } }
	if e.SSHHost != "" && !hostPattern.MatchString(e.SSHHost) { return errors.New("SSH host must be a plain config alias or user@hostname") }
	if e.TargetPort < 1 || e.TargetPort > 65535 { return errors.New("target port must be 1 to 65535") }
	switch e.Kind {
	case "ssh":
		if e.SSHHost == "" { return errors.New("SSH host is required") }
		if e.LocalPort < 1 || e.LocalPort > 65535 { return errors.New("local port must be 1 to 65535") }
		if !hostPattern.MatchString(e.TargetHost) && net.ParseIP(e.TargetHost) == nil { return errors.New("target must be a hostname or IP address") }
		if strings.Contains(e.TargetHost, "@") { return errors.New("target host cannot contain @") }
	case "serve":
		if e.TargetHost != "127.0.0.1" && e.TargetHost != "localhost" { return errors.New("Serve targets must be localhost or 127.0.0.1") }
		if e.ServePort < 1 || e.ServePort > 65535 { return errors.New("HTTPS port must be 1 to 65535") }
	default:
		return errors.New("kind must be ssh or serve")
	}
	return nil
}

func (c Config) Validate() error {
	if c.Version != 1 { return fmt.Errorf("unsupported config version %d", c.Version) }
	ids, names, endpoints := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range c.Entries {
		if err := e.Validate(); err != nil { return fmt.Errorf("%s: %w", e.Name, err) }
		key := fmt.Sprintf("ssh:%d", e.LocalPort)
		if e.Kind == "serve" { key = fmt.Sprintf("serve:%s:%d", e.SSHHost, e.ServePort) }
		if ids[e.ID] || names[e.Name] || endpoints[key] { return errors.New("entry IDs, names, and listening endpoints must be unique") }
		ids[e.ID], names[e.Name], endpoints[key] = true, true, true
	}
	return nil
}

type Store struct {
	Dir string
	lock *os.File
}

func OpenStore(dir string) (*Store, error) {
	if strings.Contains(dir, "%") { return nil, errors.New("config directory cannot contain SSH percent-expansion tokens") }
	if err := os.MkdirAll(dir, 0700); err != nil { return nil, err }
	info, err := os.Lstat(dir)
	if err != nil { return nil, err }
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 { return nil, errors.New("temo directory must be a private directory (mode 0700)") }
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil { return nil, err }
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("another temo process is using this config directory")
	}
	return &Store{Dir: dir, lock: lock}, nil
}

func (s *Store) Close() error { return s.lock.Close() }

func (s *Store) Load() (Config, error) {
	c := Config{Version: 1, Entries: []Entry{}}
	err := readJSON(filepath.Join(s.Dir, "config.json"), &c)
	if errors.Is(err, os.ErrNotExist) { return c, nil }
	if err != nil { return c, err }
	return c, c.Validate()
}

func (s *Store) Save(c Config) error {
	if err := c.Validate(); err != nil { return err }
	return atomicJSON(filepath.Join(s.Dir, "config.json"), c)
}

func readJSON(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil { return err }
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil { return err }
	if err := d.Decode(new(any)); err != io.EOF { return errors.New("unexpected trailing JSON") }
	return nil
}

func atomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil { return err }
	f, err := os.CreateTemp(filepath.Dir(path), ".temo-*")
	if err != nil { return err }
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(b, '\n')); err != nil { f.Close(); return err }
	if err = f.Sync(); err != nil { f.Close(); return err }
	if err = f.Close(); err != nil { return err }
	if err = os.Rename(name, path); err != nil { return err }
	dir, err := os.Open(filepath.Dir(path))
	if err != nil { return err }
	defer dir.Close()
	return dir.Sync()
}
