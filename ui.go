package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type model struct {
	cfg           Config
	states        map[string]Result
	kind          string
	tabs          map[string]uiTabState
	configPath    string
	configDetails bool
	refresh       uiRefresh
	revisions     map[string]uint64
	do            func(Entry, string) Result
	observe       func(context.Context, Entry) Result
	persist       func(Config) error
	width         int
	height        int
	cursor        int
	query         uiTextField
	searching     bool
	help          bool
	details       bool
	detailsOffset int
	editor        *uiEntryForm
	busy          string
	ticket        uint64
	notice        string
}

type uiTabState struct {
	cursor int
	query  uiTextField
}

type uiRefresh struct {
	ctx     context.Context
	cancel  context.CancelFunc
	queue   []Entry
	pending map[string]uint64
	total   int
	done    int
	errors  int
}

type uiRefreshStart struct{}

type uiObserved struct {
	entry    Entry
	revision uint64
	result   Result
}

const uiRefreshLimit = 4

type uiRouteDone struct {
	ticket uint64
	entry  Entry
	action string
	edit   bool
	result Result
}

type uiSaveDone struct {
	ticket  uint64
	cfg     Config
	entry   Entry
	checked *Result
	err     error
}

func NewModel(cfg Config, store *Store, engine *Engine) model {
	m := model{
		cfg:        cloneUIConfig(cfg),
		states:     make(map[string]Result),
		kind:       "ssh",
		tabs:       make(map[string]uiTabState),
		revisions:  make(map[string]uint64),
		configPath: "Unavailable (no configuration store)",
		width:      100,
		height:     30,
		notice:     "Checking saved routes. Started routes survive quitting.",
		do: func(Entry, string) Result {
			return Result{State: "error", Detail: "No route engine is available."}
		},
		persist: func(Config) error {
			return errors.New("no configuration store is available")
		},
	}
	if engine != nil {
		m.do = engine.Do
		m.observe = engine.Observe
	}
	if store != nil {
		m.persist = store.Save
		m.configPath = filepath.Join(store.Dir, "config.json")
	}
	return m
}

func (m model) Init() tea.Cmd {
	return func() tea.Msg { return uiRefreshStart{} }
}

