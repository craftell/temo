# Saved entries

## Sub-features

Add and edit named SSH or Serve connections. Validate forms, persist definitions, filter the list, and exclude a second writer.

Source entry points are `ui.go`'s `saveEditor`, `newUIEntryForm`, `visible`, and `uiEntryForm.entry`, plus `config.go`'s `OpenStore`, `Save`, and `atomicJSON`.

## How to get to it (user POV)

Press `a` from the list. Use Tab to change fields, Ctrl+U to clear a field, and Ctrl+S to save. Press `e` to edit a stopped route. Press `/` to search by name, kind, or host.

## Driving it with tmux

Run the helper from [Launch](../SKILL.md#launch). It submits an empty form, reads its error with Ctrl+D, and cancels. It then adds `local-q-ssh`, edits the name to `local-ssh`, searches for a missing name and the saved name, and reopens the same config.

Require `Saved local-ssh`, the matching row after reopen, and matching generated `config.json` values with mode 0600. A second process using the same directory must exit with `another temo process`. The helper also tries editing the running SSH route and requires refusal.

## Gotchas

A backend status error must block editing. The editor checks status both before opening and before saving an existing entry. A ready route is not editable. The helper's scratch state is not a user's configuration, and its generated SSH alias must not be copied into normal definitions.
