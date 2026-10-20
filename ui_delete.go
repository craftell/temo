package main

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type uiDeleteDialog struct {
	entry   Entry
	confirm bool
	offset  int
	err     string
}

type uiDeleteDone struct {
	ticket    uint64
	entry     Entry
	cfg       Config
	checked   *Result
	err       error
	reloaded  *Config
	uncertain bool
}

type uiReloadDone struct {
	ticket uint64
	cfg    Config
	err    error
}

// Save may have renamed config.json before a directory fsync failed. Only a
// subsequent Load can establish the actual persisted definitions after error.
func uiPersistConfig(cfg Config, persist func(Config) error, reload func() (Config, error)) (error, *Config, bool) {
	err := persist(cfg)
	if err == nil {
		return nil, nil, false
	}
	if reload == nil {
		return fmt.Errorf("%w; disk state is unknown; reload configuration before further changes", err), nil, true
	}
	actual, loadErr := reload()
	if loadErr != nil {
		return fmt.Errorf("%w; configuration reload failed: %v; further changes are blocked", err, loadErr), nil, true
	}
	actual = cloneUIConfig(actual)
	return fmt.Errorf("%w; configuration reloaded from disk; review it before retrying", err), &actual, false
}

func (m *model) adoptReload(cfg Config) {
	if m.refresh.cancel != nil {
		m.refresh.cancel()
	}
	for id := range m.revisions {
		m.revisions[id]++
	}
	m.refresh = uiRefresh{}
	m.cfg = cloneUIConfig(cfg)
	m.states = make(map[string]Result)
	m.cursor = uiClamp(m.cursor, 0, len(m.visible())-1)
}

func (m model) reloadConfig() (tea.Model, tea.Cmd) {
	if m.busy != "" {
		return m, nil
	}
	if m.reload == nil {
		m.notice = "Configuration reload is unavailable. Restart before making changes."
		return m, nil
	}
	m.ticket++
	m.busy = "reloading configuration"
	ticket, reload := m.ticket, m.reload
	return m, func() tea.Msg { cfg, err := reload(); return uiReloadDone{ticket: ticket, cfg: cfg, err: err} }
}

func (m model) openDelete() (tea.Model, tea.Cmd) {
	if m.width < 50 || m.height < 14 {
		m.notice = "Resize to at least 50 columns and 14 rows before deleting."
		return m, nil
	}
	if m.busy != "" || m.configUncertain {
		return m, nil
	}
	entry, ok := m.selected()
	if !ok {
		m.notice = "Select a route to delete its definition."
		return m, nil
	}
	if _, pending := m.refresh.pending[entry.ID]; pending {
		m.notice = "Status check is still running for " + entry.Name + ". Try again when it finishes."
		return m, nil
	}
	m.deletion = &uiDeleteDialog{entry: entry}
	m.notice = ""
	return m, nil
}

func (m model) updateDeleteKey(k string) (tea.Model, tea.Cmd) {
	if m.busy != "" {
		m.notice = "Wait for deletion to finish."
		return m, nil
	}
	switch k {
	case "esc", "n":
		m.deletion = nil
		m.notice = "Deletion cancelled."
		if m.startupWait {
			m.startupWait = false
			cmd := m.startRefresh()
			return m, cmd
		}
	case "q":
		return m.quit()
	case "down", "j", "pgdown":
		m.deletion.offset++
	case "up", "k", "pgup":
		m.deletion.offset = max(0, m.deletion.offset-1)
	case "tab", "shift+tab", "left", "right":
		m.deletion.confirm = !m.deletion.confirm
	case "enter":
		if !m.deletion.confirm {
			return m.updateDeleteKey("esc")
		}
		return m.confirmDelete()
	case "y":
		return m.confirmDelete()
	}
	return m, nil
}

