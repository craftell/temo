# Release and installation

## Sub-features

Print the version without starting the TUI. Build four predictable release archives, their SHA-256 checksums, and release notes. Install the matching binary with a checksum check, no sudo, and no shell-profile changes.

Source entry points are `main.go`'s version flags, `version.go`, `install.sh`, `.goreleaser.yaml`, and `.github/workflows/release.yml`.

## How to get to it (user POV)

Use the README's curl installer or download and read it first. The default destination is `~/.local/bin`. Set `TEMO_VERSION` to choose an existing release, or `TEMO_INSTALL_DIR` to choose the destination. Run `temo --version` or `temo -v` to inspect the installed version.

The release workflow runs only when an approved `v*` tag is pushed. Creating a tag or publishing a release is not part of verification.

## Driving it with the release helper

Run [the release recipe](../SKILL.md#release-and-installer-checks). The helper uses GoReleaser snapshot mode and overrides only the output directory in a copied config. Require four archives named `temo_<version>_<os>_<arch>.tar.gz` for darwin/linux and amd64/arm64, with exactly matching entries in `checksums.txt` and the requested MIT license inside each archive.

The unmodified installer uses `TEMO_RELEASE_BASE_URL` to reach a trusted loopback HTTP fixture instead of GitHub. The fixture serves the real snapshot archives and a latest-release redirect. No mock binary replaces the app.

Require the installed binary's two version flags to match the snapshot version. Test latest and explicit-version installs, a path with spaces, PATH hints, and the shasum branch when available. Reject wrong checksums without replacing a binary or creating an install directory. Reject unsupported OS and CPU before network access. Reject invalid versions and missing assets with clear messages. Inspect the HTTP request log and confirm a version override did not request latest.

## Gotchas

The public curl command installs published assets only. The 2026-10-21 maintenance check found v0.1.0 through v0.4.0 published. The snapshot run does not publish a release. Checksums verify download integrity, not a publisher signature. `TEMO_RELEASE_BASE_URL` is a test or trusted-mirror override; normal installs use GitHub HTTPS.

Only the native snapshot binary is executed by the helper. The other three targets are built, archived, and checksummed, not run. Real tag-triggered publishing, GitHub release permissions, and installation on other operating systems remain unverified until separately approved runs. Snapshot verification must never create or push a tag.

The release helper also asserts that the native snapshot binary was linked with the tag-derived version and `main.updateChannel=github-release`, that a source build carries no channel marker, and that every archive contains exactly `temo`, `LICENSE`, and `README.md` as regular files with an executable binary. These are the contract `temo update` relies on; see [self-update](self-update.md).
