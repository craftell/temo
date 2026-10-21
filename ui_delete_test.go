package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func uiDeleteKey(t *testing.T, m model, k rune) model {
	t.Helper()
	return uiInput(t, m, tea.KeyPressMsg{Code: k})
}

func TestUIDeleteCancelDefaultsAndFrozenTarget(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyEsc}, {Code: 'n'}} {
		a, b := uiTestEntry("Alpha"), uiTestEntry("Beta")
		m := uiTestModel(a, b)
		m.persist = func(Config) error { t.Fatal("cancel saved config"); return nil }
		m.do = func(Entry, string) Result { t.Fatal("cancel invoked engine"); return Result{} }
		m = uiDeleteKey(t, m, 'D')
		if m.deletion == nil || m.deletion.confirm {
			t.Fatal("dialog must default to cancel")
		}
		m.cursor = 1
		if m.deletion.entry != a {
			t.Fatal("target changed with selection")
		}
		m = uiInput(t, m, tea.PasteMsg{Content: "y"})
		m = uiInput(t, m, key)
		if m.deletion != nil || !reflect.DeepEqual(m.cfg.Entries, []Entry{a, b}) {
			t.Fatal("cancel mutated definitions")
		}
	}
}

func TestUIDeleteChecksFreshStatusAndNeverStops(t *testing.T) {
	for _, state := range []string{"ready", "running", "inactive", "unknown", "error", "pending", ""} {
		t.Run(state, func(t *testing.T) {
			entry := uiTestEntry("Alpha")
			m := uiTestModel(entry)
			m.states[entry.ID] = Result{State: "stopped"}
			m.do = func(e Entry, action string) Result {
				if e != entry || action != "status" {
					t.Fatalf("unexpected lifecycle action %s", action)
				}
				return Result{State: state}
			}
			m.persist = func(Config) error { t.Fatal("unsafe state persisted"); return nil }
			m = uiDeleteKey(t, m, 'D')
			m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
			m = uiFinish(t, m, cmd)
			if m.deletion == nil || m.deletion.err == "" || len(m.cfg.Entries) != 1 {
				t.Fatal("unsafe deletion accepted")
			}
		})
	}
}

func TestUIDeleteSuccessfulFilteredNeighborsAndLast(t *testing.T) {
	for _, cursor := range []int{0, 1} {
		a, b, c := uiTestEntry("Keep Alpha"), uiTestEntry("Keep Beta"), uiTestEntry("Other")
		m := uiTestModel(a, b, c)
		m.query.insert("Keep")
		m.cursor = cursor
		target, _ := m.selected()
		var saved Config
		m.persist = func(cfg Config) error { saved = cloneUIConfig(cfg); return nil }
		m.do = func(e Entry, action string) Result {
			if e != target || action != "status" {
				t.Fatal("wrong frozen target/action")
			}
			return Result{State: "stopped"}
		}
		m = uiDeleteKey(t, m, 'D')
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m = uiFinish(t, m, cmd)
		if len(saved.Entries) != 2 || len(m.cfg.Entries) != 2 || string(m.query.value) != "Keep" || m.cursor != 0 || m.deletion != nil {
			t.Fatal("deletion lost filter/neighbor")
		}
		if _, exists := m.states[target.ID]; exists {
			t.Fatal("deleted status retained")
		}
	}
	m := uiTestModel(uiTestEntry("Last"))
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	m.persist = func(Config) error { return nil }
	m = uiDeleteKey(t, m, 'D')
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
	m = uiFinish(t, m, cmd)
	if _, ok := m.selected(); ok || m.cursor != 0 || len(m.cfg.Entries) != 0 {
		t.Fatal("last row deletion failed")
	}
}

func TestUIDeleteSaveErrorReconcilesBeforeAndAfterRename(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before rename", true: "after rename"}[committed], func(t *testing.T) {
			entry := uiTestEntry("Alpha")
			m := uiTestModel(entry)
			disk := cloneUIConfig(m.cfg)
			m.do = func(Entry, string) Result { return Result{State: "stopped"} }
			m.persist = func(cfg Config) error {
				if committed {
					disk = cloneUIConfig(cfg)
				}
				return errors.New("fsync failed")
			}
			m.reload = func() (Config, error) { return disk, nil }
			m = uiDeleteKey(t, m, 'D')
			m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
			m = uiFinish(t, m, cmd)
			if !reflect.DeepEqual(m.cfg, disk) || m.configUncertain || m.deletion == nil || !strings.Contains(m.deletion.err, "fsync failed") {
				t.Fatal("save error did not reconcile disk")
			}
			if committed {
				m = uiDeleteKey(t, m, 'y')
				if !strings.Contains(m.deletion.err, "already absent") {
					t.Fatal("retried absent definition")
				}
			}
		})
	}
}

