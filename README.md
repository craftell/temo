# temo

遠くを、手元に。

A terminal UI for named SSH forwards and Tailscale Serve routes. Manage remote development services from your Mac or Linux terminal.

## Install

Release binaries support Linux and macOS on amd64 and arm64. These commands use the installer on `main`.

```sh
curl -fsSL https://raw.githubusercontent.com/craftell/temo/main/install.sh | sh
```

To download and read the script first:

```sh
curl -fsSL https://raw.githubusercontent.com/craftell/temo/main/install.sh -o install.sh
less install.sh
sh install.sh
```

The installer checks SHA-256 before installing to `~/.local/bin`. It never uses sudo or edits shell profiles. Set `TEMO_VERSION` to an existing release tag to choose a version, or `TEMO_INSTALL_DIR` to choose another directory. Add the printed directory to PATH if needed.

With Go 1.25+, you can also install with Go:

```sh
go install github.com/craftell/temo@latest
```

Or build from source:

```sh
git clone https://github.com/craftell/temo.git
cd temo
go build -o temo .
./temo --version
```

`temo --version` or `temo -v` prints the release version and exits without opening the TUI. An unstamped build uses module or VCS build information when available, otherwise `dev`.

## Upgrade

```sh
temo update --check
temo update
```

The check command reports the latest stable GitHub release without changing files. The update command selects the archive for this binary's operating system and architecture, verifies its SHA-256 checksum, validates the archive, and atomically replaces the installed executable. Neither command opens the TUI or changes definitions, ownership records, SSH forwards, or Tailscale routes. Existing running TUI processes continue using their loaded version; restart them to use the new one.

Self-update supports directly installed official release binaries, including the curl installer. Go-installed and development builds can check for releases but must upgrade using their original method, such as `go install github.com/craftell/temo@latest`. The operating system resolves the running executable path. Recognized package-manager target paths and any unresolved final symlink are refused; use the original installation method. Launching through a symlink does not itself prove package-manager ownership. No sudo is attempted. A non-writable install directory is reported rather than bypassed.

Downloads use official GitHub HTTPS endpoints. Checksums detect corrupted downloads; they are not an independent publisher signature. The updater uses a persistent lock file beside the executable to serialize updates. Do not remove it while an updater is running.

Binaries older than 0.3.0 do not have this command. Run the installer once to obtain a version that includes it. The command becomes available after that release is published, not merely when this PR is merged.

## Run

Requires OpenSSH and a terminal. Serve also requires Tailscale 1.52+ on the execution host.

```sh
temo
```

From a source checkout, use `go run .`.

The first run opens the SSH tab. Use Left/Right or `1`/`2` to switch between SSH forwarding and Serve HTTPS. Press `a` to create an entry in the current tab. The form shows only that kind’s fields; editing an existing entry preserves its kind.

For SSH, choose an alias with Ctrl+P in the form or type one, then set the local port, remote target host, and target port. For Serve, leave the SSH host empty to run Tailscale locally, or enter an SSH alias to run Tailscale on that remote host. Remote hosts need a POSIX-compatible login shell and `tailscale` in PATH.

The SSH alias must already work noninteractively. Authenticate and accept its host key with your normal SSH client first. temo does not request passwords or weaken host-key checks.

## Keys

- Up/Down or `j`/`k` select a row. Left/Right or `1`/`2` switch SSH and Serve tabs. Each tab remembers its filter and selection.
- `/` searches within the current tab. Enter or Up/Down returns to row navigation; Esc clears the filter.
- `a` adds an entry. `e` edits a stopped entry.
- `s` starts the selected entry. `x` stops its managed backend.
- `r` refreshes every saved route across both tabs. Startup does this automatically. `o` opens the selected URL.
- `d` or Enter shows full details. `p` shows the resolved config file path. `?` shows help. `q` quits.
- In a form, Ctrl+P lists Host names from `~/.ssh/config` and its Include files. Use arrows and Enter to choose, or Esc to return without a change. Wildcard patterns and Match blocks are skipped. Manual names and blank-host local Serve still work.
- In a form, Up/Down or Tab/Shift+Tab changes fields. Enter advances; Left/Right moves the text cursor. `j` and `k` are ordinary text. Ctrl+S saves, Esc cancels, and Ctrl+D opens a scrollable form error.

