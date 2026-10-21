# Self-update

## User journey

Run `temo update --check` to inspect the latest stable release. Run `temo update` from a directly installed official release to replace that executable. No TUI, config store, SSH, or Tailscale operation should occur.

Source boundaries are update.go, update_cli.go, main.go, .goreleaser.yaml, VERSION, and .github/workflows/release.yml.

The subcommand is dispatched from `os.Args[1]` before flag parsing and config initialization. `temo --config-dir DIR update` is therefore not an update: Go stops flag parsing at the first non-flag word and the TUI path starts. Run `temo update` with nothing before it.

## Isolated regression checks

Run `go test -race ./...` and `go vet ./...` on Linux and macOS. The updater suite uses an in-memory HTTP transport, generated archive fixtures, and temporary dummy executable files. It must not contact GitHub or replace the test process.

Cover latest, equal, and older stable releases; malformed metadata and checksum files; invalid archive paths, link members, duplicates, and size bounds; failed downloads and installs preserving the old file; check mode not resolving or changing an executable; unsupported build provenance; and concurrent replacement protection. Test the CLI help and invalid-argument paths without network access.

Build all four release targets. Inspect GoReleaser flags to confirm official binaries carry the release channel marker. Source and `go install` builds must not inherit that marker.

## Live fixture run (real binary, loopback)

```sh
UPDATE_EVIDENCE=$(mktemp -d /tmp/temo-update-evidence-XXXXXXXX)
python3 .cursor/skills/verify-temo/verify-update.py --evidence "$UPDATE_EVIDENCE"
```

Needs Python 3.9+, Go 1.25+, and OpenSSL. The helper builds real `temo` binaries stamped 0.2.0, 0.3.0, 0.4.0 (all `github-release`) and one unstamped source build. It creates a throwaway CA and runs a TLS-terminating CONNECT proxy on 127.0.0.1 that answers for api.github.com, github.com, release-assets.githubusercontent.com, and objects.githubusercontent.com. Each run sets `HTTPS_PROXY` and `SSL_CERT_FILE` for the child only. The unmodified updater therefore uses its real URLs, redirect checks, and TLS verification. Any other host the updater tries is logged as a refused connect and fails the scenario.

Every scenario gets its own install directory and a HOME seeded with sentinel `config.json`, `ownership.json`, and `~/.ssh/config`. PATH holds `ssh`, `tailscale`, and `ssh-keygen` stubs that log and fail. A scenario fails if HOME changes, a stub runs, a stray file appears beside the executable or its install directory, an Authorization or Cookie header is sent, or the installed bytes differ from what the scenario expects.

Scenarios, each with its expected exit status and message:

- Success: check-only (no lock file, one API request), update to the new release (mode 0755, `temo --version` reports 0.3.0), equal version, newer installed version (no downgrade), unstamped source build (check works; update refused), and a Linux symlinked launch (updates the resolved regular file, leaves the link).
- Metadata: draft, prerelease flag, prerelease tag, HTTP 403, invalid JSON, oversized JSON, an API redirect to a foreign host.
- Checksums: mismatch, missing entry, malformed hex, extra fields, duplicate entries.
- Archive: truncated body with a consistent checksum, asset size differing from metadata, not gzip, trailing data, `../temo`, absolute path, nested path, symlink, hardlink, directory, extra member, duplicate binary, non-executable binary, empty binary, no binary, and a 90 MB expanded archive.
- Transport: interrupted download, declared asset above 64 MiB, streamed body above 64 MiB, redirect to a foreign host, an `http://` redirect, a redirect loop, asset HTTP 404, unreachable proxy, and an untrusted certificate.
- Install: read-only install directory (refused at the lock, binary untouched; skipped as success when run as root), package-manager paths (`Cellar`, `nix/store`, `snap`) refused before any request, and two concurrent updaters where the second must report that another update is running while the first completes.
- Real terminal: `temo --version`, `-v`, `update --check`, and `update --help` in a PTY with a controlling terminal. Output must be plain text with no alternate-screen or cursor-hide sequences, and HOME must not change.

To prove the real GoReleaser layout satisfies the updater's strict member rules, run the release recipe first and pass its linux/amd64 snapshot archive. The scratch path is in the release evidence `environment.json`.

```sh
python3 .cursor/skills/verify-temo/verify-update.py --evidence "$UPDATE_EVIDENCE" \
  --release-archive "$SCRATCH/dist/temo_<snapshot-version>_linux_amd64.tar.gz"
```

Evidence: `result.txt`, `scenarios.json` (stdout, stderr, requests, hashes, problems), `requests.jsonl`, `environment.json` with binary hashes and the tested commit, and `terminal-*.raw`. After changing the updater, break one rule on purpose (for example allow another host in `allowedUpdateURL`, or skip the checksum comparison) in a scratch copy and confirm the matching scenario fails. A suite that cannot fail proves nothing.

## Real GitHub, scratch binary only

This exercises the real TLS, redirect hosts, and archive layout of a published release without touching anything installed. Build a binary stamped older than the latest release into a scratch directory and give it a scratch HOME.

```sh
go build -ldflags "-X main.version=0.1.0 -X main.updateChannel=github-release" -o "$SCRATCH/install/temo" .
HOME="$SCRATCH/home" XDG_CONFIG_HOME="$SCRATCH/home/.config" "$SCRATCH/install/temo" update --check
HOME="$SCRATCH/home" XDG_CONFIG_HOME="$SCRATCH/home/.config" "$SCRATCH/install/temo" update
```

The check is a read-only request to the public API. The update downloads the public archive and replaces only `$SCRATCH/install/temo`. Pick a stamped version below the latest published release, never the installed `temo`. Compare `--version` and the file hash before and after, and confirm `$SCRATCH/home` stays empty. A binary from a release older than 0.3.0 has no `update` command, so run `--version` on it and do not repeat `update`: without that subcommand the arguments start the TUI.

The upgrade of a scratch 0.1.0 binary to the published v0.3.0 was executed on Linux amd64 at merge commit `04d1826` of the route-table branch: `update --check` reported `0.1.0 → v0.3.0`, `update` replaced only the scratch binary (`--version` then reported 0.3.0), the scratch HOME stayed empty, and only `temo` and `temo.update.lock` were in the install directory. A run on macOS and a real-GitHub upgrade to v0.4.0 were not executed in that run. v0.4.0 was confirmed published during the 2026-10-21 maintenance pass, which tested the loopback fixture rather than a public-release upgrade. Do not tag or publish a fake release. Do not run self-update against a package-manager binary or the normal installed executable. Do not claim a journey to a release passed from fixture runs alone.

## Gotchas

Checksums and archive come from the same release, so they detect corruption but are not an independent publisher signature.

`os.Executable` already resolves symlinks on Linux, so the "refuse a final symlink" branch is only reachable on macOS or through the injected test executable. The Go suite covers that branch. Linux live runs cover the resolve-and-update behavior instead.

`temo.update.lock` stays beside the executable after a run, and the lock is taken before the version comparison. An up-to-date `temo update` in a read-only directory fails with the lock message rather than "up to date".

A snapshot build is stamped like `0.2.0-SNAPSHOT-<sha>`, which is not stable semver, so its `update` is refused even though it carries the release channel. Assert that stamp with `go version -m` (the release recipe does), not by running `update`.

The proxy fixture replaces network access only. It does not prove GitHub's current redirect hosts; the scratch run against the public release does.
