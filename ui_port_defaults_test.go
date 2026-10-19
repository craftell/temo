package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func uiFocusPortField(t *testing.T, m model, field int) model {
	t.Helper()
	if m.editor == nil {
		t.Fatal("expected an open form")
	}
	for range uiFieldCount {
		if m.editor.focus == field {
			return m
		}
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	t.Fatalf("field %d is not reachable", field)
	return m
}

func uiAssertPortFields(t *testing.T, m model, target, local string) {
	t.Helper()
	if m.editor == nil {
		t.Fatal("expected an open form")
	}
	if got := string(m.editor.fields[uiTargetPort].value); got != target {
		t.Fatalf("Target port = %q, want %q", got, target)
	}
	if got := string(m.editor.fields[uiLocalPort].value); got != local {
		t.Fatalf("Local port = %q, want %q", got, local)
	}
}

func TestUINewSSHLocalPortDefaultsToTarget(t *testing.T) {
	m := uiAddSSH(t, uiTestModel(), "Linked ports")
	uiAssertPortFields(t, m, "3000", "3000")
	if !strings.Contains(m.View().Content, "Local port follows target until edited.") {
		t.Fatal("new SSH form does not explain automatic Local port")
	}
	var saved Config
	m.persist = func(cfg Config) error { saved = cfg; return nil }
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	uiFinish(t, m, save)
	if len(saved.Entries) != 1 || saved.Entries[0].TargetPort != 3000 || saved.Entries[0].LocalPort != 3000 {
		t.Fatalf("saved defaults = %+v", saved.Entries)
	}
}

func TestUINewSSHLocalPortFollowsTargetInput(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a'})
	m = uiFocusPortField(t, m, uiTargetPort)
	for _, step := range []struct {
		msg  tea.Msg
		want string
	}{
		{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, ""},
		{tea.KeyPressMsg{Code: '4', Text: "4"}, "4"},
		{tea.KeyPressMsg{Code: '3', Text: "321"}, "4321"},
		{tea.PasteMsg{Content: "\n8\t"}, "43218"},
		{tea.KeyPressMsg{Code: tea.KeyBackspace}, "4321"},
		{tea.KeyPressMsg{Code: tea.KeyHome}, "4321"},
		{tea.KeyPressMsg{Code: tea.KeyDelete}, "321"},
		{tea.KeyPressMsg{Code: tea.KeyEnd}, "321"},
		{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, ""},
		{tea.PasteMsg{Content: "5500"}, "5500"},
	} {
		m = uiInput(t, m, step.msg)
		uiAssertPortFields(t, m, step.want, step.want)
	}
}

func TestUILocalPortManualEditsStopFollowing(t *testing.T) {
	for _, test := range []struct {
		name  string
		input []tea.Msg
		want  string
	}{
		{"typing", []tea.Msg{tea.KeyPressMsg{Code: '1', Text: "1"}}, "30001"},
		{"paste", []tea.Msg{tea.PasteMsg{Content: "9"}}, "30009"},
		{"backspace", []tea.Msg{tea.KeyPressMsg{Code: tea.KeyBackspace}}, "300"},
		{"delete", []tea.Msg{tea.KeyPressMsg{Code: tea.KeyHome}, tea.KeyPressMsg{Code: tea.KeyDelete}}, "000"},
		{"clear", []tea.Msg{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}}, ""},
		{"replace with same value", []tea.Msg{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, tea.PasteMsg{Content: "3000"}}, "3000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a'})
			m = uiFocusPortField(t, m, uiLocalPort)
			for _, msg := range test.input {
				m = uiInput(t, m, msg)
			}
			uiAssertPortFields(t, m, "3000", test.want)
			if !strings.Contains(m.View().Content, "Local port is set independently.") {
				t.Fatal("manual Local port override does not update the form hint")
			}
			m = uiFocusPortField(t, m, uiTargetPort)
			m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			m = uiInput(t, m, tea.PasteMsg{Content: "4000"})
			uiAssertPortFields(t, m, "4000", test.want)
			m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
			uiAssertPortFields(t, m, "400", test.want)
		})
	}
}

