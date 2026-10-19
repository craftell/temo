package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type model struct {
	cfg           Config
	states        map[string]Result
	do            func(Entry, string) Result
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
		cfg:    cloneUIConfig(cfg),
		states: make(map[string]Result),
		width:  100,
		height: 30,
		notice: "Started routes survive quitting. Press r to inspect the selected route.",
		do: func(Entry, string) Result {
			return Result{State: "error", Detail: "No route engine is available."}
		},
		persist: func(Config) error {
			return errors.New("no configuration store is available")
		},
	}
	if engine != nil {
		m.do = engine.Do
	}
	if store != nil {
		m.persist = store.Save
	}
	return m
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
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
		for i, entry := range m.cfg.Entries {
			if entry.ID == msg.entry.ID {
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
	if m.busy != "" {
		if k == "q" {
			m.notice = "Wait for the current operation before quitting."
		}
		return m, nil
	}
	if m.details {
		switch k {
		case "d", "enter", "esc":
			m.details = false
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
		switch k {
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
		case "tab", "enter":
			m.editor.focus = (m.editor.focus + 1) % len(m.editor.fields)
		case "shift+tab":
			m.editor.focus = (m.editor.focus + len(m.editor.fields) - 1) % len(m.editor.fields)
		default:
			m.editor.fields[m.editor.focus].edit(key)
			m.editor.err = ""
		}
		return m, nil
	}
	if m.searching {
		switch k {
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
		}
		return m, nil
	}
	switch k {
	case "q":
		return m.quit()
	case "?":
		m.help = true
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
		m.cursor = uiClamp(m.cursor+max(1, m.height-12), 0, len(m.visible())-1)
	case "pgup":
		m.cursor = uiClamp(m.cursor-max(1, m.height-12), 0, len(m.visible())-1)
	case "d", "enter":
		if _, ok := m.selected(); ok {
			m.details = true
			m.detailsOffset = 0
		}
	case "a":
		form := newUIEntryForm(Entry{
			ID: NewID(), Kind: "ssh", TargetHost: "127.0.0.1",
			TargetPort: 3000, LocalPort: 8080, ServePort: 8443,
		}, true)
		m.editor = &form
		m.notice = ""
	case "r", "e", "s", "x", "o":
		entry, ok := m.selected()
		if !ok {
			m.notice = "Select a route, or press a to add one."
			return m, nil
		}
		if k == "e" {
			return m.runEntry(entry, "status", true)
		}
		action := map[string]string{"r": "status", "s": "start", "x": "stop", "o": "open"}[k]
		return m.runEntry(entry, action, false)
	}
	return m, nil
}

func (m model) quit() (tea.Model, tea.Cmd) {
	if m.busy != "" {
		m.notice = "Wait for the current operation before quitting."
		return m, nil
	}
	return m, tea.Quit
}

func (m model) runEntry(entry Entry, action string, edit bool) (tea.Model, tea.Cmd) {
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
			"j/k or arrows  Select a route", "s  Start     x  Stop     o  Open URL",
			"r  Refresh the selected route's status", "a  Add       e  Edit a stopped route",
			"d or Enter  Read full details; j/k scroll",
			"/  Search    Enter keeps filter    Esc clears",
			"? or Esc  Close help", "q or Ctrl+C  Quit; started routes remain running",
			"", "Setup ready describes routing, not application health.",
			"Editing checks for stopped state before opening and saving.",
			"Only one operation runs at a time. Wait before quitting.",
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
	lines := []string{"temo | SSH forwarding and Tailscale Serve", fmt.Sprintf("%d of %d routes", len(indexes), len(m.cfg.Entries)), filter}
	rows := max(1, m.height-12)
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
			line = fmt.Sprintf("%s [%s] %s (%s)", marker, uiState(m.states[entry.ID]), safeText(entry.Name), safeText(entry.Kind))
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
		details[3] = "State: " + uiState(state)
		details[4] = "URL: " + safeText(state.URL)
		details[6] = "Detail (d for full): " + safeText(state.Detail)
	}
	lines = append(lines, details...)
	lines = append(lines, m.statusLine(), "j/k move  s start  x stop  o open  r status  a add  e edit  d details  ? help  q quit")
	return lines
}

func (m model) detailText() []string {
	var raw []string
	if m.editor != nil {
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
	return safeText(m.notice)
}

func uiState(result Result) string {
	switch result.State {
	case "":
		return "unknown"
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
	uiKind
	uiSSHHost
	uiTargetHost
	uiTargetPort
	uiLocalPort
	uiServePort
	uiFieldCount
)

type uiEntryForm struct {
	original Entry
	isNew    bool
	fields   [uiFieldCount]uiTextField
	focus    int
	err      string
}

func newUIEntryForm(entry Entry, isNew bool) uiEntryForm {
	form := uiEntryForm{original: entry, isNew: isNew}
	values := [...]string{entry.Name, entry.Kind, entry.SSHHost, entry.TargetHost, strconv.Itoa(entry.TargetPort), strconv.Itoa(entry.LocalPort), strconv.Itoa(entry.ServePort)}
	for i, value := range values {
		form.fields[i] = uiTextField{value: []rune(value), cursor: len([]rune(value))}
	}
	return form
}

func (f uiEntryForm) entry() (Entry, error) {
	value := func(i int) string { return strings.TrimSpace(string(f.fields[i].value)) }
	entry := Entry{ID: f.original.ID, Name: value(uiName), Kind: strings.ToLower(value(uiKind)), SSHHost: value(uiSSHHost), TargetHost: value(uiTargetHost)}
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
	title := "Edit route"
	if form.isNew {
		title = "Add route"
	}
	lines := []string{"temo | " + title, "Tab fields / Shift+Tab back / Ctrl+U clear", ""}
	labels := [...]string{"Name", "Kind [ssh/serve]", "SSH host", "Target host", "Target port", "Local port [ssh]", "HTTPS port [serve]"}
	for i, label := range labels {
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
	return append(lines, "Blank SSH host runs Serve on this machine.", "Only the selected kind's port is used.", message, "Ctrl+S save  Ctrl+D full error  Esc cancel")
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
