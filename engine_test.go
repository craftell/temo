package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type call struct { name string; args []string }
type fakeRunner struct {
	calls []call
	run func(string, []string) ([]byte, error)
}
func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name, append([]string(nil), args...)})
	if f.run != nil { return f.run(name, args) }
	return nil, nil
}
func contains(args []string, word string) bool { for _, a := range args { if a == word { return true } }; return false }
func TestSSHStartStopOwnedOnly(t *testing.T) {
	s := testStore(t)
	r := &fakeRunner{}
	g := Engine{Store: s, Runner: r}
	e := exampleSSH()
	if got := g.Do(e, "stop"); got.State != "stopped" || len(r.calls) != 0 { t.Fatalf("unowned stop: %#v %#v", got, r.calls) }
	if got := g.Do(e, "start"); got.State != "ready" { t.Fatal(got) }
	var start []string
	for _, c := range r.calls { if contains(c.args, "-f") { start = c.args } }
	for _, required := range []string{"127.0.0.1:8080:127.0.0.1:3000", "StrictHostKeyChecking=yes", "ExitOnForwardFailure=yes", "ForwardAgent=no", "PermitLocalCommand=no", "ClearAllForwardings=no"} {
		if !contains(start, required) { t.Fatalf("missing %s: %v", required, start) }
	}
	if contains(start, "ClearAllForwardings=yes") { t.Fatal("clears requested forward") }
	r.calls = nil
	if got := g.Do(e, "start"); got.State != "ready" { t.Fatal(got) }
	if len(r.calls) != 1 || !contains(r.calls[0].args, "check") { t.Fatal("duplicate start changed backend") }
	if got := g.Do(e, "stop"); got.State != "stopped" { t.Fatal(got) }
	last := r.calls[len(r.calls)-1]
	if !contains(last.args, g.socket(e)) || !contains(last.args, "exit") { t.Fatal(last) }
}
func TestSSHRejectsInheritedForwards(t *testing.T) {
	r := &fakeRunner{run: func(_ string, args []string) ([]byte, error) { return []byte("hostname server\nlocalforward 9000 127.0.0.1:9000\n"), nil }}
	g := Engine{Store: testStore(t), Runner: r}
	if got := g.Do(exampleSSH(), "start"); got.State != "error" { t.Fatal(got) }
	if len(r.calls) != 1 { t.Fatal("created master after inherited forward") }
}
func TestRemoteServeUsesBoundedQuotedCommand(t *testing.T) {
	e := exampleServe(); e.SSHHost = "dev-alias"
	r := &fakeRunner{}
	g := Engine{Store: testStore(t), Runner: r}
	_, err := g.tailscale(context.Background(), e, "serve", "--https=8443", "off")
	if err != nil { t.Fatal(err) }
	c := r.calls[0]
	if c.name != "ssh" || c.args[len(c.args)-1] != "tailscale 'serve' '--https=8443' 'off'" { t.Fatal(c) }
	if !contains(c.args, "ClearAllForwardings=yes") || !contains(c.args, "ControlPath=none") { t.Fatal(c) }
	if quoteArg("a'b") != "'a'\\''b'" { t.Fatal("shell quote failed") }
}
func serveJSON(e Entry, extra bool) []byte {
	handlers := map[string]any{"/": map[string]string{"Proxy": "http://127.0.0.1:3000"}}
	if extra { handlers["/other"] = map[string]string{"Text":"unmanaged"} }
	raw, _ := json.Marshal(map[string]any{"TCP": map[string]any{"8443":map[string]bool{"HTTPS":true}}, "Web":map[string]any{"dev.example.ts.net:8443":map[string]any{"Handlers":handlers}}})
	return raw
}
func TestServeOwnershipAndDrift(t *testing.T) {
	e := exampleServe()
	config := []byte("{}")
	r := &fakeRunner{run: func(_ string, args []string) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"status","--json"}) { return []byte(`{"BackendState":"Running","Self":{"ID":"node1","DNSName":"dev.example.ts.net."}}`), nil }
		if reflect.DeepEqual(args, []string{"serve","status","--json"}) { return config, nil }
		if contains(args, "off") { config = []byte("{}") } else { config = serveJSON(e, false) }
		return nil, nil
	}}
	g := Engine{Store: testStore(t), Runner: r}
	if got := g.Do(e, "start"); got.State != "ready" { t.Fatal(got) }
	before := len(r.calls)
	config = serveJSON(e, true)
	got := g.Do(e, "stop")
	if got.State != "error" || !strings.Contains(got.Detail, "changed outside") { t.Fatal(got) }
	for _, c := range r.calls[before:] { if contains(c.args, "off") { t.Fatal("stopped changed route") } }
	config = serveJSON(e, false)
	if got := g.Do(e, "stop"); got.State != "stopped" { t.Fatal(got) }
	config = serveJSON(e, false)
	if got := g.Do(e, "start"); got.State != "error" || !strings.Contains(got.Detail, "outside temo") { t.Fatal(got) }
}
func TestServePendingOutcomeNeverAdopts(t *testing.T) {
	e := exampleServe()
	s := testStore(t)
	r := &fakeRunner{run: func(_ string, args []string) ([]byte,error) {
		if args[0] == "status" { return []byte(`{"BackendState":"Running","Self":{"ID":"node1","DNSName":"dev.example.ts.net."}}`),nil }
		return serveJSON(e,false),nil
	}}
	g := Engine{Store:s,Runner:r}
	if err := g.remember(Ownership{Entry:e,Phase:"pending",NodeID:"node1"}); err != nil { t.Fatal(err) }
	if got := g.Do(e,"stop"); got.State != "error" || !strings.Contains(got.Detail,"uncertain") { t.Fatal(got) }
}
func TestServeSnapshotRejectsFunnelAndForeground(t *testing.T) {
	for _, raw := range []string{
		`{"AllowFunnel":{"dev.example.ts.net:8443":true}}`,
		`{"Foreground":{"session":{"TCP":{"8443":{"HTTPS":true}}}}}`,
		`{"Web":{"invalid":{}}}`,
		`not json`,
	} {
		if _,_,err := portSnapshot([]byte(raw),8443); err == nil { t.Fatalf("accepted %s",raw) }
	}
}
func TestDependencyFailureIsNotStopped(t *testing.T) {
	r := &fakeRunner{run:func(string,[]string)([]byte,error){return nil,errors.New("missing tailscale")}}
	g := Engine{Store:testStore(t),Runner:r}
	if got:=g.Do(exampleServe(),"status");got.State!="error"{t.Fatal(got)}
}
