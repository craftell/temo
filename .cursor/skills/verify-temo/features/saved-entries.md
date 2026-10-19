# Saved entries

## Sub-features

Add and edit named SSH or Serve connections. Choose an SSH Host name from the config or type one. Validate forms, persist definitions, filter the list, and exclude a second writer.

Source entry points are `ui.go`'s `saveEditor`, `newUIEntryForm`, `visible`, `updateKey`, and `uiEntryForm.entry`, `ssh_hosts.go`'s `configuredSSHHosts`, and `config.go`'s `OpenStore`, `Save`, and `atomicJSON`.

## How to get to it (user POV)

Press `a` from the list. Use Tab to change fields, Ctrl+U to clear a field, and Ctrl+S to save. Ctrl+P opens SSH hosts. Use arrows and Enter to choose, or Escape to return without changing the host. Press `e` to edit a stopped route. Press `/` to search by name, kind, or host.

## Driving it with tmux

Run the helper from [Launch](../SKILL.md#launch). It submits an empty form, reads its error with Ctrl+D, and cancels. With a scratch HOME and included Host definitions, it opens Ctrl+P, chooses `lab`, and saves `local-q-ssh`. It reopens the picker while editing the name to `local-ssh`, confirms the current alias is selected, searches for a missing name and the saved name, and reopens the same config. The SSH journey starts the chosen alias against a real throwaway loopback sshd.

Require `Saved local-ssh`, the matching row after reopen, and matching generated `config.json` values with mode 0600. A second process using the same directory must exit with `another temo process`. The helper also tries editing the running SSH route and requires refusal.

## Gotchas

The host list follows Include files and skips wildcard patterns and Match blocks. A missing or unreadable config gives an empty list. The list reads only names and never changes SSH config. Manual typing and blank-host local Serve remain available.

A backend status error must block editing. The editor checks status both before opening and before saving an existing entry. A ready route is not editable. The helper's scratch state is not a user's configuration, and its generated SSH alias must not be copied into normal definitions.