func (m model) confirmDelete() (tea.Model, tea.Cmd) {
	if m.deletion == nil || m.busy != "" {
		return m, nil
	}
	if m.width < 50 || m.height < 14 {
		m.deletion.confirm = false
		m.deletion.err = "Resize to at least 50 columns and 14 rows to review and confirm deletion."
		m.notice = m.deletion.err
		return m, nil
	}
	if m.configUncertain {
		m.deletion.err = "Configuration state is uncertain. Cancel and press r to reload."
		return m, nil
	}
	entry := m.deletion.entry
	if _, pending := m.refresh.pending[entry.ID]; pending {
		m.deletion.err = "Status check is still running. Try again when it finishes."
		return m, nil
	}
	next := cloneUIConfig(m.cfg)
	found := false
	for i, current := range next.Entries {
		if current.ID != entry.ID {
			continue
		}
		if current != entry {
			m.deletion.err = "The definition changed. Cancel and reopen the dialog."
			return m, nil
		}
		next.Entries = append(next.Entries[:i], next.Entries[i+1:]...)
		found = true
		break
	}
	if !found {
		m.deletion.err = "The definition is already absent. Cancel to review the list."
		return m, nil
	}
	for i, queued := range m.refresh.queue {
		if queued.ID == entry.ID {
			m.refresh.queue = append(m.refresh.queue[:i], m.refresh.queue[i+1:]...)
			m.refresh.total--
			break
		}
	}
	m.revisions[entry.ID]++
	m.ticket++
	m.busy = "deleting " + entry.Name
	m.deletion.err = ""
	m.deletion.confirm = false
	ticket, run, persist, reload := m.ticket, m.do, m.persist, m.reload
	return m, func() tea.Msg {
		result := uiDeleteDone{ticket: ticket, entry: entry, cfg: next}
		state := run(entry, "status")
		result.checked = &state
		if state.State != "stopped" {
			result.err = errors.New("deletion requires a confirmed stopped route; cancel, press x to stop it, then retry")
			return result
		}
		result.err, result.reloaded, result.uncertain = uiPersistConfig(next, persist, reload)
		return result
	}
}

func (m model) finishDelete(msg uiDeleteDone) (tea.Model, tea.Cmd) {
	if msg.ticket != m.ticket || m.busy == "" || m.deletion == nil || m.deletion.entry != msg.entry {
		return m, nil
	}
	m.busy = ""
	if msg.checked != nil {
		m.states[msg.entry.ID] = *msg.checked
	}
	if msg.err != nil {
		if msg.reloaded != nil {
			m.adoptReload(*msg.reloaded)
		}
		m.configUncertain = msg.uncertain
		m.deletion.err = msg.err.Error()
		m.notice = "Deletion could not be confirmed. Review the error and current configuration."
		return m, nil
	}
	m.cfg = msg.cfg
	delete(m.states, msg.entry.ID)
	m.revisions[msg.entry.ID]++
	m.cursor = uiClamp(m.cursor, 0, len(m.visible())-1)
	m.deletion = nil
	m.notice = "Deleted definition " + msg.entry.Name + ". Ownership journals were not removed."
	if m.startupWait {
		m.startupWait = false
		cmd := m.startRefresh()
		return m, cmd
	}
	return m, nil
}

func (m model) deleteLines() []string {
	d := m.deletion
	if d == nil {
		return nil
	}
	buttons := "[ Cancel ]    Delete"
	if d.confirm {
		buttons = "  Cancel    [ Delete ]"
	}
	width, height := m.modalSize()
	lines := []string{"Delete definition?", "Name: " + safeText(d.entry.Name), "ID: " + safeText(d.entry.ID), "Requires stopped status; cancel, x to stop.", "Does not stop routes or remove journals."}
	message := "A fresh status check runs on confirmation."
	if d.err != "" {
		message = "Error: " + safeText(d.err)
	}
	if m.busy != "" {
		message = m.statusLine()
	}
	wrapped := strings.Split(ansi.Hardwrap(message, width, true), "\n")
	rows := max(1, height-len(lines)-2)
	offset := uiClamp(d.offset, 0, max(0, len(wrapped)-rows))
	lines = append(lines, wrapped[offset:min(len(wrapped), offset+rows)]...)
	if len(wrapped) > rows {
		lines[0] = "Delete definition? (↑↓ scroll error)"
	}
	return append(lines, buttons, "Tab/←/→ choose Enter/y act Esc/n cancel")
}
