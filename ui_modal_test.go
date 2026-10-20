package main

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestUIModalKeepsListBehindFrame(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := uiTestModel(uiTestEntry("Alpha"))
	m.width, m.height, m.help = 100, 30, true
	view := m.View().Content
	if !strings.Contains(view, "temo help") || !strings.Contains(view, "SSH forwarding and Tailscale Serve") || !strings.Contains(view, "[1 SSH]") {
		t.Fatalf("help must overlay, not replace, the list:\n%s", view)
	}
	if !strings.Contains(view, "┌") || !strings.Contains(view, "D Delete") {
		t.Fatalf("missing frame or delete hint:\n%s", view)
	}
	uiAssertFits(t, view, m.width, m.height)
}

func TestUIModalMinimumFormAndPicker(t *testing.T) {
	for _, kind := range []string{"ssh", "serve"} {
		for _, env := range []string{"color", "NO_COLOR", "dumb"} {
			t.Run(kind+"/"+env, func(t *testing.T) {
				t.Setenv("NO_COLOR", "")
				t.Setenv("TERM", "xterm-256color")
				if env == "NO_COLOR" {
					t.Setenv("NO_COLOR", "1")
				}
				if env == "dumb" {
					t.Setenv("TERM", "dumb")
				}
				m := uiTestModel(uiTestEntry("Alpha"))
				m.width, m.height = 50, 14
				entry := uiTestEntry("編集テスト")
				entry.Kind = kind
				form := newUIEntryForm(entry, true)
				m.editor = &form
				for _, field := range form.visibleFields() {
					form.focus = field
					view := m.View().Content
					uiAssertFits(t, view, 50, 14)
					labels := [...]string{"Name", "SSH host", "Target host", "Target port", "Local port", "HTTPS port"}
					if !strings.Contains(view, fmt.Sprintf("> %-18s ", labels[field])) || !strings.Contains(view, "^S save") || !strings.Contains(view, "Esc cancel") {
						t.Fatalf("focused field and actions must remain visible:\n%s", view)
					}
					if env != "color" && strings.ContainsRune(view, '\x1b') {
						t.Fatal("plain terminal emitted ANSI")
					}
				}
				form.picker = &uiHostPicker{names: []string{"one", "two", "three", "four", "five", "selected-host"}, cursor: 5}
				view := m.View().Content
				uiAssertFits(t, view, 50, 14)
				if !strings.Contains(view, "> selected-host") || !strings.Contains(view, "Esc cancel") {
					t.Fatalf("picker focus/footer missing:\n%s", view)
				}
			})
		}
	}
}

func TestUIModalUnicodeOverlayCells(t *testing.T) {
	background := make([]string, 14)
	for i := range background {
		background[i] = strings.Repeat("界", 25)
	}
	lines := uiOverlayModal(background, []string{"界e\u0301👩‍💻" + strings.Repeat("界", 50)}, 50, 14)
	for _, line := range lines {
		if !utf8.ValidString(line) || ansi.StringWidth(line) != 50 {
			t.Fatalf("invalid overlay width %d: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestUIModalDetailsPagesReachEnd(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := uiTestModel(uiTestEntry("Alpha"))
	m.width, m.height = 50, 14
	form := newUIEntryForm(uiTestEntry("Unsaved"), false)
	form.err = strings.Repeat("長い診断 ", 600) + " FINAL_MODAL_END"
	m.editor, m.details = &form, true
	page := m.detailPageSize()
	for i := 0; i < len(m.detailText())/page+1; i++ {
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if !strings.Contains(m.View().Content, "FINAL_MODAL_END") {
		t.Fatal("last error line unreachable")
	}
	if m.detailsOffset > len(m.detailText())-page {
		t.Fatal("offset uses terminal instead of modal page size")
	}
	before := m.detailsOffset
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.detailsOffset != max(0, before-page) {
		t.Fatal("page up must use modal height")
	}
}

func TestUIModalSavePreservesFilter(t *testing.T) {
	entry := uiTestEntry("Alpha")
	m := uiTestModel(entry)
	m.query.insert("Alpha")
	m.do = func(Entry, string) Result { return Result{State: "stopped"} }
	m.persist = func(Config) error { return nil }
	m, cmd := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
	m = uiFinish(t, m, cmd)
	m.editor.fields[uiName] = uiTextField{value: []rune("Beta"), cursor: 4}
	m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiFinish(t, m, cmd)
	if string(m.query.value) != "Alpha" || len(m.visible()) != 0 || m.cursor != 0 || m.editor != nil {
		t.Fatal("saving a renamed route must retain the filter and clamp selection")
	}
}
