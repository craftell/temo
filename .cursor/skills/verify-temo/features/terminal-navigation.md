# Terminal navigation

## Sub-features

Switch SSH/Serve tabs, select routes and configured SSH hosts, search, edit text, inspect the actual config path, view full errors, scroll details, show help, and resize. Startup and r inspect all saved entries asynchronously without blocking navigation or quit.

Source entry points are `ui.go`'s `updateKey`, `startRefresh`, `fillRefresh`, `switchTab`, `View`, `detailLines`, `detailText`, and `uiTextField.edit`.

## How to get to it (user POV)

Launch temo in a terminal. Use Up/Down or j/k to select a route. Use Left/Right or 1/2 for the visible SSH and Serve tabs. Press / to search within the active tab; Up/Down returns to list selection, Enter keeps the filter, and Escape clears it. Press ? for help, p for the actual config.json path, and d or Enter for route details. Scroll details with arrows, j/k, PageUp, or PageDown. Escape returns.

In a form, Up/Down, Tab/Shift+Tab, and Enter move between relevant fields; j/k enter text. Left/Right moves the text cursor. Ctrl+P opens the host picker, where arrows or j/k select.

## Driving it with the PTY helpers

Run the safe keyboard smoke from [the skill](../SKILL.md#safe-keyboard-smoke-linux-ci). It sends actual arrow and Shift+Tab escape sequences to the built app, checks selected rows and form focus in current-screen captures, verifies literal j/k text, cancels both kinds of form, and checks p from the list and help. It starts with four saved definitions and verifies that status probes include hidden-tab entries.

The status fixture holds Serve reads open. Navigate while `Refreshing N/T (all tabs)` is visible. Start, stop, and edit on a pending entry must remain guarded. After releasing the fixture, require stopped status for both tabs and unchanged config/ownership bytes. Start another refresh, quit while its child is blocked, and require both the app and child to exit before cleanup. Completed entries can be acted on while other entries are still being inspected.

Run [the extended helper](../SKILL.md#launch) for host selection, validation and backend errors, help, and resize coverage: 60 by 20, the too-small 40 by 10 warning, and the restored 100 by 32 view. A name containing q must save without quitting.

Display its long diagnostic in a 50 by 14 terminal. PageDown must change the details range from `Details 1-`; PageUp must restore the first range. An error that fits without scrolling does not prove scrolling.

## Gotchas

Send Escape key events separately with time for the terminal parser to resolve each event. Consecutive Escapes in one burst can become an Alt sequence. A process check alone does not prove rendering; old raw terminal text is not the current screen.

Read-only refresh is cancellable. An actual start, stop, open, or save operation retains the wait-before-quit guard. Do not confuse the two modes. Run the changed helpers and inspect their result and evidence before treating this source-aligned recipe as newly verified.
