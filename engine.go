package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Result struct{ State, URL, Detail string }
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 1024*1024 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

type Ownership struct {
	Version  int    `json:"version"`
	Entry    Entry  `json:"entry"`
	Phase    string `json:"phase"`
	NodeID   string `json:"node_id,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
	URL      string `json:"url,omitempty"`
}

type Engine struct {
	Store  *Store
	Runner Runner
}

func (g *Engine) Do(e Entry, action string) Result {
	if err := e.Validate(); err != nil {
		return failed(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var result Result
	var err error
	if action == "open" {
		result = g.Do(e, "status")
		if result.State != "ready" || result.URL == "" {
			return Result{State: result.State, Detail: "URL is available only after setup is ready. " + result.Detail}
		}
		name := "xdg-open"
		if runtime.GOOS == "darwin" {
			name = "open"
		}
		_, err = g.Runner.Run(ctx, name, result.URL)
		if err != nil {
			result.Detail = err.Error()
		} else {
			result.Detail = "Opened URL. Application reachability is not verified."
		}
		return result
	}
	if action != "start" && action != "stop" && action != "status" {
		return failed(errors.New("unsupported action"))
	}
	if e.Kind == "ssh" {
		result, err = g.ssh(ctx, e, action)
	} else {
		result, err = g.serve(ctx, e, action)
	}
	if err != nil {
		return failed(err)
	}
	return result
}

func failed(err error) Result { return Result{State: "error", Detail: err.Error()} }
func ready(url string) Result {
	return Result{State: "ready", URL: url, Detail: "Backend setup ready. Application reachability is not verified."}
}
func stopped() Result { return Result{State: "stopped", Detail: "No managed backend is active."} }

func (g *Engine) journalPath(e Entry) string {
	return filepath.Join(g.Store.Dir, "owner-"+e.ID+".json")
}
func (g *Engine) ownership(e Entry) (Ownership, bool, error) {
	var o Ownership
	err := readJSON(g.journalPath(e), &o)
	if errors.Is(err, os.ErrNotExist) {
		return o, false, nil
	}
	if err != nil {
		return o, false, err
	}
	if o.Version != 1 {
		return o, true, errors.New("unsupported ownership record version")
	}
	if o.Entry != e {
		return o, true, errors.New("saved definition differs from ownership record; restore it before changing the backend")
	}
	return o, true, nil
}
func (g *Engine) remember(o Ownership) error {
	o.Version = 1
	return atomicJSON(g.journalPath(o.Entry), o)
}
func (g *Engine) forget(e Entry) error {
	err := os.Remove(g.journalPath(e))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (g *Engine) socket(e Entry) string {
	return filepath.Join(g.Store.Dir, e.ID+".sock")
}

func sshOptions() []string {
	return []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
}

func (g *Engine) ssh(ctx context.Context, e Entry, action string) (Result, error) {
	o, owned, err := g.ownership(e)
	if err != nil {
		return Result{}, err
	}
	socket := g.socket(e)
	if len(socket) > 100 {
		return Result{}, errors.New("config path is too long for an SSH control socket; use --config-dir with a shorter private path")
	}
	control := func(verb string) error {
		_, err := g.Runner.Run(ctx, "ssh", "-F", "/dev/null", "-S", socket, "-O", verb, "--", e.SSHHost)
		return err
	}
	if owned {
		checkErr := control("check")
		if checkErr == nil {
			if action != "stop" && o.Phase != "active" {
				return Result{}, errors.New("SSH start outcome is uncertain; its owned master is alive, use explicit stop before retrying")
			}
			if action != "stop" {
				return ready(fmt.Sprintf("http://127.0.0.1:%d", e.LocalPort)), nil
			}
			if err := control("exit"); err != nil {
				return Result{}, err
			}
			if err := g.forget(e); err != nil {
				return Result{}, err
			}
			return stopped(), nil
		}
		var exitError *exec.ExitError
		if !errors.As(checkErr, &exitError) || ctx.Err() != nil {
			return Result{}, checkErr
		}
		if _, err := os.Lstat(socket); err == nil {
			return Result{}, errors.New("owned control socket exists but check failed; inspect it manually, no process was stopped")
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		if err := g.forget(e); err != nil {
			return Result{}, err
		}
	}
	if action != "start" {
		return stopped(), nil
	}
	if _, err := os.Lstat(socket); err == nil {
		return Result{}, errors.New("unmanaged control socket exists; refusing to reuse it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	args := append(sshOptions(), "-o", "ClearAllForwardings=no", "-G", "--", e.SSHHost)
	config, err := g.Runner.Run(ctx, "ssh", args...)
	if err != nil {
		return Result{}, err
	}
	for _, line := range strings.Split(string(config), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && (fields[0] == "localforward" || fields[0] == "remoteforward" || fields[0] == "dynamicforward") {
			return Result{}, errors.New("SSH alias inherits forwarding rules; use a dedicated alias without LocalForward, RemoteForward, or DynamicForward")
		}
	}
	if err := g.remember(Ownership{Entry: e, Phase: "pending"}); err != nil {
		return Result{}, err
	}
	target := net.JoinHostPort(e.TargetHost, strconv.Itoa(e.TargetPort))
	forward := fmt.Sprintf("127.0.0.1:%d:%s", e.LocalPort, target)
	args = append(sshOptions(), "-o", "ClearAllForwardings=no", "-M", "-S", socket, "-o", "ControlMaster=yes", "-o", "ControlPersist=yes", "-o", "ExitOnForwardFailure=yes", "-f", "-N", "-L", forward, "--", e.SSHHost)
	if _, err := g.Runner.Run(ctx, "ssh", args...); err != nil {
		return Result{}, fmt.Errorf("start outcome may be uncertain; refresh before retrying: %w", err)
	}
	if err := control("check"); err != nil {
		return Result{}, fmt.Errorf("SSH setup returned but control check failed: %w", err)
	}
	if err := g.remember(Ownership{Entry: e, Phase: "active"}); err != nil {
		return Result{}, err
	}
	return ready(fmt.Sprintf("http://127.0.0.1:%d", e.LocalPort)), nil
}

func quoteArg(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (g *Engine) tailscale(ctx context.Context, e Entry, args ...string) ([]byte, error) {
	if e.SSHHost == "" {
		return g.Runner.Run(ctx, "tailscale", args...)
	}
	words := []string{"tailscale"}
	for _, a := range args {
		words = append(words, quoteArg(a))
	}
	ssh := append(sshOptions(), "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ClearAllForwardings=yes", "-T", "--", e.SSHHost, strings.Join(words, " "))
	return g.Runner.Run(ctx, "ssh", ssh...)
}

type serveConfig struct {
	TCP         map[string]json.RawMessage
	Web         map[string]json.RawMessage
	AllowFunnel map[string]bool
	Foreground  map[string]json.RawMessage
	Services    map[string]json.RawMessage
}
type nodeStatus struct {
	BackendState string
	Self         *struct {
		ID      string
		DNSName string
	}
}
type portView struct {
	TCP json.RawMessage            `json:"tcp,omitempty"`
	Web map[string]json.RawMessage `json:"web,omitempty"`
}

func canonical(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func portSnapshot(raw []byte, port int) (string, bool, error) {
	var c serveConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return "", false, fmt.Errorf("invalid or unsupported Serve status JSON: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", false, errors.New("unexpected trailing Serve status JSON")
	}
	p := strconv.Itoa(port)
	v := portView{TCP: c.TCP[p], Web: map[string]json.RawMessage{}}
	for hp, web := range c.Web {
		_, candidate, err := net.SplitHostPort(hp)
		if err != nil {
			return "", false, errors.New("unrecognized Serve host:port")
		}
		if candidate == p {
			v.Web[hp] = web
		}
	}
	for hp, enabled := range c.AllowFunnel {
		_, candidate, err := net.SplitHostPort(hp)
		if err != nil {
			return "", false, errors.New("unrecognized Funnel host:port")
		}
		if candidate == p && enabled {
			return "", false, errors.New("Funnel is enabled on this port; refusing to manage it")
		}
	}
	for _, fg := range c.Foreground {
		_, used, err := portSnapshot(fg, port)
		if err != nil {
			return "", false, err
		}
		if used {
			return "", false, errors.New("a foreground Serve process owns this port")
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false, err
	}
	snapshot, err := canonical(b)
	return snapshot, len(v.TCP) > 0 || len(v.Web) > 0, err
}

func desiredSnapshot(dns string, e Entry) string {
	proxy := fmt.Sprintf("http://127.0.0.1:%d", e.TargetPort)
	hp := net.JoinHostPort(dns, strconv.Itoa(e.ServePort))
	web, _ := json.Marshal(map[string]any{"Handlers": map[string]any{"/": map[string]string{"Proxy": proxy}}})
	b, _ := json.Marshal(portView{TCP: json.RawMessage(`{"HTTPS":true}`), Web: map[string]json.RawMessage{hp: web}})
	value, _ := canonical(b)
	return value
}

func (g *Engine) serve(ctx context.Context, e Entry, action string) (Result, error) {
	o, owned, err := g.ownership(e)
	if err != nil {
		return Result{}, err
	}
	nodeRaw, err := g.tailscale(ctx, e, "status", "--json")
	if err != nil {
		return Result{}, err
	}
	var node nodeStatus
	if err := json.Unmarshal(nodeRaw, &node); err != nil {
		return Result{}, err
	}
	if node.BackendState != "Running" || node.Self == nil || node.Self.ID == "" || node.Self.DNSName == "" {
		return Result{}, errors.New("Tailscale must be running with a node identity and MagicDNS name")
	}
	dns := strings.TrimSuffix(node.Self.DNSName, ".")
	if !hostPattern.MatchString(dns) || strings.Contains(dns, "@") {
		return Result{}, errors.New("invalid Tailscale DNS name")
	}
	if owned && o.NodeID != node.Self.ID {
		return Result{}, errors.New("Tailscale node identity changed; refusing to modify this node")
	}
	read := func() (string, bool, error) {
		raw, err := g.tailscale(ctx, e, "serve", "status", "--json")
		if err != nil {
			return "", false, err
		}
		return portSnapshot(raw, e.ServePort)
	}
	current, used, err := read()
	if err != nil {
		return Result{}, err
	}
	if owned && o.Phase != "active" {
		return Result{}, errors.New("previous Serve operation has an uncertain outcome; inspect the route and ownership journal manually before changing it")
	}
	if owned && used && current != o.Snapshot {
		return Result{}, errors.New("Serve configuration changed outside temo; refusing to modify it")
	}
	if !owned && used {
		return Result{}, errors.New("this HTTPS port is already configured outside temo; choose an unused port")
	}
	if !used && owned {
		if err := g.forget(e); err != nil {
			return Result{}, err
		}
		owned = false
	}
	url := "https://" + net.JoinHostPort(dns, strconv.Itoa(e.ServePort)) + "/"
	if action == "status" {
		if owned {
			return ready(url), nil
		}
		return stopped(), nil
	}
	if action == "stop" && !owned {
		return stopped(), nil
	}
	if action == "start" && owned {
		return ready(url), nil
	}
	second, secondUsed, err := read()
	if err != nil {
		return Result{}, err
	}
	if current != second || used != secondUsed {
		return Result{}, errors.New("Serve changed during preflight; nothing was changed")
	}
	record := Ownership{Entry: e, Phase: "pending", NodeID: node.Self.ID, Snapshot: current, URL: url}
	if err := g.remember(record); err != nil {
		return Result{}, err
	}
	args := []string{"serve", "--bg", "--https=" + strconv.Itoa(e.ServePort), "--set-path=/", fmt.Sprintf("http://127.0.0.1:%d", e.TargetPort)}
	if action == "stop" {
		args = append(args, "off")
	}
	if _, err := g.tailscale(ctx, e, args...); err != nil {
		return Result{}, fmt.Errorf("Serve outcome uncertain; inspect it manually before retrying: %w", err)
	}
	after, afterUsed, err := read()
	if err != nil {
		return Result{}, err
	}
	if action == "stop" {
		if afterUsed {
			return Result{}, errors.New("port is still configured after stop; ownership left pending for manual inspection")
		}
		if err := g.forget(e); err != nil {
			return Result{}, err
		}
		return stopped(), nil
	}
	if !afterUsed || after != desiredSnapshot(dns, e) {
		return Result{}, errors.New("Serve result differs from requested route; no automatic rollback, inspect it manually")
	}
	record.Phase, record.Snapshot = "active", after
	if err := g.remember(record); err != nil {
		return Result{}, err
	}
	return ready(url), nil
}
