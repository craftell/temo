package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func uiTestEntry(name string) Entry {
	return Entry{
		ID: NewID(), Name: name, Kind: "ssh", SSHHost: "dev-box",
		LocalPort: 8080, TargetHost: "127.0.0.1", TargetPort: 3000,
	}
}

func uiTestModel(entries ...Entry) model {
	return NewModel(Config{Version: 1, Entries: entries}, nil, nil)
}

func uiStep(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	result, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return result, cmd
}

func uiInput(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, cmd := uiStep(t, m, msg)
	if cmd != nil {
		t.Fatalf("input %T unexpectedly started a command", msg)
	}
	return next
}

func uiFinish(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an asynchronous command")
	}
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		message := current()
		if batch, ok := message.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
			continue
		}
		next, followup := uiStep(t, m, message)
		m = next
		if followup != nil {
			pending = append(pending, followup)
		}
	}
	return m
}

func uiAddSSH(t *testing.T, m model, name string) model {
	t.Helper()
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
	m = uiInput(t, m, tea.PasteMsg{Content: name})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	return uiInput(t, m, tea.PasteMsg{Content: "dev-box"})
}

func TestUIQuitLeavesRoutesRunning(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"))
	m.do = func(Entry, string) Result {
		t.Fatal("quitting must not start or stop a route")
		return Result{}
	}
	_, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'q'})
	if cmd == nil {
		t.Fatal("q did not schedule quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not produce a quit message")
	}
}

func TestUISerializesOperationsAndBlocksQuitWhileBusy(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"))
	var actions []string
	m.do = func(_ Entry, action string) Result {
		actions = append(actions, action)
		return Result{State: "ready", Detail: "SSH forwarding is active."}
	}
	m, start := uiStep(t, m, tea.KeyPressMsg{Code: 's'})
	if start == nil || len(actions) != 0 {
		t.Fatal("start must be scheduled without running inside Update")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'x'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'r'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'q'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = uiFinish(t, m, start)
	if len(actions) != 1 || actions[0] != "start" {
		t.Fatalf("actions = %v, want only start", actions)
	}
	if !strings.Contains(m.View().Content, "setup ready") {
		t.Fatal("completed start was not rendered")
	}
	_, quit := uiStep(t, m, tea.KeyPressMsg{Code: 'q'})
	if quit == nil {
		t.Fatal("quit remained blocked after the operation finished")
	}
}

func TestUIActionShortcuts(t *testing.T) {
	for _, test := range []struct {
		key    rune
		action string
	}{{'s', "start"}, {'x', "stop"}, {'o', "open"}} {
		t.Run(test.action, func(t *testing.T) {
			entry := uiTestEntry("Alpha")
			m := uiTestModel(entry)
			calls := 0
			m.do = func(got Entry, action string) Result {
				calls++
				if got.ID != entry.ID || action != test.action {
					t.Fatalf("got %s %s, want %s %s", got.ID, action, entry.ID, test.action)
				}
				return Result{State: "stopped"}
			}
			m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: test.key})
			uiFinish(t, m, cmd)
			if calls != 1 {
				t.Fatalf("engine calls = %d, want 1", calls)
			}
		})
	}
}

func TestUIRefreshChecksAllRoutes(t *testing.T) {
	alpha, beta := uiTestEntry("Alpha"), uiTestEntry("Beta")
	beta.LocalPort = 8081
	m := uiTestModel(alpha, beta)
	var checked []string
	m.do = func(entry Entry, action string) Result {
		if action != "status" {
			t.Fatalf("refresh action = %q", action)
		}
		checked = append(checked, entry.ID)
		return Result{State: "stopped"}
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'j'})
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	if len(checked) != 0 {
		t.Fatal("refresh ran synchronously")
	}
	m = uiFinish(t, m, cmd)
	if len(checked) != 2 || checked[0] != alpha.ID || checked[1] != beta.ID {
		t.Fatalf("checked routes = %v, want Alpha and Beta", checked)
	}
	content := m.View().Content
	if !uiHasTableRow(content, "Beta", "stopped", true) || !uiHasTableRow(content, "Alpha", "stopped", false) {
		t.Fatal("refresh omitted a route state")
	}
}

