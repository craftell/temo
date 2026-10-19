package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestUIFormArrowFieldMovement(t *testing.T) {
	for _, down := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Code: tea.KeyDown, IsRepeat: true},
	} {
		m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a', Text: "a"})
		m = uiInput(t, m, down)
		if m.editor.focus != uiSSHHost {
			t.Fatalf("Down focused field %d, want SSH host %d", m.editor.focus, uiSSHHost)
		}
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
		if m.editor.focus != uiName {
			t.Fatal("Up did not return to Name")
		}
	}
}

func TestUISeparateRouteTabs(t *testing.T) {
	ssh := uiTestEntry("SSH-only")
	serve := Entry{ID: NewID(), Name: "Serve-only", Kind: "serve", TargetHost: "127.0.0.1", TargetPort: 3000, ServePort: 8443}
	m := uiTestModel(ssh, serve)
	if strings.Contains(m.View().Content, "Serve-only") {
		t.Fatal("SSH view includes Serve definitions")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if !strings.Contains(m.View().Content, "Serve-only") || strings.Contains(m.View().Content, "SSH-only") {
		t.Fatal("Right did not switch to a Serve-only view")
	}
}

func TestUIStartupSchedulesStatus(t *testing.T) {
	m := uiTestModel(uiTestEntry("Saved"))
	if m.Init() == nil {
		t.Fatal("startup did not schedule status observations")
	}
}

func TestUIListKeysMatchTerminalRepresentations(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: tea.KeyDown, IsRepeat: true},
		{Code: 'j'}, {Code: 'j', Text: "j"},
	} {
		m := uiTestModel(uiTestEntry("Alpha"), uiTestEntry("Beta"))
		m = uiInput(t, m, key)
		entry, _ := m.selected()
		if entry.Name != "Beta" {
			t.Fatalf("%+v (%q) did not move selection", key, key.String())
		}
		for _, up := range []tea.KeyPressMsg{{Code: tea.KeyUp}, {Code: 'k', Text: "k"}} {
			m.cursor = 1
			m = uiInput(t, m, up)
			entry, _ = m.selected()
			if entry.Name != "Alpha" {
				t.Fatalf("%+v (%q) did not move up", up, up.String())
			}
		}
	}
}