func TestUIDeleteUnknownPersistenceBlocksChangesUntilReload(t *testing.T) {
	entry := uiTestEntry("Alpha")
	m := uiTestModel(entry)
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	m.persist = func(Config) error { return errors.New("write failed") }
	m.reload = func() (Config, error) { return Config{}, errors.New("read failed") }
	m = uiDeleteKey(t, m, 'D')
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
	m = uiFinish(t, m, cmd)
	if !m.configUncertain {
		t.Fatal("unknown disk state not blocked")
	}
	m = uiDeleteKey(t, m, 'y')
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	for _, key := range "aesxoD" {
		m = uiDeleteKey(t, m, key)
		if m.editor != nil || m.deletion != nil || m.busy != "" {
			t.Fatal("mutation allowed while disk unknown")
		}
	}
	m.reload = func() (Config, error) { return Config{Version: 1}, nil }
	m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	m = uiFinish(t, m, cmd)
	if m.configUncertain || len(m.cfg.Entries) != 0 {
		t.Fatal("successful reload failed to unblock")
	}
}

func TestUIDeleteRefreshStaleAndQueued(t *testing.T) {
	entry := uiTestEntry("Alpha")
	m := uiTestModel(entry)
	m.refresh.pending = map[string]uint64{entry.ID: 4}
	m = uiDeleteKey(t, m, 'D')
	if m.deletion != nil {
		t.Fatal("opened dialog for in-flight status")
	}
	delete(m.refresh.pending, entry.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.refresh = uiRefresh{ctx: ctx, cancel: cancel, pending: map[string]uint64{}, queue: []Entry{entry}, total: 1}
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	m.persist = func(Config) error { return nil }
	m = uiDeleteKey(t, m, 'D')
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
	if len(m.refresh.queue) != 0 || m.refresh.total != 0 {
		t.Fatal("queued refresh retained deleted candidate")
	}
	m = uiFinish(t, m, cmd)
	m = uiInput(t, m, uiObserved{entry: entry, revision: 0, result: Result{State: "ready"}})
	m = uiInput(t, m, uiObserved{entry: entry, revision: 4, result: Result{State: "ready"}})
	if _, ok := m.states[entry.ID]; ok {
		t.Fatal("stale status resurrected deleted route")
	}
}

func TestUIDeleteBusyDuplicateAndQuit(t *testing.T) {
	entry := uiTestEntry("Alpha")
	m := uiTestModel(entry)
	calls := 0
	m.do = func(Entry, string) Result { calls++; return Result{State: "stopped"} }
	m.persist = func(Config) error { return nil }
	m = uiDeleteKey(t, m, 'D')
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
	for _, key := range []tea.KeyPressMsg{{Code: 'y'}, {Code: 'D'}, {Code: tea.KeyEsc}, {Code: 'q'}, {Code: 'c', Mod: tea.ModCtrl}} {
		m = uiInput(t, m, key)
	}
	if m.quitting || m.deletion == nil || m.busy == "" {
		t.Fatal("busy deletion escaped")
	}
	m = uiInput(t, m, uiDeleteDone{ticket: m.ticket - 1, entry: entry})
	if m.busy == "" {
		t.Fatal("stale completion cleared busy")
	}
	message := cmd()
	m = uiInput(t, m, message)
	m = uiInput(t, m, message)
	if calls != 1 || len(m.cfg.Entries) != 0 || m.busy != "" {
		t.Fatal("duplicate delete executed")
	}
}

func TestUIEditorSaveErrorAdoptsActualDisk(t *testing.T) {
	original := uiTestEntry("Original")
	m := uiTestModel(original)
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	disk := cloneUIConfig(m.cfg)
	m.persist = func(cfg Config) error { disk = cloneUIConfig(cfg); return errors.New("directory sync failed") }
	m.reload = func() (Config, error) { return disk, nil }
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
	m = uiFinish(t, m, cmd)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "Renamed"})
	m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, cmd)
	if m.cfg.Entries[0].Name != "Renamed" || m.editor == nil || m.configUncertain {
		t.Fatal("editor failed to reconcile persisted save")
	}
}

