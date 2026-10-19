---
name: verify-temo
description: Drive temo's real terminal UI, capture keyboard and backend evidence, and check the connection feature map after changes.
---

# Verify temo

Status is DRAFT, UNEXECUTED. No terminal recording or live connection evidence is included. Run the recipe before calling this skill proven.

## Launch

Use macOS or Linux with Go 1.25+, tmux, and a UTF-8 terminal. Run `go mod tidy && go build -o temo .`. Create one private temporary config directory with `mktemp -d`. Never reuse a user's live directory.

Start a uniquely named tmux session with the binary and `--config-dir` pointing to that directory. Use 100 columns and 32 rows. An empty entry list and the temo title are the usable ready state.

No backend starts from an empty configuration. Do not start a real entry without authorization for that endpoint.

## Doctor

Capture the pane with `tmux capture-pane -p -t "$SESSION"`. Confirm the title, empty-list instruction, and keyboard footer. Check the session's pane PID with `tmux display-message -p -t "$SESSION" '#{pane_pid}'`. A process alone does not prove that the UI is usable.

Repeat doctor after surprises. If the UI is wedged, stop only that session and create a fresh isolated session.

## Drive

Use `tmux send-keys -t "$SESSION"` for one action at a time. Capture the pane before and after each action. Wait for the expected visible state.

1. Press a and confirm the add form.
2. Enter a named SSH definition for a nonexistent test alias. Use local port 18080, target port 3000, and target 127.0.0.1. Tab moves fields. Ctrl+S saves.
3. Confirm the row and inspect config.json for matching values and mode 0600.
4. Press slash, type the name, and confirm filtering. Escape leaves search.
5. Open help with question mark and capture it.
6. Resize to 60 columns and 20 rows, then to 100 columns and 32 rows. Confirm the selected row and keyboard help remain usable.
7. Quit, reopen the same isolated directory, and confirm the entry remains.

Follow the feature map for authorized live SSH and Serve journeys. Do not replace live journeys with mocks.

## Evidence

Keep raw terminal captures, the commit SHA, command exit codes, and an action log in an evidence directory outside the temporary config directory. Record platform, terminal size, and whether backends are real or mocked.

Capture each action and resulting screen. Verify saved files too. For start and stop, inspect the owned socket or exact Serve port independently. Test HTTP reachability separately from ready state.

`go test -race ./...` fakes commands at the Runner boundary. It does not prove live backends or terminal interaction.

## Cleanup

Stop all backends created by the journey before quitting. If an operation is uncertain, retain its config and ownership records and report the blocker.

Quit the TUI and remove only the named tmux session if it remains. Delete scratch config only after managed backends are confirmed stopped. Preserve the evidence directory and check that its captures still exist.

## Helpers

No executable helper is shipped. Use tmux directly. This draft requires a permitted local executor and has not been run.

Maintain this map after behavior changes. Every listed feature needs source and live coverage before the maintenance pass can be called clean.
