---
name: verify-temo
description: Verify temo after UI, connection, release, or installer changes. Drive the real TUI, test loopback SSH and stub Serve, and check version flags and checksum-verified installation with local snapshot artifacts, and drive `temo update` against a loopback GitHub release imitation.
---

# Verify temo

Run the [feature map](features/README.md) against the built app. The keyboard, modal, and loopback helpers drive the TUI through terminal keys. The release and update helpers exercise the command line and installer.

An earlier keyboard and loopback run was executed on Linux amd64 with Go 1.25.5, tmux 3.4, and OpenSSH 9.6p1, on `fix/keyboard-navigation-and-status` at base `8a17233` plus the start-failure wording fix. Both [verify-keyboard.py](verify-keyboard.py) and [verify.py](verify.py) printed PASS, and `GOFLAGS=-buildvcs=false` was set for the local build (see Launch). The extended run covered real loopback SSH forwarding, stale and failed-start ownership, the tab and refresh journeys below, and quit during refresh. Serve remains stubbed. Not run: native macOS terminal, real Tailscale Serve or Funnel, tailnet HTTPS, and a graphical browser. After later changes, rerun the helpers or inspect that commit's CI result and retained artifacts before claiming runtime coverage.

Self-update was last executed on Linux amd64 with Go 1.25.5, Python 3.12, and OpenSSL, on `feat/self-update` at base `08e8206` plus the verification-skill edits (see [self-update](features/self-update.md)). [verify-update.py](verify-update.py) printed PASS for 53 scenarios against the real binary, including the real GoReleaser snapshot archive. The release helper printed PASS with the stamped channel check, and the keyboard smoke printed PASS. A scratch binary stamped 0.1.0 upgraded to the published v0.2.0 over real GitHub inside a scratch directory. Not run: macOS, an upgrade to the unpublished 0.3.0, and the tag-triggered release workflow.

The route table and linked port defaults were last executed on Linux amd64 with Go 1.25.5, Python 3.12, tmux 3.4, OpenSSH 9.6p1, and a scratch GoReleaser 2.12.7, on `feat/k9s-route-table` at merge commit `04d1826` (that branch merged with main at v0.3.0, `fbe3bfa`) plus the verification-skill and release-note edits. `go test -race`, `go vet`, `gofmt -l`, the four release cross-builds, and `py_compile` of the helpers passed. [verify-keyboard.py](verify-keyboard.py) printed PASS for the aligned SSH and Serve tables at 120, 50, and 40-by-10 sizes, color and `NO_COLOR=1`, and the linked port defaults. [verify.py](verify.py) printed PASS, and the release helper printed PASS with snapshot version 0.3.0 (from the v0.3.0 tag). A snapshot with `GORELEASER_CURRENT_TAG=v0.4.0` and no tag created stamped `0.4.0-SNAPSHOT-04d1826` and `main.updateChannel=github-release`. [verify-update.py](verify-update.py) printed PASS for 53 scenarios using that 0.4.0 archive. A scratch binary stamped 0.1.0 upgraded to the published v0.3.0 over real GitHub inside a scratch directory. Not run: macOS or any native terminal, `TERM=dumb` in a PTY (the Go renderer test covers it), light-background or low-contrast terminal themes, real Tailscale or a remote SSH host, the tag-triggered release workflow, and any upgrade to v0.4.0.

## Safe keyboard smoke (Linux CI)

Use this focused check for tabs, form navigation, config-location discovery, and nonblocking status. It needs Python 3.9+ and Go 1.25+, with no tmux, SSH server, credentials, or network backend.

```sh
EVIDENCE=$(mktemp -d /tmp/temo-keyboard-evidence-XXXXXXXX)
python3 .cursor/skills/verify-temo/verify-keyboard.py --evidence "$EVIDENCE"
```

