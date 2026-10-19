---
name: verify-temo
description: Run temo's real terminal UI in a private tmux server. Use after connection or UI changes to capture screens, test real loopback SSH, and check local and SSH Serve with a safe Tailscale stub.
---

# Verify temo

Run the [feature map](features/README.md) against the built app. The bundled helper drives every mapped feature through terminal keys, not internal model calls.

This recipe passed on Linux amd64 with Go 1.25.5, tmux 3.4, and OpenSSH 9.6p1. Real loopback forwarding passed. Serve uses a stub, including when reached over real loopback SSH. Real Tailscale mutations, tailnet HTTPS, a graphical browser, and macOS terminal behavior remain unverified.

## Launch

From the repository root, choose a new evidence directory outside the checkout. The directory must not contain evidence from another run.

```sh
EVIDENCE=$(mktemp -d /tmp/temo-evidence-XXXXXXXX)
.cursor/skills/verify-temo/verify.py --evidence "$EVIDENCE"
```

Requires Python 3.9+, Go 1.25+, tmux, OpenSSH client and server, ssh-keygen, and curl. On Linux, sshd's installed privilege-separation directory must already exist. Do not change the machine's normal sshd configuration to run this check.

The helper builds `temo` in a mode-0700 scratch directory. It starts a private tmux server at 100 columns and 32 rows, a high-port loopback sshd, and a loopback HTTP server. The usable initial screen contains `temo | SSH` and `No matching routes`.

The TUI uses a scratch HOME. Its `.ssh/config` includes a scratch alias file. The SSH wrapper uses that generated config, an absolute Include path, throwaway keys, and a scratch known_hosts file. The scratch sshd disables password authentication and checks only its generated public key. Its `StrictModes no` setting permits an authorized-key file under `/tmp`. This is test setup, not a recommended SSH configuration. The helper does not change `~/.ssh`, use remote hosts, or access the machine's real Serve configuration.

Before the run, check whether real Tailscale is available. If installed, inspect only login state, without printing node or peer identities.

```sh
command -v tailscale
# Run only if installed.
tailscale status --json | python3 -c 'import json,sys; d=json.load(sys.stdin); print({"BackendState": d.get("BackendState"), "HasSelf": bool(d.get("Self"))})'
```

Do not run real Serve or Funnel mutations. The helper puts its stub first on PATH locally and in the scratch sshd's forced command. The stub's JSON shape is not proof of compatibility with a real Tailscale daemon.

If `go build` fails while obtaining VCS status, investigate the host's Git discovery first. In the verified host worktree, an unrelated ancestor `.git` directory broke Go's VCS discovery. That run used the following local-only override, approved by its supervisor. Do not change repository build settings or CI to copy that host workaround.

```sh
GOFLAGS=-buildvcs=false .cursor/skills/verify-temo/verify.py --evidence "$EVIDENCE"
```

The helper records inherited GOFLAGS and the actual Git commit in `environment.json`. An override leaves the binary unstamped.

## Doctor

The helper captures the initial screen and records the pane PID before driving each fresh TUI instance. When a screen differs from expectations, inspect these files before another run.

```sh
python3 -m json.tool "$EVIDENCE/environment.json"
ls "$EVIDENCE"/*launch.txt
python3 -c 'import pathlib,sys; print((pathlib.Path(sys.argv[1])/"result.txt").read_text())' "$EVIDENCE"
```

While a run is active, the tmux server name is `temo-verify-<helper-pid>`. Never attach to or kill a shared tmux server. Use its exact socket name to inspect the running pane.

```sh
SOCKET=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tmux_server"])' "$EVIDENCE/environment.json")
tmux -L "$SOCKET" capture-pane -p -t app
tmux -L "$SOCKET" display-message -p -t app '#{pane_pid}'
```

A live process without the expected rendered screen is not a successful doctor check.

## Drive

The executable [verify.py](verify.py) drives these journeys in one private tmux server, reopening the app twice with the same scratch config.