func (m *model) startRefresh() tea.Cmd {
	if m.refresh.done < m.refresh.total {
		m.notice = "Status checks are already running."
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.refresh = uiRefresh{
		ctx:     ctx,
		cancel:  cancel,
		queue:   append([]Entry(nil), m.cfg.Entries...),
		pending: make(map[string]uint64),
		total:   len(m.cfg.Entries),
	}
	m.notice = ""
	return m.fillRefresh()
}

func (m *model) fillRefresh() tea.Cmd {
	var commands []tea.Cmd
	for len(m.refresh.pending) < uiRefreshLimit && len(m.refresh.queue) > 0 {
		entry := m.refresh.queue[0]
		m.refresh.queue = m.refresh.queue[1:]
		m.revisions[entry.ID]++
		revision, run, observe, ctx := m.revisions[entry.ID], m.do, m.observe, m.refresh.ctx
		m.refresh.pending[entry.ID] = revision
		commands = append(commands, func() tea.Msg {
			result := Result{}
			if observe != nil {
				result = observe(ctx, entry)
			} else {
				result = run(entry, "status")
			}
			return uiObserved{entry: entry, revision: revision, result: result}
		})
	}
	if m.refresh.done == m.refresh.total {
		m.refresh.cancel()
		m.notice = fmt.Sprintf("Checked %d routes; %d errors. Application reachability is unverified.", m.refresh.total, m.refresh.errors)
	}
	return tea.Batch(commands...)
}

func (m *model) switchTab(kind string) {
	if m.kind == kind {
		return
	}
	m.tabs[m.kind] = uiTabState{cursor: m.cursor, query: m.query}
	m.kind = kind
	m.cursor, m.query = m.tabs[kind].cursor, m.tabs[kind].query
	m.searching = false
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case uiRefreshStart:
		cmd := m.startRefresh()
		return m, cmd
	case uiObserved:
		if m.refresh.pending[msg.entry.ID] != msg.revision {
			return m, nil
		}
		delete(m.refresh.pending, msg.entry.ID)
		m.refresh.done++
		if msg.result.State == "error" {
			m.refresh.errors++
		}
		if m.revisions[msg.entry.ID] == msg.revision {
			m.states[msg.entry.ID] = msg.result
		}
		cmd := m.fillRefresh()
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	case tea.PasteMsg:
		if m.busy != "" || m.details || m.help {
			return m, nil
		}
		if m.editor != nil {
			if m.editor.picker != nil {
				return m, nil
			}
			m.editor.fields[m.editor.focus].insert(msg.Content)
			m.editor.err = ""
		} else if m.searching {
			m.query.insert(msg.Content)
			m.cursor = 0
		}
	case uiRouteDone:
		if msg.ticket != m.ticket || m.busy == "" {
			return m, nil
		}
		m.busy = ""
		m.states[msg.entry.ID] = msg.result
		if msg.edit {
			if msg.result.State == "stopped" {
				m.switchTab(msg.entry.Kind)
				form := newUIEntryForm(msg.entry, false)
				m.editor = &form
				m.notice = ""
			} else {
				m.notice = "Editing requires a stopped route. Stop it and try again; d shows details."
			}
		} else {
			m.notice = fmt.Sprintf("%s %s: %s", msg.action, msg.entry.Name, msg.result.Detail)
		}
	case uiSaveDone:
		if msg.ticket != m.ticket || m.busy == "" {
			return m, nil
		}
		m.busy = ""
		if msg.checked != nil {
			m.states[msg.entry.ID] = *msg.checked
		}
		if msg.err != nil {
			if m.editor != nil {
				m.editor.err = msg.err.Error()
			}
			m.notice = "Save could not be confirmed. Inspect the error before retrying."
			return m, nil
		}
		m.cfg = msg.cfg
		m.editor = nil
		m.query = uiTextField{}
		m.searching = false
		for i, index := range m.visible() {
			if m.cfg.Entries[index].ID == msg.entry.ID {
				m.cursor = i
				break
			}
		}
		m.states[msg.entry.ID] = Result{State: "unknown", Detail: "Settings saved. Press r to inspect status."}
		m.notice = "Saved " + msg.entry.Name + "."
	}
	return m, nil
}