func TestUISearchFiltersActionsAndTreatsQAsText(t *testing.T) {
	alpha, beta := uiTestEntry("Alpha"), uiTestEntry("Beta")
	m := uiTestModel(alpha, beta)
	m.do = func(entry Entry, action string) Result {
		if entry.ID != beta.ID || action != "start" {
			t.Fatalf("filtered action selected %s %s", entry.Name, action)
		}
		return Result{State: "ready"}
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: '/'})
	m = uiInput(t, m, tea.PasteMsg{Content: "Beta"})
	if strings.Contains(m.View().Content, "Alpha") {
		t.Fatal("search still displays an unmatched route")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'q'})
	if !strings.Contains(m.View().Content, "No matching routes") {
		t.Fatal("q was not entered as search text")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 's'})
	m = uiFinish(t, m, cmd)
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !strings.Contains(m.View().Content, "Alpha") || !strings.Contains(m.View().Content, "Beta") {
		t.Fatal("Escape did not clear the committed search")
	}
}

func TestUIEditRequiresFreshStoppedStatus(t *testing.T) {
	for _, state := range []string{"ready", "error", "stopped"} {
		t.Run(state, func(t *testing.T) {
			m := uiTestModel(uiTestEntry("Alpha"))
			m.do = func(Entry, string) Result { return Result{State: "stopped"} }
			m, refresh := uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
			m = uiFinish(t, m, refresh)
			m.do = func(_ Entry, action string) Result {
				if action != "status" {
					t.Fatalf("edit check action = %q", action)
				}
				return Result{State: state}
			}
			m, check := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
			if strings.Contains(m.View().Content, "Edit route") {
				t.Fatal("editor opened before the status check finished")
			}
			m = uiFinish(t, m, check)
			opened := strings.Contains(m.View().Content, "Edit route")
			if opened != (state == "stopped") {
				t.Fatalf("editor opened = %v for state %q", opened, state)
			}
		})
	}
}

