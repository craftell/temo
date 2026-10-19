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
