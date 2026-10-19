package main

import (
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"testing"
)

func sshConfigHome(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for name, content := range files {
		path := filepath.Join(home, ".ssh", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestConfiguredSSHHosts(t *testing.T) {
	sshConfigHome(t, map[string]string{
		"config": `# Do not expose these settings.
Host zebra alpha ALPHA "quoted-host" 'single-host' *.example ? !blocked user@machine fake#comment ghost\name
 HostName secret-hostname
 IdentityFile secret-key-path
 Include "parts/*.conf" missing.conf
Match exec "must never execute"
 Include hidden.conf
 HostName another-secret
Host=after-match
Include ~/extra-ssh-hosts
`,
		"parts/one.conf": `Host included
Include nested.conf
`,
		"nested.conf": `hOsT nested
Include config
`,
		"hidden.conf": `Host hidden-in-match`,
	})
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "extra-ssh-hosts"), []byte("Host tilde\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want := []string{"after-match", "alpha", "included", "nested", "quoted-host", "single-host", "tilde", "user@machine", "zebra"}
	if got := configuredSSHHosts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
}

func TestSSHHostsIncludePathsAndMatchContext(t *testing.T) {
	home := sshConfigHome(t, map[string]string{
		"config": `Include "with space.conf"
Include context.conf
Include hidden.conf
Host after
Include absolute-placeholder
`,
		"with space.conf": "Host spaced\n",
		"context.conf":    "Match all\n",
		"hidden.conf":     "Host hidden\n",
	})
	absolute := filepath.Join(home, "absolute.conf")
	if err := os.WriteFile(absolute, []byte("Host absolute\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".ssh", "config")
	file, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("Include " + absolute + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	want := []string{"absolute", "after", "spaced"}
	if got := configuredSSHHosts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
}

func TestSSHHostsMissingOrUnreadableConfigIsEmpty(t *testing.T) {
	home := sshConfigHome(t, nil)
	if got := configuredSSHHosts(); len(got) != 0 {
		t.Fatalf("missing config returned %v", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh", "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := configuredSSHHosts(); len(got) != 0 {
		t.Fatalf("unreadable config returned %v", got)
	}
}

func TestSSHHostsNamedUserIncludeAndMatchNeverExecutes(t *testing.T) {
	home := sshConfigHome(t, map[string]string{"config": ""})
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	included := filepath.Join(home, "aliases.conf")
	if err := os.WriteFile(included, []byte("Host named-user-include\n"), 0600); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(account.HomeDir, included)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "match-executed")
	content := "Include ~" + account.Username + "/" + relative + "\nMatch exec \"touch " + marker + "\"\n HostName secret\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if got := configuredSSHHosts(); !reflect.DeepEqual(got, []string{"named-user-include"}) {
		t.Fatalf("hosts = %v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("reading hosts executed Match: %v", err)
	}
}

func TestSSHConfigWords(t *testing.T) {
	for _, tt := range []struct {
		line string
		want []string
	}{
		{`Host = first second # comment`, []string{"Host", "first", "second"}},
		{`Host=first`, []string{"Host", "first"}},
		{`Host name#not-a-comment`, []string{"Host", "name#not-a-comment"}},
		{`Host 'single-host'`, []string{"Host", "single-host"}},
		{`Host ghost\name`, []string{"Host", `ghost\name`}},
		{`Host ""`, []string{"Host", ""}},
		{`Include "with space.conf" escaped\ path.conf`, []string{"Include", "with space.conf", "escaped path.conf"}},
		{`Include path=with=equals`, []string{"Include", "path=with=equals"}},
		{`Host invalid=alias`, []string{"Host", "invalid=alias"}},
		{`Host "unfinished`, nil},
		{`Host trailing\`, []string{"Host", `trailing\`}},
	} {
		if got := sshConfigWords(tt.line); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("words(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