func TestUIAddSavesUnicodeNameAndIgnoresUnusedPort(t *testing.T) {
	m := uiTestModel()
	var saved *Config
	m.persist = func(cfg Config) error { saved = &cfg; return nil }
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
	m = uiInput(t, m, tea.PasteMsg{Content: "日本語"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = uiInput(t, m, tea.PasteMsg{Content: "語"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = uiInput(t, m, tea.PasteMsg{Content: "dev-box"})
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if saved != nil {
		t.Fatal("save ran inside Update")
	}
	m = uiFinish(t, m, save)
	if saved == nil || len(saved.Entries) != 1 {
		t.Fatal("new route was not persisted")
	}
	entry := saved.Entries[0]
	if entry.Name != "日語本" || entry.SSHHost != "dev-box" || entry.ServePort != 0 {
		t.Fatalf("saved route = %+v", entry)
	}
	if strings.Contains(m.View().Content, "Add route") || !strings.Contains(m.View().Content, "日語本") {
		t.Fatal("successful save did not return to the saved route")
	}
}

func TestUIAddLocalServeDefaultsTo8443(t *testing.T) {
	m := uiTestModel()
	var saved *Config
	m.persist = func(cfg Config) error { saved = &cfg; return nil }
	m = uiInput(t, m, tea.KeyPressMsg{Code: '2'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
	m = uiInput(t, m, tea.PasteMsg{Content: "Local app"})
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, save)
	if saved == nil || len(saved.Entries) != 1 {
		t.Fatal("local Serve route was not persisted")
	}
	entry := saved.Entries[0]
	if entry.Kind != "serve" || entry.SSHHost != "" || entry.ServePort != 8443 || entry.LocalPort != 0 {
		t.Fatalf("local Serve route = %+v", entry)
	}
}

func TestUIEditRechecksStatusBeforeSave(t *testing.T) {
	m := uiTestModel(uiTestEntry("Original"))
	checks, saves := 0, 0
	m.do = func(_ Entry, action string) Result {
		if action != "status" {
			t.Fatalf("unexpected action %q", action)
		}
		checks++
		if checks == 1 {
			return Result{State: "stopped"}
		}
		return Result{State: "ready"}
	}
	m.persist = func(Config) error { saves++; return nil }
	m, check := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
	m = uiFinish(t, m, check)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "Renamed"})
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, save)
	if checks != 2 || saves != 0 {
		t.Fatalf("checks = %d, saves = %d", checks, saves)
	}
	if !strings.Contains(m.View().Content, "no longer confirmed stopped") {
		t.Fatal("save-time status failure was not shown")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !strings.Contains(m.View().Content, "Original") || strings.Contains(m.View().Content, "Renamed") {
		t.Fatal("blocked edit changed the active configuration")
	}
}

func TestUIFailedSaveKeepsPreviousConfiguration(t *testing.T) {
	m := uiTestModel(uiTestEntry("Original"))
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	m.persist = func(cfg Config) error {
		if cfg.Entries[0].Name != "Renamed" {
			t.Fatal("save did not receive the edited name")
		}
		return errors.New("disk full")
	}
	m, check := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
	m = uiFinish(t, m, check)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "Renamed"})
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, save)
	if !strings.Contains(m.View().Content, "disk full") || !strings.Contains(m.View().Content, "Edit route") {
		t.Fatal("failed save did not keep the form and show the error")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !strings.Contains(m.View().Content, "Original") || strings.Contains(m.View().Content, "Renamed") {
		t.Fatal("failed save replaced the previous in-memory configuration")
	}
}

func TestUIInvalidPortDoesNotScheduleSave(t *testing.T) {
	m := uiTestModel()
	m.persist = func(Config) error { t.Fatal("invalid form reached persistence"); return nil }
	m = uiAddSSH(t, m, "Alpha")
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "70000"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !strings.Contains(m.View().Content, "target port must be") {
		t.Fatal("invalid target port was not explained")
	}
}

func TestUIPasteOutsideInputsDoesNotRunShortcuts(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"))
	m.do = func(Entry, string) Result { t.Fatal("paste triggered an action"); return Result{} }
	uiInput(t, m, tea.PasteMsg{Content: "sxoq\n"})
}

func TestUIViewEscapesTerminalControlsAndFits(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := uiTestModel(uiTestEntry("\x1b]52;c;data\a日本語"))
	m.do = func(Entry, string) Result {
		return Result{State: "ready", URL: "http://localhost/\x1b", Detail: "warning\a\u202e"}
	}
	m, refresh := uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	m = uiFinish(t, m, refresh)
	m = uiInput(t, m, tea.WindowSizeMsg{Width: 50, Height: 14})
	view := m.View()
	if !view.AltScreen {
		t.Fatal("view did not request alternate-screen mode")
	}
	if strings.ContainsAny(view.Content, "\x1b\a\u202e") {
		t.Fatal("untrusted terminal control characters reached the view")
	}
	if !strings.Contains(view.Content, "\\u001b") {
		t.Fatal("escaped control character is not visible")
	}
	uiAssertFits(t, view.Content, 50, 14)
	m = uiInput(t, m, tea.WindowSizeMsg{Width: 7, Height: 3})
	uiAssertFits(t, m.View().Content, 7, 3)
}

func TestUIViewportKeepsSelectedRouteVisible(t *testing.T) {
	entries := make([]Entry, 20)
	for i := range entries {
		entries[i] = uiTestEntry(fmt.Sprintf("Route%02d", i))
	}
	m := uiTestModel(entries...)
	m = uiInput(t, m, tea.WindowSizeMsg{Width: 60, Height: 14})
	for i := 0; i < 19; i++ {
		m = uiInput(t, m, tea.KeyPressMsg{Code: 'j'})
	}
	content := m.View().Content
	if !uiHasTableRow(content, "Route19", "unknown", true) || strings.Contains(content, "Route00") {
		t.Fatal("viewport did not follow the selected route")
	}
	uiAssertFits(t, content, 60, 14)
}

func TestUIDetailsShowsFullErrorsAndScrolls(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := uiTestModel(uiTestEntry("Alpha"))
	m.do = func(Entry, string) Result {
		return Result{State: "error", Detail: "\x1b[31m" + strings.Repeat("diagnostic ", 600) + "FINAL_DETAIL_MARKER"}
	}
	m, refresh := uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	m = uiFinish(t, m, refresh)
	m = uiInput(t, m, tea.WindowSizeMsg{Width: 60, Height: 14})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'd'})
	if !strings.Contains(m.View().Content, "temo | Details") {
		t.Fatal("d did not open details")
	}
	for i := 0; i < len(m.detailText())/m.detailPageSize()+1; i++ {
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	content := m.View().Content
	if !strings.Contains(strings.ReplaceAll(content, "\n", ""), "FINAL_DETAIL_MARKER") {
		t.Fatal("the end of a long error was not reachable")
	}
	if strings.ContainsRune(content, '\x1b') {
		t.Fatal("details view emitted an untrusted escape character")
	}
	uiAssertFits(t, content, 60, 14)
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if strings.Contains(m.View().Content, "temo | Details") {
		t.Fatal("Escape did not close details")
	}
}