The helper builds once, launches the real app in a private 120-by-32 PTY, and seeds four saved entries plus stale active ownership records in a temporary HOME/config directory. CI may pass `--binary ./temo` after its normal build. The app's PATH contains only stubs that allow exact SSH control checks and local/remote Serve status commands; they reject every other invocation. No SSH key, listener, tunnel, URL opener, or live Tailscale command is created.

The doctor requires rendered SSH and Serve tabs and the expected selected fixture. The helper drives actual escape sequences for arrows and Shift+Tab, literal j/k, Enter, Tab, form cancellation, the p config-location view, and its help hint. It holds Serve reads behind a fixture gate while checking navigation and guarded actions on a pending entry. The first sweep must inspect all four entries despite the selected tab/filter. A second sweep is cancelled by q; the app and delayed child must stop before cleanup.

Evidence includes numbered current-screen captures, terminal.raw, actions.log, backend.jsonl, before/after file hashes and permissions, fixture JSON, cancellation.json, environment.json, and result.txt. Assertions use a bounded VT screen model, not old text left in the output stream. An unsupported terminal sequence is a harness failure to investigate, not permission to weaken assertions. Config and ownership bytes must remain unchanged. PASS is written only after cleanup; failure evidence is retained and Linux CI uploads it even on failure.

The same helper also covers the route table: aligned SSH and Serve columns at 120 columns, the two-line layout at 50 columns including the 50-by-14 minimum, the too-small notice at 40 by 10 and the restore afterward, color on and `NO_COLOR=1`, and the linked Local port defaults, override, paste, cancellation, and unchanged files. The Go renderer tests cover width, wide characters, and `TERM=dumb`.

The helper terminates only its own PTY process group. Its fixture directory is retained at the path in environment.json and contains no credentials. This focused check does not replace the extended loopback journey below, Go race tests, or real-backend validation.

## Launch

From the repository root, choose a new evidence directory outside the checkout. The directory must not contain evidence from another run.

```sh
EVIDENCE=$(mktemp -d /tmp/temo-evidence-XXXXXXXX)
.cursor/skills/verify-temo/verify.py --evidence "$EVIDENCE"
```

Requires Python 3.9+, Go 1.25+, tmux, OpenSSH client and server, ssh-keygen, and curl. On Linux, sshd's installed privilege-separation directory must already exist. Do not change the machine's normal sshd configuration to run this check.

The helper builds `temo` in a mode-0700 scratch directory. It starts a private tmux server at 100 columns and 32 rows, a high-port loopback sshd, and a loopback HTTP server. The usable initial screen contains `[1 SSH]`, `[2 Serve]`, and `No matching routes`.

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

The executable [verify.py](verify.py) drives these journeys in one private tmux server, reopening the app with the same scratch config.

