# Self-update

## User journey

Run `temo update --check` to inspect the latest stable release. Run `temo update` from a directly installed official release to replace that executable. No TUI, config store, SSH, or Tailscale operation should occur.

Source boundaries are update.go, update_cli.go, main.go, .goreleaser.yaml, VERSION, and .github/workflows/release.yml.

## Isolated regression checks

Run `go test -race ./...` and `go vet ./...` on Linux and macOS. The updater suite uses an in-memory HTTP transport, generated archive fixtures, and temporary dummy executable files. It must not contact GitHub or replace the test process.

Cover latest, equal, and older stable releases; malformed metadata and checksum files; invalid archive paths, link members, duplicates, and size bounds; failed downloads and installs preserving the old file; check mode not resolving or changing an executable; unsupported build provenance; and concurrent replacement protection. Test the CLI help and invalid-argument paths without network access.

Build all four release targets. Inspect GoReleaser flags to confirm official binaries carry the release channel marker. Source and `go install` builds must not inherit that marker.

## Real release verification

After an authorized release exists, use a disposable copy in a private directory for an actual GitHub download-and-replace journey. Compare its reported version and checksum before and after; capture stdout and stderr. This is separate from fixture tests and requires an environment the user permits.

Do not tag or publish a fake release for a test. Do not run self-update against a package-manager binary or the normal installed executable. Do not claim the real release journey passed from fixture CI alone.
