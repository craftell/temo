package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func uiTableLine(t *testing.T, content, match string) string {
	t.Helper()
	for _, line := range strings.Split(ansi.Strip(content), "\n") {
		if strings.Contains(line, match) {
			return line
		}
	}
	t.Fatalf("missing %q in view:\n%s", match, ansi.Strip(content))
	return ""
}

func uiTableColumn(t *testing.T, line, value string) int {
	t.Helper()
	index := strings.Index(line, value)
	if index < 0 {
		t.Fatalf("missing %q in line %q", value, line)
	}
	return ansi.StringWidth(line[:index])
}

func TestUIRouteTableAlignsColumns(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{80, 100, 120} {
		for _, kind := range []string{"ssh", "serve"} {
			t.Run(fmt.Sprintf("%s/%d", kind, width), func(t *testing.T) {
				alpha, japanese := uiTestEntry("Alpha"), uiTestEntry("日本語")
				alpha.Kind, japanese.Kind = kind, kind
				alpha.LocalPort, japanese.LocalPort = 65535, 8081
				alpha.ServePort, japanese.ServePort = 65535, 8443
				alpha.TargetPort, japanese.TargetPort = 54321, 3001
				alpha.TargetHost = strings.Repeat("long-host.", 8) + "example"
				japanese.TargetHost = "2001:db8::1234"
				if kind == "serve" {
					alpha.SSHHost = ""
				}
				m := uiTestModel(alpha, japanese)
				m.kind, m.width = kind, width
				m.states[alpha.ID] = Result{State: "ready"}
				m.states[japanese.ID] = Result{State: "inactive"}
				content := m.View().Content
				header := uiTableLine(t, content, "NAME")
				first := uiTableLine(t, content, "> Alpha")
				second := uiTableLine(t, content, "日本語")
				for _, test := range []struct {
					line   string
					value  string
					header string
				}{
					{first, "Alpha", "NAME"},
					{second, "日本語", "NAME"},
					{first, "setup ready", "STATUS"},
					{second, "stopped; cleanup needed", "STATUS"},
					{second, "dev-box", "SSH HOST"},
				} {
					if got, want := uiTableColumn(t, test.line, test.value), uiTableColumn(t, header, test.header); got != want {
						t.Fatalf("%q starts at column %d, want %d", test.value, got, want)
					}
				}
				listenHeader := "LOCAL"
				if kind == "serve" {
					listenHeader = "HTTPS"
					if !strings.Contains(first, "local") {
						t.Fatal("local Serve route did not identify its host as local")
					}
				}
				if got, want := uiTableColumn(t, first, ":65535"), uiTableColumn(t, header, listenHeader); got != want {
					t.Fatalf("listening port starts at column %d, want %d", got, want)
				}
				target := ansi.Cut(first, uiTableColumn(t, header, "TARGET"), width-2)
				if !strings.Contains(target, ":54321") || !strings.Contains(target, "…") {
					t.Fatalf("long target lost its port or truncation marker: %q", target)
				}
				uiAssertFits(t, content, width, m.height)
			})
		}
	}
}

func TestUIRouteTableCompactPreservesStatusAndPorts(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{50, 60, 79} {
		for _, height := range []int{14, 19, 30} {
			for _, kind := range []string{"ssh", "serve"} {
				t.Run(fmt.Sprintf("%s/%dx%d", kind, width, height), func(t *testing.T) {
					entry := uiTestEntry("配送レポート")
					entry.Kind, entry.LocalPort, entry.ServePort = kind, 65535, 65535
					entry.TargetPort = 54321
					entry.TargetHost = strings.Repeat("長いホスト", 20)
					m := uiTestModel(entry)
					m.kind, m.width, m.height = kind, width, height
					m.states[entry.ID] = Result{State: "inactive"}
					content := m.View().Content
					row := uiTableLine(t, content, "> 配送レポート")
					if !strings.Contains(row, "stopped; cleanup needed") {
						t.Fatalf("cleanup warning was clipped: %q", row)
					}
					endpoints := uiTableLine(t, content, ":65535")
					if !strings.Contains(endpoints, "dev-box") || !strings.Contains(endpoints, ":54321") {
						t.Fatalf("compact row lost its host or ports: %q", endpoints)
					}
					for _, label := range []string{"NAME", "STATUS", "SSH HOST", "TARGET", "p config location", "Application reachability is unverified."} {
						if !strings.Contains(content, label) {
							t.Fatalf("missing %q at %dx%d", label, width, height)
						}
					}
					listenHeader := "LOCAL"
					if kind == "serve" {
						listenHeader = "HTTPS"
					}
					if !strings.Contains(content, listenHeader) {
						t.Fatalf("missing %s column", listenHeader)
					}
					uiAssertFits(t, content, width, height)
				})
			}
		}
	}
}