1. Reject a second writer. Show an invalid form and its full error. Add an SSH entry from the SSH tab, open Ctrl+P, pick `lab` from the included scratch Host names, and save. Reopen the picker while editing the stopped entry and confirm `lab` stays selected. Inspect the saved JSON and mode 0600.
2. Open p and verify the actual config.json path. Search for a missing name and a saved name in the active tab. Show help. Resize to 60 by 20, then below the minimum at 40 by 10, and back to 100 by 32.
3. Start real loopback SSH with an inherited `GatewayPorts yes`. Reach the HTTP target through `127.0.0.1` and reject a connection through `127.0.0.2`. Check the private active ownership record.
4. Open the SSH URL through a logging opener stub. Refuse editing while active. Quit, confirm HTTP still responds, reopen, refresh, and stop. Confirm the forward disappears and an unrelated scratch ControlMaster survives.
5. Reject an SSH alias with an inherited forwarding rule.
6. Add local Serve from its tab against the stub. Check stopped, start, status, URL opening, and quit/reopen. While delayed read-only status runs, refuse start/stop/edit on the pending entry, navigate between tabs, and permit quit. Reopen before continuing.
7. Refuse changed routes, changed node identity, unmanaged ports, Funnel, foreground sessions, unknown fields, and invalid JSON. Check that refused operations issue no mutating command.
8. Add remote Serve through the real scratch SSH server. Start, refresh, open its URL, and stop against the same stub.
9. Simulate a Serve command that applies a route and then fails. Confirm pending ownership blocks stop and no automatic rollback occurs. Display a long backend error and verify that PageDown and PageUp move the details view.
10. Stale SSH ownership: kill the real scratch ControlMaster from outside. Refresh must show `stopped; cleanup needed` and keep the journal byte for byte. Edit must be refused. Stop must release the journal and the socket record.
11. Failed SSH start: occupy the local port first. Start must fail with `start outcome may be uncertain; use Stop (x)` and keep a pending journal. Refresh and a retried start must refuse to change it. Stop must release it.
12. Stale Serve ownership: remove the stub route after a start. Refresh must show `stopped; cleanup needed` and issue no mutating stub command. Stop must release ownership without touching the route.
13. Tabs and refresh: seed five more Serve entries (two through SSH) beside the existing entries. Check that the SSH and Serve lists and filters are independent and that the config path opens during a sweep. With a slow stub, three `r` presses must produce one status call per Serve entry (six), at most four concurrent, and navigation must keep working. A further sweep cut short by quit must kill its local check processes before they finish, and config and ownership bytes must not change.

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
GOOS=linux GOARCH=arm64 go build -o "$EVIDENCE/temo-linux-arm64" .
```

Require no output from gofmt. Apply an approved host-only GOFLAGS override consistently if VCS discovery requires one. Record failures and remaining gaps. Do not relabel a stub check as live Tailscale evidence.

## Release and installer checks

Use this additional recipe after changing `install.sh`, version reporting, `.goreleaser.yaml`, or the release workflow. Requires GoReleaser on PATH. The executed run used a pinned GoReleaser 2.12.7 in a scratch directory, not a global install. ShellCheck is used if available; the executed run used a scratch copy of 0.11.0.

```sh
RELEASE_EVIDENCE=$(mktemp -d /tmp/temo-release-evidence-XXXXXXXX)
.cursor/skills/verify-temo/verify-release.py --evidence "$RELEASE_EVIDENCE"
```

If tools are outside PATH, pass their executable paths with `--goreleaser` and `--shellcheck`. Use the same approved local GOFLAGS override as the TUI recipe if host VCS discovery needs it.

The helper runs `goreleaser check` and `goreleaser release --snapshot --clean`. It copies the checked config and changes only `dist` to a fresh external scratch directory. It builds all four Linux/macOS amd64/arm64 archives, verifies their checksums and MIT license, and checks both version flags without HOME or a TUI. A loopback HTTP fixture then serves those real archives to the unmodified installer.

Require successful latest-release installation, an explicit `TEMO_VERSION` install to a path with spaces, the correct PATH hint, and a shasum fallback when available. Wrong checksums must preserve an existing binary and must not create a new install directory. Unsupported OS and CPU values must fail before any request. Missing releases and invalid versions must give clear errors. The scratch shell profile must remain unchanged.

Read [release and installation](features/release-and-install.md) for the complete contract. `result.txt`, command logs, HTTP requests, installed version, and cleanup notes remain in the evidence directory. Snapshot archives and scratch installations are retained at the path in `environment.json`. No release asset is published. The helper checks that tag refs did not change and stops its HTTP server before recording PASS.

The helper's snapshot takes its version from the newest tag. To confirm the version stamped for the VERSION file before a tag exists, run a scratch snapshot with `GORELEASER_CURRENT_TAG=v$(cat VERSION)` and a config copy whose `dist` is a scratch directory, then check `temo --version` and `go version -m` on the extracted binary and that `git for-each-ref refs/tags` did not change. This sets an environment variable only; it does not create a tag.

Do not replace `--snapshot` with a real release command. Do not create or push a tag during verification. The tag-only publishing workflow itself remains unexecuted until a separately approved first release.

## Cleanup

The TUI helper stops only its private tmux server, its owned SSH masters, its throwaway sshd, and its HTTP server, including after failure. It never kills by process name.

Evidence survives cleanup. Confirm `result.txt` and the numbered captures still exist. The helper retains the private scratch directory and prints its path. A deliberately uncertain stub route and pending journal remain there for inspection. They do not represent a real backend, and all live SSH resources must be stopped. Do not reuse the scratch config for normal work.

If interrupted in a way that prevents Python cleanup, use the exact scratch path, tmux socket name, and process IDs from that run. Stop only those resources. Retain uncertain ownership records until the backend is understood.

## Helpers

Run `python3 .cursor/skills/verify-temo/verify-keyboard.py --evidence "$EVIDENCE"` for the Linux-only safe keyboard smoke, `.cursor/skills/verify-temo/verify.py --evidence "$EVIDENCE"` for extended loopback terminal checks, `.cursor/skills/verify-temo/verify-release.py --evidence "$RELEASE_EVIDENCE"` for snapshot and installer checks, and `.cursor/skills/verify-temo/verify-update.py --evidence "$UPDATE_EVIDENCE"` for the updater journeys. Use a separate empty evidence directory for each. All helpers use Python's standard library plus their listed command-line tools. Review the helpers before execution when permissions or endpoint rules differ.

Use `/maintain-verification-skill` after behavior changes. Keep source coverage and runtime coverage separate. A real-tailnet check requires a separately approved disposable node and is not part of this safe default recipe.

## Self-update checks

Read [self-update](features/self-update.md) when touching update.go, update_cli.go, main.go dispatch, release provenance, VERSION, or release packaging. It has three layers, each with its own evidence.

1. The Go fixture suite in `go test -race ./...` uses an in-memory transport and dummy executables. It never contacts GitHub.
2. `python3 .cursor/skills/verify-temo/verify-update.py --evidence "$UPDATE_EVIDENCE"` runs the real binary through a loopback TLS proxy that imitates the GitHub API, redirect, and CDN hosts. It checks success, check-only, downgrade refusal, checksum and archive attacks, size and redirect bounds, offline and untrusted TLS, install-directory and package-path refusal, concurrent updaters, and a real PTY run. Pass `--release-archive` with the snapshot archive from the release recipe to cover the real GoReleaser layout.
3. A scratch binary stamped below the latest release can check and upgrade against the public release inside a scratch directory.

Use a separate empty evidence directory for each helper. The release recipe now also asserts the stamped `main.updateChannel` and version with `go version -m`, and that the archive holds exactly `temo`, `LICENSE`, and `README.md` as regular files.

The updater is never exercised on the installed `temo`, a package-manager binary, or a real release that was created for the test. Record the exact tested commit and CI URL in the PR. Never create a release or tag as part of verification without separate authorization.

## Table presentation changes

After route-table or port-default changes, use the expanded [terminal navigation recipe](features/terminal-navigation.md#route-table-and-port-defaults). Review current-screen text captures and terminal.raw from the safe PTY smoke. Read the table renderer tests alongside the captures. Preserve the distinction between generated terminal evidence, a human visual review, and backend integration checks. The table feature adds no automatic route mutations.

## Modal and deletion changes

Use [verify-modal.py](verify-modal.py) and the [modal recipe](features/terminal-navigation.md#modal-interactions-and-deletion) after changes to deletion, focus, or modal rendering. It needs Linux, Python 3.9+, and Go 1.25+, with no SSH server or credentials. Run the existing safe keyboard smoke and the extended loopback helper as well. Saving now keeps the active filter, so the loopback helper explicitly selects each added route before any backend action.

```sh
MODAL_EVIDENCE=$(mktemp -d /tmp/temo-modal-evidence-XXXXXXXX)
python3 .cursor/skills/verify-temo/verify-modal.py --evidence "$MODAL_EVIDENCE"
```

The modal helper writes only temporary definitions and ownership fixtures. It refuses all mutating backend commands and keeps the unrelated ownership journal unchanged. It adds a disposable journal after opening a stopped route's dialog to prove the confirmation checks fresh status. It temporarily replaces its own config file with an empty directory to force a failed save and unreadable reload, then restores the file and checks recovery with r. It never alters the user's config.

The helper checks Cancel, Esc and n without backend calls, frozen selection, ignored pasted confirmation, hidden-confirmation refusal after resize, exact surviving definitions, and unknown-outcome blocking. Its numbered text and SVG captures show the current screen, not historical terminal output. It retains before/after hashes, fixture JSON, raw terminal output, actions and backend logs. PASS is written only after cleanup.

Do not infer that a failed Store.Save left disk unchanged. Unit tests must cover a save that reports an error after the new config became readable, and an unreadable outcome that blocks further mutation. Confirm deletion checks status again and permits only `stopped`, never `inactive`.

The modal and deletion helpers were executed on Linux amd64 with Go 1.25.5 and Python 3.12 for the 0.5.0 change based on `eedf2354`. Both `verify-keyboard.py` and `verify-modal.py` passed with disposable config and read-only denying backends. The real PTY exercised cancellation, retained-ownership refusal, confirmed persisted deletion, nested dialogs, and resize. Independent review also added unit regressions preventing invisible confirmations below 50 by 14. See the [retained PTY renderings](../../../docs/verification/modal-0.5.0/README.md). Native macOS terminals, real SSH/Tailscale routes, and release publication were not run.

A maintenance run on 2026-10-21 tested `cc93fec` plus skill-only corrections on Linux amd64, Go 1.25.5, Python 3.12.12, and tmux 3.4. The original extended helper failed because it acted on an old filtered entry after Add. After explicit selection was added, `verify.py` passed its full real-loopback SSH and stub Serve journeys. The expanded `verify-modal.py` and unchanged `verify-keyboard.py` passed. Fresh Go race tests, vet, gofmt and diff checks passed. The release helper passed with GoReleaser 2.12.7 and ShellCheck 0.11.0, including all four archive targets. The update helper passed 53 loopback TLS scenarios with the real snapshot archive. An environment-only `GORELEASER_CURRENT_TAG=v0.5.0` snapshot stamped `0.5.0-SNAPSHOT-cc93fec`; tag refs did not change.

That run used the approved host-only `GOFLAGS=-buildvcs=false` because Go's VCS discovery failed with exit 128 in the worktree. Evidence is retained in the review task's `data/modal-definition-delete/` directory, including the original failure and corrected runs. The added failure checks ran against the real binary. The after-rename save-error case remains a Go regression, not a real filesystem fault test. Native macOS, real Tailscale, remote SSH, public-release self-update and publishing were not run in this maintenance pass.

## Alternative definition validation

For changes to definition validation, also run the [saved-entries alternative-definition recipe](features/saved-entries.md#alternative-definitions-051). The `verify-definitions.py` helper drives the real Linux TUI without available SSH/Tailscale commands. Keep port availability and connection checks at Start, not config Save/Load. Preserve ID uniqueness, structural validation, injection protection, and owned/pending backend safety guards.

Validation change verification executed on 2026-10-24 in Linux amd64 with Go 1.25.5: `go test -race ./...`, `go vet ./...`, unchanged module lockfiles after `go mod tidy`, `gofmt`, Python helper compilation, and darwin/linux amd64/arm64 cross-builds passed. The real-PTY `verify-definitions.py`, `verify-keyboard.py`, and `verify-modal.py` helpers passed. The first new definitions-helper run used an incorrect table-count expectation (`3/3` rather than the rendered `SERVE routes [3]`); inspecting the failure capture confirmed that all three definitions had saved, and the corrected helper passed. Runtime conflicts are covered by fake-runner Go tests, not live backend tests. Not executed for this change: native macOS terminal, real SSH daemon forwarding, real Tailscale or tailnet HTTPS.
