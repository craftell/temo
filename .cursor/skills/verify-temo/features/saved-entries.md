# Saved entries

## Sub-features

Add, edit, and safely delete named SSH or Serve definitions in separate tabs. Choose an SSH Host name or type one. Validate forms, persist definitions, recover from failed saves, filter within a tab, discover the actual config path, and exclude a second writer.

Source entry points are `ui.go`'s `saveEditor`, `newUIEntryForm`, `visibleFields`, `visible`, `switchTab`, `updateKey`, and `uiEntryForm.entry`; `ui_delete.go`'s `openDelete`, `confirmDelete`, `uiPersistConfig`, `adoptReload`, and `reloadConfig`; `ssh_hosts.go`'s `configuredSSHHosts`; and `config.go`'s `OpenStore`, `Load`, `Save`, and `atomicJSON`.

## How to get to it (user POV)

Choose SSH or Serve with Left/Right or 1/2, then press a. The active tab determines the new entry's kind; kind is not an editable field.

SSH fields are Name, SSH host, Target host, Target port, and Local port. Serve fields are Name, SSH host, Target port, and HTTPS port. Blank Serve SSH host selects this machine; its localhost target is not editable in the form.

Use Up/Down, Tab/Shift+Tab, or Enter to move fields, Ctrl+U to clear, and Ctrl+S to save. j/k are ordinary text in forms. Ctrl+P opens configured SSH hosts; use arrows or j/k and Enter to choose, or Escape to cancel. Press e to edit a stopped route, / to filter the active tab, and p to inspect the actual config.json path, including a --config-dir override.

## Driving it with the PTY helpers

Run the safe keyboard smoke from [the skill](../SKILL.md#safe-keyboard-smoke-linux-ci). Require only the appropriate rows in each tab and only the appropriate fields in each form. Verify field navigation, literal j/k, cancellation without a saved-file change, the help hint, and the actual config path.

Run [the extended helper](../SKILL.md#launch) for persistence and host selection. It submits an empty form, reads its error with Ctrl+D, and cancels. With a scratch HOME and included Host definitions, it opens Ctrl+P, chooses lab, and saves local-q-ssh. It reopens the picker while editing the name to local-ssh, confirms the current alias is selected, searches for a missing name and the saved name, and reopens the same config. The SSH journey starts the alias against a real throwaway loopback sshd.

Require `Saved local-ssh`, the matching SSH-tab row after reopen, and matching config.json values with mode 0600. A second process using the same directory must exit with `another temo process`. Editing an active route must be refused. The Serve journeys create entries from the Serve tab without an SSH-only local-port or target-host field.

## Deletion and failed-save recovery

Press D on the selected route. The dialog freezes that definition and selects Cancel. Enter on Cancel, Esc, or n leaves it unchanged. Confirm with y, or Tab then Enter. Confirmation checks status again and deletes only a confirmed stopped definition. Retained ownership, active routes, pending checks, and uncertain status must refuse deletion. Deletion never invokes Stop or removes an ownership journal.

Saving and deleting preserve the active filter, even when it hides the changed entry. Clear the filter or select the desired entry before the next backend action. Do not assume Add selects a row that the filter excludes.

After a failed write, the UI reloads config.json before accepting more changes. If the reload fails, cancel the form or dialog and repair the config file, then press r. Changes remain blocked while reload fails. If the write reached disk before its error, inspect the loaded result rather than retrying the stale form.

Run [the modal helper](../SKILL.md#modal-and-deletion-changes). Require unchanged hashes and no backend calls for cancellation, refusal when ownership appears after the dialog opens, and unchanged surviving entry fields and journals after confirmed deletion. Its disposable unreadable-config fixture checks mutation blocking, a failed r, and successful recovery after restoring the file. The Go suite covers failed writes both before and after a committed rename, including stale form retries.

## Gotchas

The host list follows Include files and skips wildcard patterns and Match blocks. A missing or unreadable config gives an empty list. The picker reads names and never changes SSH config. Manual typing and blank-host local Serve remain available.

Editing requires a confirmed stopped state before opening and saving. For an unowned definition this is determined locally, without requiring SSH/Tailscale availability or a free endpoint. For an owned definition, a pending check, a status error, an active route, or retained ownership needing cleanup must not bypass this guard. Refresh is read-only and leaves stale ownership records intact; explicit stop releases those records before editing. Definitions remain saved after stop.

The scratch config and generated alias are test fixtures, not normal user definitions. The updated loopback recipe passed on Linux for this branch. Source coverage and runtime coverage stay separate, and that run did not use a native macOS terminal.

## Alternative definitions (0.6.0)

Run `python3 .cursor/skills/verify-temo/verify-definitions.py --binary ./temo --evidence /path/to/new-empty-evidence` on Linux. This real PTY recipe uses an empty backend PATH and disposable config. It loads equal names and HTTPS ports, edits the second row by ID offline, adds another same-name/same-port definition, deletes only the selected ID, and reopens the file. No backend or credentials are needed.

The Go regression suite additionally covers SSH and Serve duplicate roundtrips, rejection of duplicate IDs, untouched active ownership on a competing start, and Serve start-after-stop. Names are display labels, not identifiers. `stopped` does not promise a free port; Start must reject real collisions without takeover. Owned/pending/stale ownership guards remain in effect.