func TestUIRouteTableKeepsEveryStatusReadable(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, test := range []struct {
		state string
		want  string
	}{
		{"", "unknown"},
		{"unknown", "unknown"},
		{"ready", "setup ready"},
		{"running", "setup ready"},
		{"stopped", "stopped"},
		{"inactive", "stopped; cleanup needed"},
		{"error", "error"},
		{"checking", "checking"},
		{"queued", "queued"},
	} {
		t.Run(test.want+"/"+test.state, func(t *testing.T) {
			entry := uiTestEntry("Status")
			m := uiTestModel(entry)
			m.width, m.height = 50, 14
			m.states[entry.ID] = Result{State: test.state}
			if test.state == "checking" {
				m.refresh.pending = map[string]uint64{entry.ID: 1}
			}
			if test.state == "queued" {
				m.refresh.queue = []Entry{entry}
			}
			row := uiTableLine(t, m.View().Content, "> Status")
			if !strings.Contains(row, test.want) {
				t.Fatalf("status %q was not readable: %q", test.want, row)
			}
		})
	}
}

func TestUIRouteTableSelectionSurvivesResizeAndScrolling(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	entries := make([]Entry, 60)
	for i := range entries {
		entries[i] = uiTestEntry(fmt.Sprintf("Route%02d", i))
	}
	m := uiTestModel(entries...)
	for _, width := range []int{100, 50, 80, 60, 100} {
		m = uiInput(t, m, tea.WindowSizeMsg{Width: width, Height: 14})
		for _, position := range []int{0, 20, 59, 58, 0} {
			m.cursor = position
			content := m.View().Content
			row := uiTableLine(t, content, fmt.Sprintf("> Route%02d", position))
			if !strings.Contains(row, "unknown") {
				t.Fatal("selected row lost its status")
			}
			if position == 59 && strings.Contains(content, "Route00") {
				t.Fatal("old rows remain visible after scrolling to the end")
			}
			if strings.Count(content, "│ > ") != 1 {
				t.Fatal("selection is missing or duplicated")
			}
			uiAssertFits(t, content, width, 14)
		}
	}
}

func TestUIRouteTableColorAndPlainTextAgree(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	entry := uiTestEntry("Alpha")
	m := uiTestModel(entry)
	m.states[entry.ID] = Result{State: "ready"}
	colored := m.View().Content
	if !strings.Contains(colored, "\x1b[37;44m") || !strings.Contains(colored, "\x1b[92m") {
		t.Fatal("selected row background or ready state color is missing")
	}
	for _, env := range []string{"NO_COLOR", "TERM"} {
		t.Run(env, func(t *testing.T) {
			if env == "NO_COLOR" {
				t.Setenv("NO_COLOR", "1")
			} else {
				t.Setenv("TERM", "dumb")
			}
			plain := m.View().Content
			if strings.ContainsRune(plain, '\x1b') {
				t.Fatal("plain-text mode emitted ANSI controls")
			}
			if plain != ansi.Strip(colored) {
				t.Fatal("disabling color changed labels, alignment, or selection")
			}
		})
	}
}

func TestUIRouteTableEscapesControlsBeforeStyling(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	entry := uiTestEntry("Alpha\x1b]52;c;unsafe\a")
	entry.SSHHost = "dev\u202e-box"
	m := uiTestModel(entry)
	m.states[entry.ID] = Result{State: "error", URL: "https://example/\x1b", Detail: "bad\a\u202e"}
	content := m.View().Content
	plain := ansi.Strip(content)
	if strings.ContainsAny(plain, "\x1b\a\u202e") || strings.Contains(content, "\x1b]52") {
		t.Fatal("untrusted terminal controls escaped sanitization")
	}
	for _, escaped := range []string{"\\u001b", "\\u0007", "\\u202e"} {
		if !strings.Contains(plain, escaped) {
			t.Fatalf("escaped control %q is missing", escaped)
		}
	}
	uiAssertFits(t, content, m.width, m.height)
}

func TestUIRouteTableLongNameDoesNotHideEndpoints(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, width := range []int{50, 80, 100} {
		entry := uiTestEntry(strings.Repeat("日本語", 40))
		entry.LocalPort, entry.TargetPort = 65432, 54321
		m := uiTestModel(entry)
		m.width = width
		content := m.View().Content
		if !strings.Contains(content, ":65432") || !strings.Contains(content, ":54321") {
			t.Fatalf("long name hid a port at width %d", width)
		}
		if !strings.Contains(content, "…") {
			t.Fatal("long name was not visibly truncated")
		}
		uiAssertFits(t, content, width, m.height)
	}
}

func TestUIRouteTablePageKeysMatchVisibleRows(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	entries := make([]Entry, 30)
	for i := range entries {
		entries[i] = uiTestEntry(fmt.Sprintf("Route%02d", i))
	}
	for _, width := range []int{50, 80, 120} {
		m := uiTestModel(entries...)
		m = uiInput(t, m, tea.WindowSizeMsg{Width: width, Height: 20})
		page := m.tablePageSize()
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
		if m.cursor != page {
			t.Fatalf("PageDown at width %d moved to %d, want %d", width, m.cursor, page)
		}
		uiTableLine(t, m.View().Content, fmt.Sprintf("> Route%02d", page))
		m = uiInput(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
		if m.cursor != 0 {
			t.Fatalf("PageUp at width %d did not return to the first row", width)
		}
		uiTableLine(t, m.View().Content, "> Route00")
	}
}
