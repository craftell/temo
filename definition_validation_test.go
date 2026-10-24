package main

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestAlternativeDefinitionsRoundTrip(t *testing.T) {
	for _, kind := range []string{"ssh", "serve"} {
		t.Run(kind, func(t *testing.T) {
			a := exampleSSH()
			if kind == "serve" {
				a = exampleServe()
			}
			b := a
			b.ID = NewID()
			b.TargetPort++
			c := Config{Version: 1, Entries: []Entry{a, b}}
			s := testStore(t)
			if err := s.Save(c); err != nil {
				t.Fatal(err)
			}
			got, err := s.Load()
			if err != nil || !reflect.DeepEqual(got, c) {
				t.Fatalf("roundtrip: %+v %v", got, err)
			}
			c.Entries[1].ID = a.ID
			if err := s.Save(c); err == nil {
				t.Fatal("duplicate identity accepted")
			}
		})
	}
}

func TestUnownedDefinitionsNeedNoBackend(t *testing.T) {
	for _, e := range []Entry{exampleSSH(), exampleServe()} {
		t.Run(e.Kind, func(t *testing.T) {
			r := &fakeRunner{run: func(string, []string) ([]byte, error) {
				t.Fatal("unowned status/stop contacted backend")
				return nil, nil
			}}
			g := Engine{Store: testStore(t), Runner: r}
			for _, action := range []string{"status", "stop"} {
				if got := g.Do(e, action); got.State != "stopped" {
					t.Fatal(got)
				}
			}
			if _, owned, err := g.ownership(e); err != nil || owned {
				t.Fatalf("unexpected ownership: %v %v", owned, err)
			}
		})
	}
}

func TestOfflineDefinitionEditAndDeleteByID(t *testing.T) {
	a := exampleServe()
	b := a
	b.ID = NewID()
	s := testStore(t)
	cfg := Config{Version: 1, Entries: []Entry{a, b}}
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{run: func(string, []string) ([]byte, error) {
		t.Fatal("definition editing contacted backend")
		return nil, nil
	}}
	g := &Engine{Store: s, Runner: r}
	m := NewModel(cfg, s, g)
	m.kind = "serve"
	m.cursor = 1
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
	m = uiFinish(t, m, cmd)
	if m.editor == nil {
		t.Fatal("offline editor did not open")
	}
	m.editor.fields[uiTargetPort] = uiTextField{value: []rune("4000"), cursor: 4}
	m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, cmd)
	got, err := s.Load()
	if err != nil || len(got.Entries) != 2 || got.Entries[0] != a || got.Entries[1].ID != b.ID || got.Entries[1].TargetPort != 4000 {
		t.Fatalf("edited wrong duplicate: %+v %v", got, err)
	}
	m.cursor = 1
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'D'})
	m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
	uiFinish(t, m, cmd)
	got, err = s.Load()
	if err != nil || len(got.Entries) != 1 || got.Entries[0] != a {
		t.Fatalf("deleted wrong duplicate: %+v %v", got, err)
	}
}

func TestServeAlternativesConflictOnlyAtStart(t *testing.T) {
	a := exampleServe()
	b := a
	b.ID = NewID()
	b.TargetPort = 4000
	config := []byte("{}")
	mutations := 0
	r := &fakeRunner{run: func(_ string, args []string) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"status", "--json"}) {
			return []byte(`{"BackendState":"Running","Self":{"ID":"node1","DNSName":"dev.example.ts.net."}}`), nil
		}
		if reflect.DeepEqual(args, []string{"serve", "status", "--json"}) {
			return config, nil
		}
		mutations++
		if contains(args, "off") {
			config = []byte("{}")
		} else if contains(args, "http://127.0.0.1:4000") {
			config = []byte(`{"TCP":{"8443":{"HTTPS":true}},"Web":{"dev.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4000"}}}}}`)
		} else {
			config = serveJSON(a, false)
		}
		return nil, nil
	}}
	g := Engine{Store: testStore(t), Runner: r}
	if got := g.Do(a, "start"); got.State != "ready" {
		t.Fatal(got)
	}
	before := mutations
	if got := g.Do(b, "status"); got.State != "stopped" {
		t.Fatal(got)
	}
	if got := g.Do(b, "start"); got.State != "error" || !strings.Contains(got.Detail, "already configured") {
		t.Fatal(got)
	}
	if mutations != before {
		t.Fatal("conflicting start changed active route")
	}
	if _, owned, err := g.ownership(b); err != nil || owned {
		t.Fatalf("failed conflict acquired ownership: %v %v", owned, err)
	}
	if got := g.Do(b, "stop"); got.State != "stopped" || mutations != before {
		t.Fatal("unowned stop touched active alternative", got)
	}
	if got := g.Do(a, "stop"); got.State != "stopped" {
		t.Fatal(got)
	}
	if got := g.Do(b, "start"); got.State != "ready" {
		t.Fatal(got)
	}
}

func TestSSHAlternativeBindFailureKeepsActiveRoute(t *testing.T) {
	a := exampleSSH()
	b := a
	b.ID = NewID()
	b.SSHHost = "other-server"
	masters := map[string]bool{}
	r := &fakeRunner{run: func(_ string, args []string) ([]byte, error) {
		if contains(args, "-G") {
			return []byte("hostname server\n"), nil
		}
		socket := ""
		for i, arg := range args {
			if arg == "-S" {
				socket = args[i+1]
			}
		}
		if contains(args, "-M") {
			if len(masters) != 0 {
				return nil, errors.New("bind: Address already in use")
			}
			masters[socket] = true
		}
		if contains(args, "check") && !masters[socket] {
			return nil, &exec.ExitError{}
		}
		if contains(args, "exit") {
			delete(masters, socket)
		}
		return nil, nil
	}}
	g := Engine{Store: testStore(t), Runner: r}
	if got := g.Do(a, "start"); got.State != "ready" {
		t.Fatal(got)
	}
	before, err := os.ReadFile(g.journalPath(a))
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Do(b, "start"); got.State != "error" || !strings.Contains(got.Detail, "Address already in use") {
		t.Fatal(got)
	}
	after, err := os.ReadFile(g.journalPath(a))
	if err != nil || string(before) != string(after) || !masters[g.socket(a)] {
		t.Fatal("conflict changed first route")
	}
	// Failed SSH starts conservatively retain pending ownership; never silently retry.
	if got := g.Do(b, "start"); got.State != "error" {
		t.Fatal(got)
	}
	if got := g.Do(b, "stop"); got.State != "stopped" || !masters[g.socket(a)] {
		t.Fatal("failed alternative stop changed first route", got)
	}
	if got := g.Do(a, "stop"); got.State != "stopped" {
		t.Fatal(got)
	}
	if got := g.Do(b, "start"); got.State != "ready" {
		t.Fatal(got)
	}

}