1. Reject a second writer. Show an invalid form and its full error. Add an SSH entry, open Ctrl+P, pick `lab` from the included scratch Host names, and save. Reopen the picker while editing the stopped entry and confirm `lab` stays selected. Inspect the saved JSON and mode 0600.
2. Search for a missing name and a saved name. Show help. Resize to 60 by 20, below the supported minimum, and back to 100 by 32.
3. Start real loopback SSH with an inherited `GatewayPorts yes`. Reach the HTTP target through `127.0.0.1` and reject a connection through `127.0.0.2`. Check the private active ownership record.
4. Open the SSH URL through a logging opener stub. Refuse editing while active. Quit, confirm HTTP still responds, reopen, refresh, and stop. Confirm the forward disappears and an unrelated scratch ControlMaster survives.
5. Reject an SSH alias with an inherited forwarding rule.
6. Add local Serve against the stub. Check stopped, start, status, URL opening, and quit/reopen. While a delayed status runs, refuse start and quit.
7. Refuse changed routes, changed node identity, unmanaged ports, Funnel, foreground sessions, unknown fields, and invalid JSON. Check that refused operations issue no mutating command.
8. Add remote Serve through the real scratch SSH server. Start, refresh, open its URL, and stop against the same stub.
9. Simulate a Serve command that applies a route and then fails. Confirm pending ownership blocks stop and no automatic rollback occurs. Display a long backend error and verify that PageDown and PageUp move the details view.

For a manual replay, use the source entry points and key sequences in each feature file. Send Escape as its own key event. The helper spaces key events by 150 ms to avoid treating consecutive Escapes as an Alt sequence.

## Evidence

`result.txt` says PASS only after the terminal journeys, backend assertions, and cleanup finish. `actions.log` records typed values and keys. `commands.jsonl` records helper command arguments and exit codes. Numbered text captures show the before and after screens. The helper also saves generated definitions, ownership records, stub Serve state and command log, remote SSH commands, opened URLs, SSH HTTP response, sshd log, and the one-writer error.

The opener stub proves that the app passes the expected URL to the OS opener command. It does not prove a browser window opens. The SSH HTTP response proves target reachability separately from setup-ready state. Stub Serve does not provide HTTPS or a certificate.

Run the Go checks separately and save their output. The runtime helper is not a substitute for race tests.

```sh
go test -race ./...
go vet ./...
gofmt -l *.go
go build -o "$EVIDENCE/temo" .
GOOS=darwin GOARCH=arm64 go build -o "$EVIDENCE/temo-darwin-arm64" .
GOOS=darwin GOARCH=amd64 go build -o "$EVIDENCE/temo-darwin-amd64" .
GOOS=linux GOARCH=amd64 go build -o "$EVIDENCE/temo-linux-amd64" .
```

Require no output from gofmt. Apply an approved host-only GOFLAGS override consistently if VCS discovery requires one. Record failures and remaining gaps. Do not relabel a stub check as live Tailscale evidence.

## Cleanup

The helper stops only its private tmux server, its owned SSH masters, its throwaway sshd, and its HTTP server, including after failure. It never kills by process name.

Evidence survives cleanup. Confirm `result.txt` and the numbered captures still exist. The helper retains the private scratch directory and prints its path. A deliberately uncertain stub route and pending journal remain there for inspection. They do not represent a real backend, and all live SSH resources must be stopped. Do not reuse the scratch config for normal work.

If interrupted in a way that prevents Python cleanup, use the exact scratch path, tmux socket name, and process IDs from that run. Stop only those resources. Retain uncertain ownership records until the backend is understood.

## Helpers

Run `.cursor/skills/verify-temo/verify.py --evidence "$EVIDENCE"` directly. It uses only Python's standard library plus the listed command-line tools. Review the helper before execution when permissions or endpoint rules differ.

Use `/maintain-verification-skill` after behavior changes. Keep source coverage and runtime coverage separate. A real-tailnet check requires a separately approved disposable node and is not part of this safe default recipe.
