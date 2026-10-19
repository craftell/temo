package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSSHStatusRetainsMissingMasterJournal(t *testing.T) {
	for _, phase := range []string{"active", "pending"} {
		t.Run(phase, func(t *testing.T) {
			e := exampleSSH()
			r := &fakeRunner{run: func(_ string, args []string) ([]byte, error) {
				if !contains(args, "check") {
					t.Errorf("unexpected command during missing-master check: %v", args)
				}
				return nil, &exec.ExitError{}
			}}
			g := Engine{Store: testStore(t), Runner: r}
			if err := g.remember(Ownership{Entry: e, Phase: phase}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(g.journalPath(e))
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(g.journalPath(e))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				got := g.Do(e, "status")
				if phase == "active" && (got.State != "inactive" || !strings.Contains(got.Detail, "Ownership retained")) {
					t.Fatal(got)
				}
				if phase == "pending" && (got.State != "error" || !strings.Contains(got.Detail, "uncertain")) {
					t.Fatal(got)
				}
				assertJournalUnchanged(t, &g, e, before, info)
			}
			if phase == "pending" {
				if got := g.Do(e, "start"); got.State != "error" || !strings.Contains(got.Detail, "uncertain") {
					t.Fatal(got)
				}
				assertJournalUnchanged(t, &g, e, before, info)
			}
			if got := g.Do(e, "stop"); got.State != "stopped" {
				t.Fatal(got)
			}
			if _, err := os.Stat(g.journalPath(e)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("explicit stop retained journal: %v", err)
			}
		})
	}
}

func TestServeStatusRetainsRuntimeJournal(t *testing.T) {
	for _, tc := range []struct {
		name, phase, state string
		used, drift        bool
	}{
		{name: "active missing", phase: "active", state: "inactive"},
		{name: "pending missing", phase: "pending", state: "error"},
		{name: "active ready", phase: "active", state: "ready", used: true},
		{name: "pending present", phase: "pending", state: "error", used: true},
		{name: "active drift", phase: "active", state: "error", used: true, drift: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := exampleServe()
			config := []byte("{}")
			if tc.used {
				config = serveJSON(e, tc.drift)
			}
			r := &fakeRunner{run: func(name string, args []string) ([]byte, error) {
				if name == "tailscale" && reflect.DeepEqual(args, []string{"status", "--json"}) {
					return []byte(`{"BackendState":"Running","Self":{"ID":"node1","DNSName":"dev.example.ts.net."}}`), nil
				}
				if name == "tailscale" && reflect.DeepEqual(args, []string{"serve", "status", "--json"}) {
					return config, nil
				}
				t.Errorf("unexpected mutation: %s %v", name, args)
				return nil, errors.New("unexpected mutation")
			}}
			g := Engine{Store: testStore(t), Runner: r}
			if err := g.remember(Ownership{Entry: e, Phase: tc.phase, NodeID: "node1", Snapshot: desiredSnapshot("dev.example.ts.net", e)}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(g.journalPath(e))
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(g.journalPath(e))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				got := g.Do(e, "status")
				if got.State != tc.state {
					t.Fatal(got)
				}
				if tc.phase == "pending" && !strings.Contains(got.Detail, "uncertain") {
					t.Fatal(got)
				}
				if tc.drift && !strings.Contains(got.Detail, "changed outside") {
					t.Fatal(got)
				}
				assertJournalUnchanged(t, &g, e, before, info)
			}
			if !tc.used && tc.phase == "active" {
				if got := g.Do(e, "stop"); got.State != "stopped" {
					t.Fatal(got)
				}
				if _, err := os.Stat(g.journalPath(e)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("explicit stop retained journal: %v", err)
				}
			}
		})
	}
}

func assertJournalUnchanged(t *testing.T, g *Engine, e Entry, before []byte, info os.FileInfo) {
	t.Helper()
	after, err := os.ReadFile(g.journalPath(e))
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(g.journalPath(e))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !os.SameFile(info, afterInfo) || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("status changed the ownership journal")
	}
}

type observationRunnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f observationRunnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func TestObserveCancellationPreservesJournal(t *testing.T) {
	for _, e := range []Entry{exampleSSH(), exampleServe()} {
		for _, alreadyCanceled := range []bool{false, true} {
			name := e.Kind + "/in flight"
			if alreadyCanceled {
				name = e.Kind + "/already canceled"
			}
			t.Run(name, func(t *testing.T) {
				entered := make(chan struct{})
				r := observationRunnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				})
				g := Engine{Store: testStore(t), Runner: r}
				if err := g.remember(Ownership{Entry: e, Phase: "active", NodeID: "node1", Snapshot: desiredSnapshot("dev.example.ts.net", e)}); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(g.journalPath(e))
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(g.journalPath(e))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if alreadyCanceled {
					cancel()
				}
				done := make(chan Result, 1)
				go func() { done <- g.Observe(ctx, e) }()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("observation did not reach runner")
				}
				cancel()
				select {
				case got := <-done:
					if got.State != "error" || !strings.Contains(got.Detail, context.Canceled.Error()) {
						t.Fatal(got)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("observation ignored parent cancellation")
				}
				assertJournalUnchanged(t, &g, e, before, info)
			})
		}
	}
}

func TestSSHFailedStartDirectsUserToStop(t *testing.T) {
	e := exampleSSH()
	r := &fakeRunner{run: func(_ string, args []string) ([]byte, error) {
		switch {
		case contains(args, "-G"):
			return []byte("hostname 127.0.0.1\n"), nil
		case contains(args, "-M"):
			return nil, errors.New("bind: Address already in use")
		case contains(args, "check"):
			return nil, &exec.ExitError{}
		}
		t.Errorf("unexpected command: %v", args)
		return nil, errors.New("unexpected command")
	}}
	g := Engine{Store: testStore(t), Runner: r}
	got := g.Do(e, "start")
	if got.State != "error" || !strings.Contains(got.Detail, "use Stop") || strings.Contains(got.Detail, "refresh before") {
		t.Fatal(got)
	}
	before, err := os.ReadFile(g.journalPath(e))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(g.journalPath(e))
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"status", "start"} {
		if got := g.Do(e, action); got.State != "error" || !strings.Contains(got.Detail, "use explicit stop") {
			t.Fatal(action, got)
		}
		assertJournalUnchanged(t, &g, e, before, info)
	}
	if got := g.Do(e, "stop"); got.State != "stopped" {
		t.Fatal(got)
	}
	if _, err := os.Stat(g.journalPath(e)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit stop retained journal: %v", err)
	}
}