func (m model) updateKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := key.String()
	if k == "ctrl+c" {
		return m.quit()
	}
	if m.busy != "" && m.editor != nil && !m.details {
		m.notice = "Wait for the current operation before editing."
		return m, nil
	}
	if m.details {
		switch k {
		case "d", "p", "enter", "esc":
			m.details = false
			m.configDetails = false
		case "q":
			return m.quit()
		case "j", "down":
			m.detailsOffset++
		case "k", "up":
			m.detailsOffset--
		case "pgdown":
			m.detailsOffset += max(1, m.height-2)
		case "pgup":
			m.detailsOffset -= max(1, m.height-2)
		}
		m.detailsOffset = uiClamp(m.detailsOffset, 0, max(0, len(m.detailText())-max(1, m.height-2)))
		return m, nil
	}
	if m.editor != nil {
		if picker := m.editor.picker; picker != nil {
			switch k {
			case "esc", "ctrl+p":
				m.editor.picker = nil
			case "j", "down":
				picker.cursor = uiClamp(picker.cursor+1, 0, len(picker.names)-1)
			case "k", "up":
				picker.cursor = uiClamp(picker.cursor-1, 0, len(picker.names)-1)
			case "pgdown":
				picker.cursor = uiClamp(picker.cursor+max(1, m.height-5), 0, len(picker.names)-1)
			case "pgup":
				picker.cursor = uiClamp(picker.cursor-max(1, m.height-5), 0, len(picker.names)-1)
			case "enter":
				if len(picker.names) > 0 {
					name := []rune(picker.names[picker.cursor])
					m.editor.fields[uiSSHHost] = uiTextField{value: name, cursor: len(name)}
					m.editor.focus = uiSSHHost
					m.editor.err = ""
					m.editor.picker = nil
				}
			}
			return m, nil
		}
		switch k {
		case "ctrl+p":
			picker := &uiHostPicker{names: configuredSSHHosts()}
			for i, name := range picker.names {
				if strings.EqualFold(name, string(m.editor.fields[uiSSHHost].value)) {
					picker.cursor = i
					break
				}
			}
			m.editor.picker = picker
		case "esc":
			m.editor = nil
			m.notice = "Edit cancelled."
		case "ctrl+s":
			return m.saveEditor()
		case "ctrl+d":
			if m.editor.err != "" {
				m.details = true
				m.detailsOffset = 0
			}
		case "tab", "enter", "down":
			m.editor.move(1)
		case "shift+tab", "up":
			m.editor.move(-1)
		default:
			m.editor.fields[m.editor.focus].edit(key)
			m.editor.err = ""
		}
		return m, nil
	}
	if m.searching {
		switch k {
		case "up", "down":
			m.searching = false
			if k == "down" {
				m.cursor = uiClamp(m.cursor+1, 0, len(m.visible())-1)
			} else {
				m.cursor = uiClamp(m.cursor-1, 0, len(m.visible())-1)
			}
			return m, nil
		case "enter":
			m.searching = false
		case "esc":
			m.searching = false
			m.query = uiTextField{}
		default:
			m.query.edit(key)
		}
		m.cursor = 0
		return m, nil
	}
	if m.help {
		switch k {
		case "?", "esc":
			m.help = false
		case "q":
			return m.quit()
		case "p":
			m.help = false
			m.details, m.configDetails, m.detailsOffset = true, true, 0
		}
		return m, nil
	}
	if m.busy != "" && strings.Contains("aersxo", k) && len(k) == 1 {
		m.notice = "Wait for the current operation before starting another."
		return m, nil
	}
	switch k {
	case "q":
		return m.quit()
	case "?":
		m.help = true
	case "left", "right":
		if m.kind == "ssh" {
			m.switchTab("serve")
		} else {
			m.switchTab("ssh")
		}
	case "1":
		m.switchTab("ssh")
	case "2":
		m.switchTab("serve")
	case "p":
		m.details, m.configDetails, m.detailsOffset = true, true, 0
	case "r":
		cmd := m.startRefresh()
		return m, cmd
	case "/":
		m.searching = true
		m.query.cursor = len(m.query.value)
	case "esc":
		m.query = uiTextField{}
		m.cursor = 0
	case "j", "down":
		m.cursor = uiClamp(m.cursor+1, 0, len(m.visible())-1)
	case "k", "up":
		m.cursor = uiClamp(m.cursor-1, 0, len(m.visible())-1)
	case "pgdown":
		m.cursor = uiClamp(m.cursor+max(1, m.height-13), 0, len(m.visible())-1)
	case "pgup":
		m.cursor = uiClamp(m.cursor-max(1, m.height-13), 0, len(m.visible())-1)
	case "d", "enter":
		if _, ok := m.selected(); ok {
			m.details = true
			m.detailsOffset = 0
		}
	case "a":
		form := newUIEntryForm(Entry{
			ID: NewID(), Kind: m.kind, TargetHost: "127.0.0.1",
			TargetPort: 3000, LocalPort: 8080, ServePort: 8443,
		}, true)
		m.editor = &form
		m.notice = ""
	case "e", "s", "x", "o":
		entry, ok := m.selected()
		if !ok {
			m.notice = "Select a route, or press a to add one."
			return m, nil
		}
		if k == "e" {
			return m.runEntry(entry, "status", true)
		}
		action := map[string]string{"s": "start", "x": "stop", "o": "open"}[k]
		return m.runEntry(entry, action, false)
	}
	return m, nil
}

func (m model) quit() (tea.Model, tea.Cmd) {
	if m.busy != "" {
		m.notice = "Wait for the current operation before quitting."
		return m, nil
	}
	if m.refresh.cancel != nil {
		m.refresh.cancel()
	}
	return m, tea.Quit
}