func TestUIFormErrorDetailsPreserveUnsavedInput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := uiTestModel(uiTestEntry("Original"))
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	// This retry scenario can establish that the failed save left disk unchanged.
	m.reload = func() (Config, error) { return cloneUIConfig(m.cfg), nil }
	m.persist = func(cfg Config) error {
		if cfg.Entries[0].Name != "Renamed" {
			t.Fatal("save did not receive the edited name")
		}
		return errors.New("\x1b[31m" + strings.Repeat("diagnostic ", 600) + "FINAL_FORM_ERROR")
	}
	m, check := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
	m = uiFinish(t, m, check)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "Renamed"})
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, save)
	m = uiInput(t, m, tea.WindowSizeMsg{Width: 50, Height: 14})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if !strings.Contains(m.View().Content, "temo | Details") {
		t.Fatal("Ctrl+D did not open full form errors")
	}
	m = uiInput(t, m, tea.PasteMsg{Content: "must not edit the hidden form"})
	for i := 0; i < len(m.detailText())/m.detailPageSize()+1; i++ {
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	content := m.View().Content
	if !strings.Contains(strings.ReplaceAll(content, "\n", ""), "FINAL_FORM_ERROR") {
		t.Fatal("the end of a long form error was not reachable")
	}
	if strings.ContainsRune(content, '\x1b') {
		t.Fatal("form error emitted an untrusted escape character")
	}
	uiAssertFits(t, content, 50, 14)
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !strings.Contains(m.View().Content, "Edit route") || !strings.Contains(m.View().Content, "Renamed") {
		t.Fatal("returning from the error lost the unsaved form")
	}
	m, retry := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	uiFinish(t, m, retry)
}

func TestUICancelDoesNotPersistChanges(t *testing.T) {
	for _, editing := range []bool{false, true} {
		t.Run(fmt.Sprintf("editing=%v", editing), func(t *testing.T) {
			m := uiTestModel(uiTestEntry("Original"))
			m.do = func(Entry, string) Result { return Result{State: "stopped"} }
			m.persist = func(Config) error {
				t.Fatal("cancelled form reached persistence")
				return nil
			}
			if editing {
				var cmd tea.Cmd
				m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
				m = uiFinish(t, m, cmd)
			} else {
				m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
			}
			m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			m = uiInput(t, m, tea.PasteMsg{Content: "Unsaved"})
			m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
			content := m.View().Content
			if !strings.Contains(content, "Original") || strings.Contains(content, "Unsaved") {
				t.Fatal("cancelling a form changed the route list")
			}
		})
	}
}

func TestUIStaleCompletionCannotUnlockOrOverwriteNewOperation(t *testing.T) {
	m := uiTestModel(uiTestEntry("Alpha"))
	state := "stopped"
	m.do = func(Entry, string) Result { return Result{State: state} }
	m, first := uiStep(t, m, tea.KeyPressMsg{Code: 'r'})
	if first == nil {
		t.Fatal("status command was not scheduled")
	}
	oldResult := first()
	m = uiInput(t, m, oldResult)
	state = "ready"
	m, second := uiStep(t, m, tea.KeyPressMsg{Code: 's'})
	m = uiInput(t, m, oldResult)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'q'})
	m = uiFinish(t, m, second)
	m = uiInput(t, m, oldResult)
	if !uiHasTableRow(m.View().Content, "Alpha", "setup ready", true) || uiHasTableRow(m.View().Content, "Alpha", "stopped", false) {
		t.Fatal("a stale result replaced the completed operation's state")
	}
}

func uiAssertFits(t *testing.T, content string, width, height int) {
	t.Helper()
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		t.Fatalf("view has %d lines, available height is %d", len(lines), height)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > width {
			t.Fatalf("line exceeds %d columns: %q", width, line)
		}
	}
}

// uiHasTableRow checks the current row, so a selected-route detail or notice
// cannot satisfy an assertion about the table's selection or status.
func uiHasTableRow(content, name, state string, selected bool) bool {
	for _, line := range strings.Split(ansi.Strip(content), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "│") {
			continue
		}
		row := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "│"))
		if selected && !strings.HasPrefix(row, "> ") {
			continue
		}
		row = strings.TrimSpace(strings.TrimPrefix(row, ">"))
		if strings.HasPrefix(row, name+" ") && strings.Contains(strings.TrimPrefix(row, name+" "), state) {
			return true
		}
	}
	return false
}