func TestUIDeleteReconcilesRealStoreAndPreservesJournal(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			dir := t.TempDir()
			store := &Store{Dir: dir}
			entry := uiTestEntry("Stored")
			cfg := Config{Version: 1, Entries: []Entry{entry}}
			if err := store.Save(cfg); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(dir, "owner-"+entry.ID+".json")
			if err := os.WriteFile(journal, []byte("retained ownership"), 0600); err != nil {
				t.Fatal(err)
			}
			m := NewModel(cfg, store, nil)
			m.do = func(Entry, string) Result { return Result{State: "stopped"} }
			m.persist = func(next Config) error {
				if committed {
					if err := store.Save(next); err != nil {
						return err
					}
					return errors.New("simulated directory fsync error after rename")
				}
				// A real validation failure happens before atomicJSON can replace disk.
				next.Version = 99
				return store.Save(next)
			}
			m = uiDeleteKey(t, m, 'D')
			m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'y'})
			m = uiFinish(t, m, cmd)
			actual, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m.cfg, cloneUIConfig(actual)) || m.configUncertain {
				t.Fatal("model differs from real persisted configuration")
			}
			if committed && len(m.cfg.Entries) != 0 || !committed && len(m.cfg.Entries) != 1 {
				t.Fatal("unexpected persistence outcome")
			}
			if data, err := os.ReadFile(journal); err != nil || string(data) != "retained ownership" {
				t.Fatal("deletion altered journal")
			}
		})
	}
}

func TestUIDeleteModalActionsVisibleAtMinimumSize(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"))
	m.width, m.height = 50, 14
	m = uiDeleteKey(t, m, 'D')
	m.deletion.err = strings.Repeat("long diagnostic ", 100) + "LAST_DIAGNOSTIC"
	content := m.View().Content
	for _, text := range []string{"Delete definition", "Cancel", "Enter/y", "Error:"} {
		if !strings.Contains(content, text) {
			t.Fatalf("modal hid %q: %s", text, content)
		}
	}
	for i := 0; i < 100; i++ {
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !strings.Contains(m.View().Content, "LAST_DIAGNOSTIC") {
		t.Fatal("long delete diagnostic inaccessible")
	}
}

func TestUIEditorReconciledCommitCannotRetryAgainstStaleRoute(t *testing.T) {
	for _, isNew := range []bool{false, true} {
		t.Run(fmt.Sprint(isNew), func(t *testing.T) {
			entry := uiTestEntry("Original")
			m := uiTestModel(entry)
			if isNew {
				m = uiTestModel()
			}
			calls := 0
			m.do = func(Entry, string) Result { calls++; return Result{State: "stopped"} }
			disk := cloneUIConfig(m.cfg)
			m.persist = func(cfg Config) error { disk = cloneUIConfig(cfg); return errors.New("sync failed") }
			m.reload = func() (Config, error) { return disk, nil }
			if isNew {
				m = uiAddSSH(t, m, "New")
			} else {
				var cmd tea.Cmd
				m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
				m = uiFinish(t, m, cmd)
				m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
				m = uiInput(t, m, tea.PasteMsg{Content: "Renamed"})
			}
			m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			m = uiFinish(t, m, cmd)
			before := calls
			m = uiInput(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if calls != before || !strings.Contains(m.editor.err, "reopen") {
				t.Fatal("retry checked stale original or duplicated committed entry")
			}
		})
	}
}

func TestUIDeleteRejectsDefinitionChangedWhileDialogOpen(t *testing.T) {
	m := uiTestModel(uiTestEntry("Original"))
	m = uiDeleteKey(t, m, 'D')
	m.cfg.Entries[0].TargetPort++
	m = uiDeleteKey(t, m, 'y')
	if !strings.Contains(m.deletion.err, "changed") {
		t.Fatal("changed identity allowed deletion")
	}
}

func TestUIDeleteRequiresVisibleConfirmation(t *testing.T) {
	for _, size := range [][2]int{{49, 30}, {100, 13}, {20, 5}} {
		for _, opened := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/opened=%v", size[0], size[1], opened), func(t *testing.T) {
				entry := uiTestEntry("Alpha")
				m := uiTestModel(entry)
				m.do = func(Entry, string) Result { t.Fatal("hidden confirmation ran status"); return Result{} }
				m.persist = func(Config) error { t.Fatal("hidden confirmation saved"); return nil }
				if opened {
					m = uiDeleteKey(t, m, 'D')
					m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
				}
				m = uiInput(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				if !opened {
					m = uiDeleteKey(t, m, 'D')
					if m.deletion != nil {
						t.Fatal("opened hidden delete dialog")
					}
				}
				m = uiDeleteKey(t, m, 'y')
				if opened {
					m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
					m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
				}
				if len(m.cfg.Entries) != 1 || m.busy != "" {
					t.Fatal("hidden confirmation mutated configuration")
				}
				m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
				if m.deletion != nil {
					t.Fatal("hidden dialog cannot be cancelled")
				}
			})
		}
	}
}