func (m model) runEntry(entry Entry, action string, edit bool) (tea.Model, tea.Cmd) {
	if _, pending := m.refresh.pending[entry.ID]; pending {
		m.notice = "Status check is still running for " + entry.Name + ". Try again when it finishes."
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
	m.busy = action + " " + entry.Name
	m.notice = ""
	ticket, run := m.ticket, m.do
	return m, func() tea.Msg {
		return uiRouteDone{ticket: ticket, entry: entry, action: action, edit: edit, result: run(entry, action)}
	}
}

func (m model) saveEditor() (tea.Model, tea.Cmd) {
	entry, err := m.editor.entry()
	if err != nil {
		m.editor.err = err.Error()
		return m, nil
	}
	next := cloneUIConfig(m.cfg)
	original, isNew := m.editor.original, m.editor.isNew
	if isNew {
		next.Entries = append(next.Entries, entry)
	} else {
		found := false
		for i := range next.Entries {
			if next.Entries[i].ID == entry.ID {
				next.Entries[i] = entry
				found = true
				break
			}
		}
		if !found {
			m.editor.err = "The original route is missing. Cancel and reopen the editor."
			return m, nil
		}
	}
	m.revisions[entry.ID]++
	m.ticket++
	m.busy = "saving " + entry.Name
	m.editor.err = ""
	m.notice = ""
	ticket, run, persist := m.ticket, m.do, m.persist
	return m, func() tea.Msg {
		result := uiSaveDone{ticket: ticket, cfg: next, entry: entry}
		if !isNew {
			state := run(original, "status")
			result.checked = &state
			if state.State != "stopped" {
				result.err = errors.New("the route is no longer confirmed stopped; cancel, stop it, and reopen the editor")
				return result
			}
		}
		result.err = persist(next)
		return result
	}
}

func (m model) visible() []int {
	query := strings.ToLower(strings.TrimSpace(string(m.query.value)))
	indexes := make([]int, 0, len(m.cfg.Entries))
	for i, entry := range m.cfg.Entries {
		if entry.Kind != m.kind {
			continue
		}
		text := strings.ToLower(strings.Join([]string{entry.Name, entry.Kind, entry.SSHHost, entry.TargetHost}, " "))
		if query == "" || strings.Contains(text, query) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (m model) selected() (Entry, bool) {
	indexes := m.visible()
	if len(indexes) == 0 {
		return Entry{}, false
	}
	return m.cfg.Entries[indexes[uiClamp(m.cursor, 0, len(indexes)-1)]], true
}

func (m model) View() tea.View {
	var lines []string
	switch {
	case m.width < 50 || m.height < 14:
		lines = []string{"temo", "Resize to at least 50 columns and 14 rows.", m.statusLine(), "q quits when idle; started routes persist."}
	case m.details:
		lines = m.detailLines()
	case m.editor != nil:
		lines = m.formLines()
	case m.help:
		lines = []string{
			"temo help", "",
			"Up/Down or j/k  Select a route",
			"Left/Right or 1/2  SSH / Serve tabs",
			"s Start  x Stop  o Open  r Refresh all",
			"a Add in this tab  e Edit  Ctrl+P SSH hosts",
			"Form: Up/Down or Tab/Shift+Tab fields; j/k type",
			"d or Enter Details; p Config file location",
			"/ Search; arrows return to list; Esc clear",
			"? or Esc Close help; q or Ctrl+C Quit",
			"Setup ready is routing, not application health.",
			"Refresh is read-only; navigation stays available.",
			"Stop releases retained ownership before editing.",
			"Started routes survive quitting.",
		}
	default:
		lines = m.listLines()
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func (m model) listLines() []string {
	indexes := m.visible()
	filter := "Press / to filter by name, kind, or host."
	if m.searching {
		filter = "Search: " + m.query.view(max(1, m.width-8), true)
	} else if len(m.query.value) > 0 {
		filter = "Filter: " + safeText(string(m.query.value)) + "  (Esc clears)"
	}
	tabs := "> [1 SSH]   [2 Serve]"
	if m.kind == "serve" {
		tabs = "[1 SSH]   > [2 Serve]"
	}
	lines := []string{"temo | SSH forwarding and Tailscale Serve", tabs + "  Left/Right switch", fmt.Sprintf("%d %s routes | p config location", len(indexes), m.kind), filter}
	rows := max(1, m.height-13)
	cursor := uiClamp(m.cursor, 0, len(indexes)-1)
	start := max(0, cursor-rows+1)
	for row := 0; row < rows; row++ {
		position := start + row
		line := ""
		if position < len(indexes) {
			entry := m.cfg.Entries[indexes[position]]
			marker := " "
			if position == cursor {
				marker = ">"
			}
			line = fmt.Sprintf("%s [%s] %s", marker, m.entryState(entry.ID), safeText(entry.Name))
		} else if row == 0 {
			line = "No matching routes. Press a to add one."
		}
		lines = append(lines, line)
	}
	details := []string{"", "No route selected.", "", "", "", "Application reachability is unverified.", ""}
	if entry, ok := m.selected(); ok {
		state := m.states[entry.ID]
		details[1] = safeText(entry.Name) + " [" + safeText(entry.Kind) + "]"
		details[2] = "Route: " + safeText(uiRouteSummary(entry))
		details[3] = "State: " + m.entryState(entry.ID)
		details[4] = "URL: " + safeText(state.URL)
		details[6] = "Detail (d for full): " + safeText(state.Detail)
	}
	lines = append(lines, details...)
	lines = append(lines, m.statusLine(), "↑/↓ j/k move  s start  x stop  r refresh  a add  e edit  ? help  q quit")
	return lines
}

func (m model) detailText() []string {
	var raw []string
	if m.configDetails {
		raw = []string{
			"Configuration file",
			safeText(m.configPath),
			"",
			"Saved definitions are stored in config.json.",
			"Private owner-<id>.json files track backend ownership.",
			"Starting or stopping a route does not delete its definition.",
			"Esc returns to the list.",
		}
	} else if m.editor != nil {
		raw = []string{
			"Form error",
			safeText(m.editor.err),
			"",
			"Press Esc to return to your unsaved form.",
		}
	} else {
		entry, ok := m.selected()
		if !ok {
			return []string{"No route selected."}
		}
		state := m.states[entry.ID]
		raw = []string{
			safeText(entry.Name) + " [" + safeText(entry.Kind) + "]",
			"Route: " + safeText(uiRouteSummary(entry)),
			"State: " + uiState(state),
			"URL: " + safeText(state.URL),
			"Application reachability is unverified.",
			"",
			"Detail: " + safeText(state.Detail),
		}
	}
	var lines []string
	for _, line := range raw {
		lines = append(lines, strings.Split(ansi.Hardwrap(line, max(1, m.width), true), "\n")...)
	}
	return lines
}

func (m model) detailLines() []string {
	content := m.detailText()
	rows := max(1, m.height-2)
	start := uiClamp(m.detailsOffset, 0, max(0, len(content)-rows))
	end := min(len(content), start+rows)
	lines := []string{fmt.Sprintf("temo | Details %d-%d of %d", start+1, end, len(content))}
	lines = append(lines, content[start:end]...)
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	return append(lines, "j/k scroll  PgUp/PgDown page  Esc return  q quit")
}

func (m model) statusLine() string {
	if m.busy != "" {
		text := "Working: " + safeText(m.busy)
		if m.notice != "" {
			text += " | " + safeText(m.notice)
		}
		return text
	}
	if m.refresh.done < m.refresh.total {
		return fmt.Sprintf("Refreshing %d/%d (all tabs). %s", m.refresh.done, m.refresh.total, safeText(m.notice))
	}
	return safeText(m.notice)
}

func (m model) entryState(id string) string {
	if _, pending := m.refresh.pending[id]; pending {
		return "checking"
	}
	for _, entry := range m.refresh.queue {
		if entry.ID == id {
			return "queued"
		}
	}
	return uiState(m.states[id])
}

func uiState(result Result) string {
	switch result.State {
	case "":
		return "unknown"
	case "inactive":
		return "stopped; cleanup needed"
	case "ready", "running":
		return "setup ready"
	default:
		return safeText(result.State)
	}
}

func uiRouteSummary(entry Entry) string {
	target := net.JoinHostPort(entry.TargetHost, strconv.Itoa(entry.TargetPort))
	if entry.Kind == "ssh" {
		return fmt.Sprintf("127.0.0.1:%d -> %s -> %s", entry.LocalPort, entry.SSHHost, target)
	}
	host := entry.SSHHost
	if host == "" {
		host = "this machine"
	}
	return fmt.Sprintf("Serve HTTPS :%d on %s -> http://%s", entry.ServePort, host, target)
}

const (
	uiName = iota
	uiSSHHost
	uiTargetHost
	uiTargetPort
	uiLocalPort
	uiServePort
	uiFieldCount
)

type uiHostPicker struct {
	names  []string
	cursor int
}

type uiEntryForm struct {
	picker   *uiHostPicker
	original Entry
	isNew    bool
	fields   [uiFieldCount]uiTextField
	focus    int
	err      string
}

func newUIEntryForm(entry Entry, isNew bool) uiEntryForm {
	form := uiEntryForm{original: entry, isNew: isNew}
	values := [...]string{entry.Name, entry.SSHHost, entry.TargetHost, strconv.Itoa(entry.TargetPort), strconv.Itoa(entry.LocalPort), strconv.Itoa(entry.ServePort)}
	for i, value := range values {
		form.fields[i] = uiTextField{value: []rune(value), cursor: len([]rune(value))}
	}
	return form
}

func (f uiEntryForm) visibleFields() []int {
	if f.original.Kind == "serve" {
		return []int{uiName, uiSSHHost, uiTargetPort, uiServePort}
	}
	return []int{uiName, uiSSHHost, uiTargetHost, uiTargetPort, uiLocalPort}
}

func (f *uiEntryForm) move(delta int) {
	fields := f.visibleFields()
	for i, field := range fields {
		if field == f.focus {
			f.focus = fields[(i+delta+len(fields))%len(fields)]
			return
		}
	}
	f.focus = fields[0]
}

func (f uiEntryForm) entry() (Entry, error) {
	value := func(i int) string { return strings.TrimSpace(string(f.fields[i].value)) }
	entry := Entry{ID: f.original.ID, Name: value(uiName), Kind: f.original.Kind, SSHHost: value(uiSSHHost), TargetHost: value(uiTargetHost)}
	if entry.Kind != "ssh" && entry.Kind != "serve" {
		return Entry{}, errors.New("kind must be ssh or serve")
	}
	port := func(i int, label string) (int, error) {
		number, err := strconv.Atoi(value(i))
		if err != nil || number < 1 || number > 65535 {
			return 0, fmt.Errorf("%s must be a number from 1 to 65535", label)
		}
		return number, nil
	}
	var err error
	if entry.TargetPort, err = port(uiTargetPort, "target port"); err != nil {
		return Entry{}, err
	}
	if entry.Kind == "ssh" {
		entry.LocalPort, err = port(uiLocalPort, "local port")
	} else {
		entry.ServePort, err = port(uiServePort, "HTTPS port")
	}
	if err != nil {
		return Entry{}, err
	}
	return entry, entry.Validate()
}

func (m model) formLines() []string {
	form := m.editor
	if picker := form.picker; picker != nil {
		lines := []string{"temo | SSH hosts", fmt.Sprintf("%d configured hosts", len(picker.names)), ""}
		rows := max(1, m.height-5)
		start := max(0, picker.cursor-rows+1)
		for i := start; i < min(len(picker.names), start+rows); i++ {
			marker := "  "
			if i == picker.cursor {
				marker = "> "
			}
			lines = append(lines, marker+safeText(picker.names[i]))
		}
		if len(picker.names) == 0 {
			lines = append(lines, "No configured SSH hosts. Type a host in the form.")
		}
		return append(lines, "", "Up/Down or j/k select  Enter use  Esc cancel")
	}
	title := "Edit route"
	if form.isNew {
		title = "Add route"
	}
	lines := []string{"temo | " + title + " [" + form.original.Kind + "]", "↑/↓ or Tab/Shift+Tab fields; j/k type text", "Ctrl+U clear; Left/Right move text cursor"}
	labels := [...]string{"Name", "SSH host", "Target host", "Target port", "Local port", "HTTPS port"}
	for _, i := range form.visibleFields() {
		label := labels[i]
		marker := " "
		if i == form.focus {
			marker = ">"
		}
		prefix := fmt.Sprintf("%s %-18s ", marker, label)
		lines = append(lines, prefix+form.fields[i].view(max(1, m.width-ansi.StringWidth(prefix)), i == form.focus))
	}
	message := m.statusLine()
	if form.err != "" {
		message = "Error (Ctrl+D): " + safeText(form.err)
	}
	hint := "SSH host is required. Only SSH forwarding fields are shown."
	if form.original.Kind == "serve" {
		hint = "Blank SSH host = this machine. Target: localhost."
	}
	return append(lines, hint, message, "Ctrl+S save  Ctrl+P hosts  Ctrl+D error  Esc cancel")
}

type uiTextField struct {
	value  []rune
	cursor int
}

func (f *uiTextField) insert(text string) {
	room := 512 - len(f.value)
	insert := make([]rune, 0)
	for _, r := range text {
		if room <= 0 {
			break
		}
		if unicode.IsPrint(r) && !unicode.In(r, unicode.Cf) {
			insert = append(insert, r)
			room--
		}
	}
	f.cursor = uiClamp(f.cursor, 0, len(f.value))
	next := make([]rune, 0, len(f.value)+len(insert))
	next = append(next, f.value[:f.cursor]...)
	next = append(next, insert...)
	next = append(next, f.value[f.cursor:]...)
	f.value = next
	f.cursor += len(insert)
}

func (f *uiTextField) edit(key tea.KeyPressMsg) {
	f.cursor = uiClamp(f.cursor, 0, len(f.value))
	switch key.String() {
	case "left":
		f.cursor = max(0, f.cursor-1)
	case "right":
		f.cursor = min(len(f.value), f.cursor+1)
	case "home", "ctrl+a":
		f.cursor = 0
	case "end", "ctrl+e":
		f.cursor = len(f.value)
	case "backspace":
		if f.cursor > 0 {
			f.value = append(f.value[:f.cursor-1], f.value[f.cursor:]...)
			f.cursor--
		}
	case "delete":
		if f.cursor < len(f.value) {
			f.value = append(f.value[:f.cursor], f.value[f.cursor+1:]...)
		}
	case "ctrl+u":
		f.value = nil
		f.cursor = 0
	default:
		if key.Mod & ^tea.ModShift != 0 {
			return
		}
		text := key.Text
		if text == "" && unicode.IsPrint(key.Code) {
			text = string(key.Code)
		}
		f.insert(text)
	}
}

func (f uiTextField) view(width int, focused bool) string {
	if !focused {
		return ansi.Truncate(safeText(string(f.value)), width, "…")
	}
	cursor := uiClamp(f.cursor, 0, len(f.value))
	before := safeText(string(f.value[:cursor]))
	text := before + "│" + safeText(string(f.value[cursor:]))
	left := max(0, ansi.StringWidth(before)-width+1)
	return ansi.Cut(text, left, left+width)
}

func safeText(text string) string {
	var out strings.Builder
	for _, r := range text {
		if !unicode.IsPrint(r) || unicode.In(r, unicode.Cf) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func cloneUIConfig(cfg Config) Config {
	cfg.Entries = append([]Entry(nil), cfg.Entries...)
	return cfg
}

func uiClamp(value, low, high int) int {
	if high < low {
		return low
	}
	return min(max(value, low), high)
}
