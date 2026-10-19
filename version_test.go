package main

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersionUsesLinkedVersion(t *testing.T) {
	previous := version
	t.Cleanup(func() { version = previous })
	version = "1.2.3"
	if got := buildVersion(); got != "1.2.3" {
		t.Fatalf("version = %q", got)
	}
}

func TestVersionFromBuildInfo(t *testing.T) {
	for _, tt := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"missing", nil, "dev"},
		{"module", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
		{"unversioned", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"revision", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}}, "dev-0123456789ab"},
		{"dirty", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}}, "dev-abc-dirty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionFromInfo(tt.info); got != tt.want {
				t.Fatalf("version = %q, want %q", got, tt.want)
			}
		})
	}
}
