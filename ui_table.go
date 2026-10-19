package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type uiRouteRow struct {
	name       string
	status     string
	sshHost    string
	listen     string
	targetHost string
	targetPort string
	url        string
	detail     string
}

func newUIRouteRow(entry Entry, result Result) uiRouteRow {
	host := entry.SSHHost
	if host == "" {
		host = "local"
	}
	port := entry.LocalPort
	if entry.Kind == "serve" {
		port = entry.ServePort
	}
	return uiRouteRow{
		name:       safeText(entry.Name),
		status:     uiState(result),
		sshHost:    safeText(host),
		listen:     ":" + strconv.Itoa(port),
		targetHost: safeText(entry.TargetHost),
		targetPort: strconv.Itoa(entry.TargetPort),
		url:        safeText(result.URL),
		detail:     safeText(result.Detail),
	}
}

type uiTableLayout struct {
	width   int
	compact bool
	name    int
	status  int
	host    int
	listen  int
	target  int
}

func newUITableLayout(width int) uiTableLayout {
	layout := uiTableLayout{width: width, compact: width < 80, status: 23, listen: 6}
	if layout.compact {
		layout.name = width - 8 - layout.status
		available := width - 16
		layout.host = available / 3
		layout.target = available - layout.host
	} else {
		available := width - 14 - layout.status - layout.listen
		layout.name = available * 2 / 5
		layout.host = available / 4
		layout.target = available - layout.name - layout.host
	}
	return layout
}

func (m model) tablePageSize() int {
	if m.width < 80 {
		return max(1, (m.height-12)/2)
	}
	return max(1, m.height-11)
}

type uiTablePalette struct {
	color bool
}

func newUITablePalette() uiTablePalette {
	return uiTablePalette{color: os.Getenv("NO_COLOR") == "" && strings.ToLower(os.Getenv("TERM")) != "dumb"}
}

func (p uiTablePalette) paint(text, style string) string {
	if !p.color || style == "" {
		return text
	}
	start := "\x1b[" + style + "m"
	return start + strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+start) + "\x1b[0m"
}

