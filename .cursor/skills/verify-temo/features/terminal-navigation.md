# Terminal navigation

## Sub-features

Select routes, search, edit text, view full errors, scroll details, show help, resize, and wait for a backend operation before quitting.

Source entry points are `ui.go`'s `updateKey`, `View`, `detailLines`, `detailText`, and `uiTextField.edit`.

## How to get to it (user POV)

Launch temo in a terminal. Use `j`, `k`, or arrows to select. Press `/` to search, `?` for help, and `d` or Enter for details. Scroll details with arrows, `j`, `k`, PageUp, or PageDown. Escape returns to the prior view.

## Driving it with tmux

Run the helper from [Launch](../SKILL.md#launch). It captures list, form, search, validation error, backend error, and help screens. A name containing `q` must save without quitting. Resizes cover 60 by 20, the too-small 40 by 10 warning, and the restored 100 by 32 view.

While a delayed stub status runs, send `s` and `q`. The TUI must remain alive, show `Wait for the current operation before quitting`, and issue no start command. Let status finish before the next action.

Display the stub's long diagnostic in a 50 by 14 terminal. PageDown must change the details range from `Details 1-`; PageUp must restore the first range. A captured error that fits without scrolling does not prove scrolling.

## Gotchas

Send Escape key events separately with time for the terminal parser to resolve each event. Consecutive Escapes in a single input burst can become an Alt sequence. A process check alone does not prove that the terminal rendered. Unit tests remain useful for individual key mappings that the runtime journey does not enumerate.