Status checks run up to four at a time and update rows as they finish. Navigation, search, help, forms, and quitting stay available during refresh. Repeating `r` while a sweep is running does not duplicate checks. A selected entry cannot be changed until its in-flight check finishes. Start, Stop, Open, and edit/save checks remain serialized; quitting waits for these explicit operations.

A ready status means the backend is configured. It does not prove that the application responds. SSH entries open an HTTP URL; use your own client for non-HTTP services.

## Persistence and ownership

Quitting temo leaves started forwards and Serve routes running. Reopening temo checks every saved route without starting or stopping anything. Status checks never write definitions or ownership journals. A confirmed absent backend with retained ownership displays `stopped; cleanup needed`; use `x` to release that ownership before editing. Pending or ambiguous outcomes remain errors. Reopen temo with the same config directory to inspect or stop routes. No background temo daemon is required.

Definitions live in `config.json` in the OS user config directory under `temo`. On macOS this is normally `~/Library/Application Support/temo/config.json`. On Linux it is `$XDG_CONFIG_HOME/temo/config.json`, or `~/.config/temo/config.json` when XDG_CONFIG_HOME is unset. Press `p` to see the exact resolved path, including any `--config-dir` override. Per-entry `owner-<id>.json` journals and SSH control sockets live beside it. Use `--config-dir /absolute/private/path` to choose another directory. The directory must have mode 0700. Config files contain connection metadata, not passwords or keys.

Only one temo process can use a config directory at a time. Do not edit config or ownership files while it is running. Keep ownership records until every managed backend is stopped. Losing them can leave a backend running that temo will refuse to adopt.

SSH uses a dedicated ControlMaster socket, binds to 127.0.0.1, and preserves SSH config identities, agents for authentication, and ProxyJump. It disables forwarding the agent and X11 to the destination. Aliases with inherited LocalForward, RemoteForward, or DynamicForward rules are rejected. Stop addresses only the saved temo control socket.

Serve owns an entire dedicated HTTPS port with one root route to `http://127.0.0.1:PORT`. Choose an unused port such as 8443. It refuses occupied ports, Funnel on that port, foreground Serve sessions on that port, changed node identities, and observed changes to managed routes. It never invokes Funnel or resets Serve.

Serve mutation uses the Tailscale CLI. Its before/after checks are not an atomic lock against other programs. A concurrent external writer can race with a change. Do not change the same device's Serve configuration concurrently. Strict conditional LocalAPI updates are future work. Remote Serve requires a stable alias pointing to one node, not a load balancer. Do not change SSH aliases or Tailscale profiles during an operation. Each remote command opens a separate SSH connection, so node identity checking is not atomic across those changes.

An interrupted or ambiguous Serve operation keeps a pending ownership record and blocks further mutation. Inspect `tailscale serve status --json` on the execution host before resolving it. There is no automatic rollback that could erase another program's change.

## Development and verification

```sh
go mod tidy
go test -race ./...
go vet ./...
go build -o temo .
```

CI runs tests, vet, and builds on Linux and macOS. Linux CI also drives the real binary through a PTY with command-denying fake backends. That smoke test checks terminal key decoding, tab-specific forms, startup progress, cancellation, and unchanged definitions/journals without opening a network tunnel. It also cross-builds macOS arm64, macOS amd64, and Linux amd64. Dependency checksums are generated by Go and checked into go.sum. CI also saves resolved module files as artifacts.

[Verification guide](.cursor/skills/verify-temo/SKILL.md) includes an executed PTY helper for the real terminal UI, throwaway loopback SSH forwarding, and stubbed local and SSH Serve. It captures screens and checks backend state, including stale ownership, refresh concurrency, and quit during refresh. Real Tailscale mutations, tailnet HTTPS, and graphical browser opening remain unverified. Mocked tests alone do not prove a live connection or rendered terminal interaction.

[Design decisions](docs/design.md) explain the ownership boundary and current limits.

Licensed under the [MIT License](LICENSE).

## Preparing a release

Every feature or fix PR includes its intended next version in `VERSION` and notes under `docs/releases/`. The release workflow rejects a tag that does not match `VERSION`. Source builds still report their module or development identity; changing `VERSION` alone does not make a build official. Publishing still requires an authorized matching tag on the intended tested commit.
