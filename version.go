package main

import "runtime/debug"

var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	info, _ := debug.ReadBuildInfo()
	return versionFromInfo(info)
}

func versionFromInfo(info *debug.BuildInfo) string {
	if info == nil {
		return "dev"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	var revision string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return "dev"
	}
	label := "dev-" + revision[:min(12, len(revision))]
	if modified {
		label += "-dirty"
	}
	return label
}