func uiTableCell(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func (layout uiTableLayout) frame(content string, p uiTablePalette, selected bool) string {
	content = uiTableCell(content, layout.width-4)
	if selected {
		content = p.paint(content, "37;44")
	}
	return p.paint("│ ", "90") + content + p.paint(" │", "90")
}

func (layout uiTableLayout) rule(left, right, label string, p uiTablePalette) string {
	label = ansi.Truncate(label, max(0, layout.width-4), "…")
	return p.paint(left+"─"+label+strings.Repeat("─", max(0, layout.width-3-ansi.StringWidth(label)))+right, "36")
}

func (layout uiTableLayout) header(kind string, p uiTablePalette) []string {
	listen := "LOCAL"
	if kind == "serve" {
		listen = "HTTPS"
	}
	nameAndStatus := "  " + uiTableCell("NAME", layout.name) + "  " + uiTableCell("STATUS", layout.status)
	endpoints := uiTableCell("SSH HOST", layout.host) + "  " + uiTableCell(listen, layout.listen) + "  " + uiTableCell("TARGET", layout.target)
	if layout.compact {
		return []string{
			layout.frame(p.paint(nameAndStatus, "1;36"), p, false),
			layout.frame(p.paint("  "+endpoints, "1;36"), p, false),
		}
	}
	return []string{layout.frame(p.paint(nameAndStatus+"  "+endpoints, "1;36"), p, false)}
}

func uiTableStateStyle(state string) string {
	switch state {
	case "setup ready":
		return "92"
	case "error":
		return "91"
	case "stopped; cleanup needed":
		return "93"
	case "checking", "queued", "starting", "stopping":
		return "96"
	default:
		return "90"
	}
}

func (row uiRouteRow) target(width int) string {
	endpoint := net.JoinHostPort(row.targetHost, row.targetPort)
	if ansi.StringWidth(endpoint) <= width {
		return uiTableCell(endpoint, width)
	}
	// Keep the port visible when a hostname or IPv6 address exceeds its cell.
	suffix := ":" + row.targetPort
	host := ansi.Truncate(row.targetHost, max(1, width-ansi.StringWidth(suffix)), "…")
	return uiTableCell(host+suffix, width)
}

func (layout uiTableLayout) row(row uiRouteRow, p uiTablePalette, selected bool) []string {
	marker := "  "
	if selected {
		marker = "> "
	}
	status := p.paint(uiTableCell(row.status, layout.status), uiTableStateStyle(row.status))
	nameAndStatus := marker + uiTableCell(row.name, layout.name) + "  " + status
	endpoints := uiTableCell(row.sshHost, layout.host) + "  " +
		p.paint(uiTableCell(row.listen, layout.listen), "1;36") + "  " +
		p.paint(row.target(layout.target), "36")
	if layout.compact {
		return []string{
			layout.frame(nameAndStatus, p, selected),
			layout.frame("  "+endpoints, p, selected),
		}
	}
	return []string{layout.frame(nameAndStatus+"  "+endpoints, p, selected)}
}

func (m model) listLines() []string {
	indexes := m.visible()
	rows := make([]uiRouteRow, len(indexes))
	for i, index := range indexes {
		entry := m.cfg.Entries[index]
		rows[i] = newUIRouteRow(entry, m.states[entry.ID])
		rows[i].status = m.entryState(entry.ID)
	}
	layout, palette := newUITableLayout(m.width), newUITablePalette()
	ssh, serve := "[1 SSH]", "[2 Serve]"
	if m.kind == "serve" {
		serve = palette.paint("> "+serve, "1;36")
	} else {
		ssh = palette.paint("> "+ssh, "1;36")
	}
	tabs := ssh + "   " + serve
	config := "p config location"
	tabs += strings.Repeat(" ", max(2, m.width-ansi.StringWidth(tabs)-len(config))) + config
	title := palette.paint("temo", "1;36") + " | SSH forwarding and Tailscale Serve"
	label := fmt.Sprintf(" %s routes [%d]  / filter ", strings.ToUpper(m.kind), len(rows))
	if m.searching {
		label = " Search: " + m.query.view(max(1, m.width-14), true) + " "
	} else if len(m.query.value) > 0 {
		label = " Filter: " + safeText(string(m.query.value)) + " (Esc clears) "
	}
	lines := []string{title, tabs, layout.rule("┌", "┐", label, palette)}
	lines = append(lines, layout.header(m.kind, palette)...)

	page := m.tablePageSize()
	cursor := uiClamp(m.cursor, 0, len(rows)-1)
	start := max(0, cursor-page+1)
	bodyHeight := m.height - 11
	if layout.compact {
		bodyHeight = m.height - 12
	}
	body := make([]string, 0, max(1, bodyHeight))
	for i := start; i < min(len(rows), start+page); i++ {
		body = append(body, layout.row(rows[i], palette, i == cursor)...)
	}
	if len(rows) == 0 {
		body = append(body, layout.frame("No matching routes. Press a to add one.", palette, false))
	}
	for len(body) < bodyHeight {
		body = append(body, layout.frame("", palette, false))
	}
	lines = append(lines, body...)
	rangeLabel := " No routes "
	if len(rows) > 0 {
		rangeLabel = fmt.Sprintf(" %d-%d of %d ", start+1, min(len(rows), start+page), len(rows))
		if layout.compact {
			rangeLabel += "compact "
		}
	}
	lines = append(lines, layout.rule("└", "┘", rangeLabel, palette))
	url, detail := "No route selected.", "Press a to add a route."
	if len(rows) > 0 {
		url, detail = rows[cursor].url, rows[cursor].detail
		if url == "" {
			url = "Unavailable until setup is ready."
		}
		if detail == "" {
			detail = "d opens full route details."
		}
	}
	lines = append(lines,
		"URL: "+url,
		"Detail (d for full): "+detail,
		palette.paint("Application reachability is unverified.", "90"),
		m.statusLine(),
		"↑/↓ j/k move  s start  x stop  r refresh",
		"a add  e edit  o open  d details  ? help  q quit",
	)
	return lines
}
