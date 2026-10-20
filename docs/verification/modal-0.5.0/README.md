# Modal verification for 0.5.0

These SVGs are generated from the current screen cells observed by the Linux PTY helper. They are not screenshots of a native macOS terminal. The fixtures contain no credentials and do not use real SSH or Tailscale routes.

- [Delete confirmation](delete.svg) shows the frozen definition and Cancel selected by default.
- [SSH host picker](host-picker.svg) shows a nested picker over an unsaved form and the route list.
- [Small form](small-form.svg) shows the focused local-port field and actions at 50 columns and 14 rows.

Regenerate the captures from the tested source with these commands. Each evidence directory must be empty.

```sh
go build -o /tmp/temo-modal .
python3 .cursor/skills/verify-temo/verify-modal.py --binary /tmp/temo-modal --evidence /tmp/temo-modal-evidence
python3 .cursor/skills/verify-temo/verify-keyboard.py --binary /tmp/temo-modal --evidence /tmp/temo-keyboard-evidence
```

The helpers retain numbered text and SVG captures, raw terminal output, exact key actions, disposable config, and backend command logs. CI uploads both evidence directories for each run.

Executed on Linux amd64 with Go 1.25.5 and Python 3.12. Both helpers passed. `go test -race ./...`, `go vet ./...`, and the Linux and macOS amd64 and arm64 cross-builds passed. The test suite covers unknown save outcomes, stale observations, hidden confirmations below the minimum terminal size, and unsafe backend states.

The intended next release is 0.5.0. Source builds retain their development or VCS identity. No tag or release was created. Native macOS terminal appearance, real Tailscale, remote SSH, and release publication were not tested.