func TestUILocalPortNavigationAndNoOpsKeepFollowing(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a'})
	m = uiFocusPortField(t, m, uiLocalPort)
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyLeft},
		tea.KeyPressMsg{Code: tea.KeyRight},
		tea.KeyPressMsg{Code: tea.KeyHome},
		tea.KeyPressMsg{Code: tea.KeyBackspace},
		tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: tea.KeyEnd},
		tea.KeyPressMsg{Code: tea.KeyDelete},
		tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl},
		tea.PasteMsg{Content: ""},
		tea.PasteMsg{Content: "\n\t\x1b\u202e"},
		tea.KeyPressMsg{Code: tea.KeyTab},
		tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift},
		tea.KeyPressMsg{Code: tea.KeyUp},
		tea.KeyPressMsg{Code: tea.KeyDown},
	} {
		m = uiInput(t, m, msg)
		uiAssertPortFields(t, m, "3000", "3000")
	}
	m = uiFocusPortField(t, m, uiTargetPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "4500"})
	uiAssertPortFields(t, m, "4500", "4500")
}

func TestUILocalPortNoOpOnEmptyValueKeepsFollowing(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a'})
	m = uiFocusPortField(t, m, uiTargetPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiFocusPortField(t, m, uiLocalPort)
	for _, key := range []tea.KeyPressMsg{
		{Code: 'u', Mod: tea.ModCtrl},
		{Code: tea.KeyBackspace},
		{Code: tea.KeyDelete},
	} {
		m = uiInput(t, m, key)
		uiAssertPortFields(t, m, "", "")
	}
	m = uiFocusPortField(t, m, uiTargetPort)
	m = uiInput(t, m, tea.PasteMsg{Content: "5000"})
	uiAssertPortFields(t, m, "5000", "5000")
}

func TestUILocalOverrideDoesNotMutateTarget(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a'})
	m = uiFocusPortField(t, m, uiTargetPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "4000"})
	m = uiFocusPortField(t, m, uiLocalPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyDelete})
	uiAssertPortFields(t, m, "4000", "000")
}

func TestUICancelReopensAutomaticLocalPort(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: 'a'})
	m = uiFocusPortField(t, m, uiLocalPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "8080"})
	m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
	uiAssertPortFields(t, m, "3000", "3000")
	m = uiFocusPortField(t, m, uiTargetPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "4500"})
	uiAssertPortFields(t, m, "4500", "4500")
}

func TestUIExistingSSHLocalPortNeverFollows(t *testing.T) {
	for _, local := range []int{3000, 8080} {
		entry := uiTestEntry("Existing")
		entry.LocalPort = local
		m := uiTestModel(entry)
		m.do = func(Entry, string) Result { return stopped() }
		m, check := uiStep(t, m, tea.KeyPressMsg{Code: 'e'})
		m = uiFinish(t, m, check)
		if !strings.Contains(m.View().Content, "SSH host is required.") {
			t.Fatal("existing SSH form lost its normal hint")
		}
		m = uiFocusPortField(t, m, uiTargetPort)
		m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		m = uiInput(t, m, tea.PasteMsg{Content: "4500"})
		var saved Config
		m.persist = func(cfg Config) error { saved = cfg; return nil }
		m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		uiFinish(t, m, save)
		if len(saved.Entries) != 1 || saved.Entries[0].TargetPort != 4500 || saved.Entries[0].LocalPort != local {
			t.Fatalf("editing local port %d saved %+v", local, saved.Entries)
		}
	}
}

func TestUIServePortDoesNotFollowTarget(t *testing.T) {
	m := uiInput(t, uiTestModel(), tea.KeyPressMsg{Code: '2'})
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'a'})
	m = uiInput(t, m, tea.PasteMsg{Content: "Serve app"})
	m = uiFocusPortField(t, m, uiTargetPort)
	m = uiInput(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = uiInput(t, m, tea.PasteMsg{Content: "4500"})
	if got := string(m.editor.fields[uiServePort].value); got != "8443" {
		t.Fatalf("HTTPS port = %q, want 8443", got)
	}
	var saved Config
	m.persist = func(cfg Config) error { saved = cfg; return nil }
	m, save := uiStep(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	uiFinish(t, m, save)
	if len(saved.Entries) != 1 || saved.Entries[0].TargetPort != 4500 || saved.Entries[0].ServePort != 8443 || saved.Entries[0].LocalPort != 0 {
		t.Fatalf("saved Serve route = %+v", saved.Entries)
	}
}
