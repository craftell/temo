package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func (m model) modalSize() (int, int) {
	width, height := uiModalSize(m.width, m.height)
	if m.editor != nil && (m.details || m.editor.picker != nil) {
		width = max(1, width-4)
		height = max(1, height-2)
	}
	return width, height
}

func uiModalSize(width, height int) (int, int) {
	return max(1, min(88, width-8)), max(1, min(20, height-4))
}

func (m model) detailPageSize() int {
	_, height := m.modalSize()
	return max(1, height-2)
}

func uiOverlayModal(background, content []string, width, height int) []string {
	innerWidth, innerHeight := uiModalSize(width, height)
	return uiOverlayModalSize(background, content, width, height, innerWidth, innerHeight)
}

func uiOverlayModalSize(background, content []string, width, height, innerWidth, innerHeight int) []string {
	boxHeight := min(innerHeight, len(content))
	frameWidth := innerWidth + 4
	frame := []string{"┌" + strings.Repeat("─", frameWidth-2) + "┐"}
	for _, line := range content[:boxHeight] {
		frame = append(frame, "│ "+uiTableCell(line, innerWidth)+" │")
	}
	frame = append(frame, "└"+strings.Repeat("─", frameWidth-2)+"┘")
	left, top := max(0, (width-frameWidth)/2), max(0, (height-len(frame))/2)
	lines := make([]string, height)
	for y := range lines {
		if y < len(background) {
			lines[y] = uiTableCell(background[y], width)
		} else {
			lines[y] = strings.Repeat(" ", width)
		}
		if y >= top && y < top+len(frame) {
			// Cut by terminal cells, not bytes or runes. Padding also repairs a wide
			// background glyph intersected by the opaque frame's left edge.
			prefix := uiTableCell(ansi.Cut(lines[y], 0, left), left)
			if strings.Contains(prefix, "\x1b[") {
				prefix += "\x1b[0m"
			}
			suffix := ansi.Cut(lines[y], left+frameWidth, width)
			lines[y] = prefix + frame[y-top] + suffix
		}
	}
	return lines
}

func (m model) modalLines() []string {
	background := m.listLines()
	if m.help && m.details {
		background = uiOverlayModal(background, m.helpLines(), m.width, m.height)
	}
	if m.editor != nil {
		editor := *m.editor
		editor.picker = nil
		base := m
		base.editor, base.details = &editor, false
		background = uiOverlayModal(background, base.formLines(), m.width, m.height)
	}
	var content []string
	switch {
	case m.details:
		content = m.detailLines()
	case m.deletion != nil:
		content = m.deleteLines()
	case m.editor != nil && m.editor.picker != nil:
		content = m.formLines()
	case m.editor != nil:
		return background
	case m.help:
		content = m.helpLines()
	default:
		return background
	}
	width, height := m.modalSize()
	return uiOverlayModalSize(background, content, m.width, m.height, width, height)
}

func (m model) helpLines() []string {
	return []string{
		"temo help",
		"↑/↓ j/k select; ←/→ 1/2 switch tabs",
		"s Start  x Stop  o Open  r Refresh all",
		"a Add  e Edit  D Delete  Ctrl+P SSH hosts",
		"d/Enter Details  p Config location",
		"/ Search  Esc clear/close  ? close help",
		"Form: ↑/↓ Tab/Shift+Tab fields; j/k type",
		"Ctrl+S save  Ctrl+D error  Esc cancel",
		"Setup ready ≠ application health.",
		"q/Ctrl+C quit; started routes persist.",
	}
}
