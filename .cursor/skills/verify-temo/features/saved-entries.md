# Saved entries

## Sub-features

Add and edit named SSH or Serve connections in separate tabs. Choose an SSH Host name or type one. Validate forms, persist definitions, filter within a tab, discover the actual config path, and exclude a second writer.

Source entry points are `ui.go`'s `saveEditor`, `newUIEntryForm`, `visibleFields`, `visible`, `switchTab`, `updateKey`, and `uiEntryForm.entry`; `ssh_hosts.go`'s `configuredSSHHosts`; and `config.go`'s `OpenStore`, `Save`, and `atomicJSON`.

## How to get to it (user POV)

Choose SSH or Serve with Left/Right or 1/2, then press a. The active tab determines the new entry's kind; kind is not an editable field.

SSH fields are Name, SSH host, Target host, Target port, and Local port. Serve fields are Name, SSH host, Target port, and HTTPS port. Blank Serve SSH host selects this machine; its localhost target is not editable in the form.

Use Up/Down, Tab/Shift+Tab, or Enter to move fields, Ctrl+U to clear, and Ctrl+S to save. j/k are ordinary text in forms. Ctrl+P opens configured SSH hosts; use arrows or j/k and Enter to choose, or Escape to cancel. Press e to edit a stopped route, / to filter the active tab, and p to inspect the actual config.json path, including a --config-dir override.

## Driving it with the PTY helpers

Run the safe keyboard smoke from [the skill](../SKILL.md#safe-keyboard-smoke-linux-ci). Require only the appropriate rows in each tab and only the appropriate fields in each form. Verify field navigation, literal j/k, cancellation without a saved-file change, the help hint, and the actual config path.

Run [the extended helper](../SKILL.md#launch) for persistence and host selection. It submits an empty form, reads its error with Ctrl+D, and cancels. With a scratch HOME and included Host definitions, it opens Ctrl+P, chooses lab, and saves local-q-ssh. It reopens the picker while editing the name to local-ssh, confirms the current alias is selected, searches for a missing name and the saved name, and reopens the same config. The SSH journey starts the alias against a real throwaway loopback sshd.

Require `Saved local-ssh`, the matching SSH-tab row after reopen, and matching config.json values with mode 0600. A second process using the same directory must exit with `another temo process`. Editing an active route must be refused. The Serve journeys create entries from the Serve tab without an SSH-only local-port or target-host field.

## Gotchas

The host list follows Include files and skips wildcard patterns and Match blocks. A missing or unreadable config gives an empty list. The picker reads names and never changes SSH config. Manual typing and blank-host local Serve remain available.

Editing requires a confirmed stopped state before opening and saving. A pending check, a status error, an active route, or retained ownership needing cleanup must not bypass this guard. Refresh is read-only and leaves stale ownership records intact; explicit stop releases those records before editing. Definitions remain saved after stop.

The scratch config and generated alias are test fixtures, not normal user definitions. The updated loopback recipe passed on Linux for this branch. Source coverage and runtime coverage stay separate, and that run did not use a native macOS terminal.
