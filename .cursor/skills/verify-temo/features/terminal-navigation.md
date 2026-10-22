# Terminal navigation

## Sub-features

Switch SSH/Serve tabs, select routes and configured SSH hosts, search, edit text, inspect the actual config path, view full errors, scroll details, show help, and resize. Startup and r inspect all saved entries asynchronously without blocking navigation or quit.

Source entry points are `ui.go`'s `updateKey`, `startRefresh`, `fillRefresh`, `switchTab`, `View`, `detailLines`, `detailText`, and `uiTextField.edit`; `ui_modal.go`'s `modalLines`, `modalSize`, and `uiOverlayModalSize`; and `ui_delete.go`'s `updateDeleteKey`, `confirmDelete`, and `deleteLines`.

## How to get to it (user POV)

Launch temo in a terminal. Use Up/Down or j/k to select a route. Use Left/Right or 1/2 for the visible SSH and Serve tabs. Press / to search within the active tab; Up/Down returns to list selection, Enter keeps the filter, and Escape clears it. Press ? for help, p for the actual config.json path, and d or Enter for route details. Scroll details with arrows, j/k, PageUp, or PageDown. Escape returns.

In a form, Up/Down, Tab/Shift+Tab, and Enter move between relevant fields; j/k enter text. Left/Right moves the text cursor. Ctrl+P opens the host picker, where arrows or j/k select.

## Driving it with the PTY helpers

Run the safe keyboard smoke from [the skill](../SKILL.md#safe-keyboard-smoke-linux-ci). It sends actual arrow and Shift+Tab escape sequences to the built app, checks selected rows and form focus in current-screen captures, verifies literal j/k text, cancels both kinds of form, and checks p from the list and help. It starts with four saved definitions and verifies that status probes include hidden-tab entries.

The status fixture holds Serve reads open. Navigate while `Refreshing N/T (all tabs)` is visible. Start, stop, and edit on a pending entry must remain guarded. After releasing the fixture, require stopped status for both tabs and unchanged config/ownership bytes. Start another refresh, quit while its child is blocked, and require both the app and child to exit before cleanup. Completed entries can be acted on while other entries are still being inspected.

The extended helper's tabs-and-refresh journey slows the Serve stub and counts its status calls. Three `r` presses must yield one call per Serve entry, with exactly four in flight at the peak, and quit during a sweep must kill the check processes. Its stale-ownership journeys require `stopped; cleanup needed` after refresh with the journal unchanged, and Stop must release it.

Run [the extended helper](../SKILL.md#launch) for host selection, validation and backend errors, help, and resize coverage: 60 by 20, the too-small 40 by 10 warning, and the restored 100 by 32 view. A name containing q must save without quitting.

Display its long diagnostic in a 50 by 14 terminal. PageDown must change the details range from `Details 1-`; PageUp must restore the first range. An error that fits without scrolling does not prove scrolling.

## Gotchas

Send Escape key events separately with time for the terminal parser to resolve each event. Consecutive Escapes in one burst can become an Alt sequence. A process check alone does not prove rendering; old raw terminal text is not the current screen.

Read-only refresh is cancellable. An actual start, stop, open, or save operation retains the wait-before-quit guard. Do not confuse the two modes. After changing refresh code, rerun both helpers and inspect their evidence.

## Route table and port defaults

Inspect the table at 120, 80, and 50 columns. Status and listening/target port numbers must stay visible; selected rows remain identifiable without color. Long names and Japanese names must not shift subsequent columns. Full route endpoints and diagnostics remain available with d or Enter. Check empty filters, the first and last rows in a long list, and resizing while the selected row is near the end.

Compare the normal color view with NO_COLOR=1 and TERM=dumb. Header, active tab, selected row, and status should be visually distinguishable. Status text must not imply application health. A stale owned backend must retain the cleanup-needed warning.

In a new SSH form, confirm Target port and Local port begin at 3000. Clear Target port and type or paste another valid port; Local port follows. Move through Local port with cursor keys without editing, then change Target port again; follow remains enabled. Edit Local port, then change Target port; the local override remains. Cancel and reopen the form; defaults reset. Edit an existing stopped route with different ports and confirm neither port changes without an explicit field edit. Serve HTTPS remains independent.

The safe PTY helper exercises terminal input with denying stubs. Go rendering tests cover width and Unicode layout. A successful test does not establish native macOS terminal appearance or real Tailscale reachability; record those separately when inspected.

## Modal interactions and deletion

Run `python3 .cursor/skills/verify-temo/verify-modal.py --evidence "$EVIDENCE"` in a new evidence directory. The helper uses the shared PTY screen decoder and disposable definitions. Its backend commands are read-only denying stubs.

Check that the list stays behind add, edit, details, help, config location, and host-picker modals. Open config location from help and press Esc to return to help. Open the host picker from an unsaved form and press Esc to return without losing input. At 50 columns and 14 rows, move through every field and verify that focus and cancel remain visible. Resize below the minimum and back while editing.

Press `D` on a stopped entry. Require the visible `[ Cancel ]` selection. Enter, Esc, and n must leave config and ownership hashes unchanged and issue no backend command. List-movement keys and pasted y must not change the frozen target or confirm. Confirm deletion with `y` or Tab then Enter. The active filter stays in place even if no row matches afterward. Compare every surviving definition field, not just names, and retain unrelated journals byte for byte. No deletion path may invoke Stop.

The modal helper adds ownership to its stopped fixture after the dialog opens. Confirmation must perform a fresh read-only check and refuse deletion. It also selects Delete, resizes below 50 by 14, tries Enter and y, and requires unchanged files. After restoring 50 by 14, Cancel and the action footer must be visible.

Follow the [failed-save recipe](saved-entries.md#deletion-and-failed-save-recovery) for unreadable outcomes and r recovery. Those checks use disposable files only. A filesystem fault after rename remains covered by the Go regression's simulated error, not by this PTY fixture.

Go tests cover fresh unsafe states, failed and uncertain saves, empty lists, queued refreshes, stale completions, and Unicode dimensions. PTY captures prove rendered Linux input behavior. Neither establishes native macOS appearance or real backend reachability.
