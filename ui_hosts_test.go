package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestUISelectSSHHostForAddAndEdit(t *testing.T) {
	sshConfigHome(t, map[string]string{"config": "Include hosts.conf\n", "hosts.conf": "Host alpha beta\n HostName not-a-choice\n"})
	for _, editing := range []bool{false, true} {
		t.Run(fmt.Sprintf("editing=%v", editing), func(t *testing.T) {
			m := uiTestModel(uiTestEntry("Selected"))
			m.do = func(_ Entry, action string) Result {
				if action != "status" {
					t.Fatalf("unexpected action %q", action)
				}
				return stopped()
			}
			var saved Config
			m.persist = func(cfg Config) error { saved = cfg; return nil }
			if editing {
				var cmd tea.Cmd
				m, cmd = uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
				m = uiFinish(t, m, cmd)
			} else {
				m.cfg.Entries = nil
				m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
				m = uiInput(t, m, tea.PasteMsg{Content: "Selected"})
			}
			if !strings.Contains(m.View().Content, "Ctrl+P hosts") {
				t.Fatal("form does not advertise the host picker")
			}
			m = uiInput(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			content := m.View().Content
			if !strings.Contains(content, "SSH hosts") || !strings.Contains(content, "> alpha") || strings.Contains(content, "not-a-choice") {
				t.Fatalf("unexpected host list: %s", content)
			}
			m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if !strings.Contains(m.View().Content, "Add route") && !strings.Contains(m.View().Content, "Edit route") {
				t.Fatal("choosing a host did not return to the form")
			}
			m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			m = uiFinish(t, m, save)
			if len(saved.Entries) != 1 || saved.Entries[0].SSHHost != "beta" {
				t.Fatalf("chosen host was not saved: %+v", saved)
			}
			m.do = func(entry Entry, action string) Result {
				if action != "start" || entry.SSHHost != "beta" {
					t.Fatalf("chosen host did not reach the route engine: %+v %s", entry, action)
				}
				return ready("http://127.0.0.1:8080")
			}
			m, start := uiStep(t, m, tea.KeyPressMsg{Code: 's'})
			uiFinish(t, m, start)
		})
	}
}

func TestUIHostPickerCancelPreservesManualInput(t *testing.T) {
	sshConfigHome(t, map[string]string{"config": "Host alpha\n"})
	m := uiAddSSH(t, uiTestModel(), "Manual")
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "must-not-edit-hidden-field"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	var saved Config
	m.persist = func(cfg Config) error { saved = cfg; return nil }
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	uiFinish(t, m, save)
	if len(saved.Entries) != 1 || saved.Entries[0].SSHHost != "dev-box" {
		t.Fatalf("cancel changed manually typed host: %+v", saved)
	}
}

func TestUIEmptyHostPickerKeepsLocalServeAvailable(t *testing.T) {
	sshConfigHome(t, nil)
	m := uiTestModel()
	m = uiInput(t, m, tea.KeyPressMsg{Code: '2'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
	m = uiInput(t, m, tea.PasteMsg{Content: "Local"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View().Content, "No configured SSH hosts") {
		t.Fatal("missing config did not leave an empty, cancellable list")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	var saved Config
	m.persist = func(cfg Config) error { saved = cfg; return nil }
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	uiFinish(t, m, save)
	if len(saved.Entries) != 1 || saved.Entries[0].Kind != "serve" || saved.Entries[0].SSHHost != "" {
		t.Fatalf("empty SSH host no longer selects local Serve: %+v", saved)
	}
}

func TestUIHostPickerViewportAndCurrentSelection(t *testing.T) {
	var config strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&config, "Host host%02d\n", i)
	}
	sshConfigHome(t, map[string]string{"config": config.String()})
	m := uiAddSSH(t, uiTestModel(), "Many hosts")
	m = uiInput(t, m, tea.WindowSizeMsg{Width: 50, Height: 14})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for i := 0; i < 39; i++ {
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	content := m.View().Content
	if !strings.Contains(content, "> host39") || strings.Contains(content, "host00") {
		t.Fatal("host list did not scroll with the selection")
	}
	uiAssertFits(t, content, 50, 14)
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !strings.Contains(m.View().Content, "> host39") {
		t.Fatal("reopening did not select the current SSH host")
	}
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if !strings.Contains(m.View().Content, "> host30") {
		t.Fatal("PageUp did not move by the host viewport")
	}
}

func TestUIHelpAdvertisesHostPicker(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: '?'})
	if !strings.Contains(m.View().Content, "Ctrl+P SSH hosts") {
		t.Fatal("help does not advertise SSH host selection")
	}
}
