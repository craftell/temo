package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpdateCLIHelpAndInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"help", []string{"--help"}, false},
		{"unknown flag", []string{"--force"}, true},
		{"positional", []string{"tomorrow"}, true},
		{"check positional", []string{"--check", "tomorrow"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runUpdateCLI(tc.args, &out)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %v", err, tc.wantError)
			}
			if tc.name == "help" && !strings.Contains(out.String(), "-check") {
				t.Fatalf("help missing --check: %s", out.String())
			}
		})
	}
}