func TestUIFormsUseOnlyKindFieldsAndKeepTextKeys(t *testing.T) {
	for _, kind := range []string{"ssh", "serve"} {
		m := uiTestModel()
		if kind == "serve" {
			m = uiInput(t, m, tea.KeyPressMsg{Code: '2', Text: "2"})
		}
		m = uiInput(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
		for _, key := range []rune{'j', 'k', 'q', 's', '1', '2'} {
			m = uiInput(t, m, tea.KeyPressMsg{Code: key, Text: string(key)})
		}
		if string(m.editor.fields[uiName].value) != "jkqs12" || m.editor.original.Kind != kind {
			t.Fatal("text entry triggered navigation or changed kind")
		}
		if strings.Contains(m.View().Content, "Kind [") {
			t.Fatal("kind remains editable")
		}
		fields := m.editor.visibleFields()
		for _, field := range fields[1:] {
			m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			if m.editor.focus != field {
				t.Fatalf("wrong field %d, want %d", m.editor.focus, field)
			}
		}
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.editor.focus != uiName {
			t.Fatal("Tab did not wrap visible fields")
		}
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		if m.editor.focus != fields[len(fields)-1] {
			t.Fatal("Shift+Tab did not wrap backwards")
		}
		if kind == "ssh" && strings.Contains(m.View().Content, "HTTPS port") {
			t.Fatal("SSH form shows Serve port")
		}
		if kind == "serve" && (strings.Contains(m.View().Content, "Local port") || strings.Contains(m.View().Content, "Target host")) {
			t.Fatal("Serve form shows unrelated SSH fields")
		}
	}
}

func TestUITabsRememberFilterAndSelection(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"), uiTestEntry("Beta"))
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'j'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: '2'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: '/'})
	m = uiInput(t, m, tea.PasteMsg{Content: "Serve filter"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = uiInput(t, m, tea.KeyPressMsg{Code: '1'})
	entry, ok := m.selected()
	if !ok || entry.Name != "Beta" || len(m.query.value) != 0 {
		t.Fatal("Serve tab changed SSH position or filter")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if string(m.query.value) != "Serve filter" {
		t.Fatal("tab lost its filter")
	}
}

func TestUIConfigPathIsDiscoverableAndScrollable(t *testing.T) {
	store := testStore(t)
	m := NewModel(Config{Version: 1}, store, nil)
	if !strings.Contains(m.View().Content, "p config location") {
		t.Fatal("list does not advertise configuration location")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'p'})
	content := strings.ReplaceAll(m.View().Content, "\n", "")
	if !strings.Contains(content, store.Dir+"/config.json") {
		t.Fatalf("actual config path missing: %s", content)
	}
}

func TestUIRefreshIsBoundedProgressiveAndNonblocking(t *testing.T) {
	entries := make([]Entry, 7)
	for i := range entries {
		entries[i] = uiTestEntry(string(rune('A' + i)))
	}
	entries[6].Kind, entries[6].SSHHost, entries[6].ServePort = "serve", "", 8443
	m := uiTestModel(entries...)
	calls := 0
	m.do = func(Entry, string) Result { calls++; return stopped() }
	m, cmd := uiStep(t, m, m.Init()())
	if calls != 0 || m.busy != "" || len(m.refresh.pending) != uiRefreshLimit {
		t.Fatal("startup refresh was synchronous or unbounded")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != uiRefreshLimit {
		t.Fatalf("startup command = %T, want bounded batch", cmd())
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatal("refresh blocked arrows")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: '?'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = uiInput(t, m, tea.KeyPressMsg{Code: '/'})
	m = uiInput(t, m, tea.PasteMsg{Content: "B"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.searching {
		t.Fatal("arrow did not return search to list")
	}
	m, _ = uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	if len(m.refresh.pending) != uiRefreshLimit {
		t.Fatal("repeat refresh duplicated work")
	}
	m, next := uiStep(t, m, batch[2]())
	if m.states[entries[2].ID].State != "stopped" || m.refresh.done != 1 || calls != 1 {
		t.Fatal("first completed observation was not applied progressively")
	}
	if next == nil || len(m.refresh.pending) != uiRefreshLimit {
		t.Fatal("completion did not replenish bounded queue")
	}
	m = uiFinish(t, m, next)
	for i, command := range batch {
		if i != 2 {
			m = uiFinish(t, m, command)
		}
	}
	if calls != len(entries) || m.refresh.done != len(entries) || m.states[entries[6].ID].State != "stopped" {
		t.Fatal("refresh omitted queued, filtered, or hidden-tab entries")
	}
}

func TestUIRefreshCannotOverwriteActionAndQuitCancels(t *testing.T) {
	entry := uiTestEntry("Alpha")
	m := uiTestModel(entry)
	m.do = func(_ Entry, action string) Result {
		if action == "start" {
			return ready("http://127.0.0.1:8080")
		}
		return stopped()
	}
	m, observation := uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	m, denied := uiStep(t, m, tea.KeyPressMsg{Code: 's'})
	if denied != nil {
		t.Fatal("mutation overlapped same-entry observation")
	}
	old := observation()
	m = uiInput(t, m, old)
	m, start := uiStep(t, m, tea.KeyPressMsg{Code: 's'})
	m = uiFinish(t, m, start)
	m = uiInput(t, m, old)
	if m.states[entry.ID].State != "ready" {
		t.Fatal("stale status overwrote start")
	}
	m, _ = uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	ctx := m.refresh.ctx
	_, quit := uiStep(t, m, tea.KeyPressMsg{Code: 'q'})
	if quit == nil || ctx.Err() == nil {
		t.Fatal("quit did not cancel status reads")
	}
}

func TestUIBusyMutationStillAllowsListNavigation(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"), uiTestEntry("Beta"))
	m, _ = uiStep(t, m, tea.KeyPressMsg{Code: 's'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatal("mutation blocked list navigation")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.kind != "serve" {
		t.Fatal("mutation blocked tabs")
	}
}
