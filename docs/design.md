# Lifecycle decisions

temo stores version-1 connection definitions separately from per-entry ownership records. The TUI sends asynchronous commands to one lifecycle service. The service returns a state, URL, and explanation. Backend setup and application health remain separate.

Two designs were considered. A desired-state reconciler would continually restart missing backends. A command-oriented service changes backends only when the user acts. The command-oriented design avoids unexpected restarts and does not need a daemon.

The data shape follows Model the Domain. Saved entries describe desired connection settings. Ownership records describe resources that temo attempted to create. A matching port alone never grants ownership. Pending Serve records remain uncertain after failure.

Prove It Works separates evidence levels. Source review, mocked tests, CI builds, terminal interaction, and live connection tests are different checks. The initial implementation was authored through the GitHub connector. No local CLI recording or live tunnel was executed during authoring.

## Work sequence

1. Blocking checks established current Bubble Tea v2 APIs, OpenSSH forwarding behavior, and Tailscale status shape.
2. Independent work separated the TUI and connection lifecycle design. Implementation writes were serialized on one branch.
3. Shared state is limited to the private config directory and the external Tailscale configuration. An OS file lock excludes another temo writer.
4. One engine owns backend mutations. Explicit operations remain serialized. Read-only observations run concurrently across entries, with at most four in flight.

The smallest safe implementation has no auto-reconnect, deletion, arbitrary command field, credentials, Funnel, or global reset. Remote Serve runs only fixed tailscale commands with quoted arguments over SSH. There is no arbitrary remote shell text field.

## Known limits

The CLI-based Serve read/change/read sequence can race with another writer. Exact-state comparison detects observed drift, not all concurrent changes or identical replacement. A future LocalAPI adapter should preserve unknown JSON fields and use ETag/If-Match.

SSH control checks prove a master is alive. ExitOnForwardFailure proves forwarding setup. Neither proves application health. The form's Ctrl+P picker reads literal Host names from `~/.ssh/config` and included files, skipping wildcard patterns and Match blocks. It does not run SSH or store other config values. Manual names and blank-host local Serve remain available. Selection does not change the engine's `ssh -G`, strict host-key, or inherited-forwarding checks. SSH config may contain trusted ProxyCommand or Match exec directives; temo honors the user's SSH configuration when running a route.

A stopped backend must be confirmed before changing a definition. Missing dependencies and unreadable backend state are errors, not a stopped result.

Persistent backends remain after TUI quit. This reversible default avoids interrupting development work. Use explicit stop before removing the config directory.

Remote Serve requires a stable single-node SSH alias. Each fixed command uses a separate SSH connection. Alias changes, load balancing, and Tailscale profile switches during an operation are unsupported. Node identity checks are not atomic across those external changes. Session-pinned transport is future work.

## Keyboard modes and status observation

The list has two explicit kind views. Each view owns its selection and filter. A form gets its immutable kind from the active view or existing definition. Its visible-field list determines both rendering and field navigation, so a hidden port cannot receive focus.

Up and Down select rows in lists and move between fields in forms. The letters j and k navigate outside text inputs and remain text inside them. The original list already handled arrow key events; the form did not, and the global busy guard swallowed navigation during operations. Regression tests cover actual Bubble Tea key codes and printable Text values. The PTY smoke exercises raw terminal arrow sequences separately.

Two observation designs were considered. A separate engine inspector would duplicate ownership and status rules. Keeping status intrinsically read-only gives startup, refresh, Open preflight, and edit checks the same safety boundary. Observe adds a parent context to that implementation so quitting can cancel subprocesses.

The UI keeps a bounded queue and per-entry observation revisions. Each result updates its row before the next queued read starts. Repeating refresh does not enqueue another sweep. Same-entry actions wait for an in-flight read. An action supersedes a queued read for that entry. Revisions reject obsolete results. A delayed startup message waits until any explicit operation or editor has finished.

An absent backend with a retained active journal is inactive, rendered as stopped with cleanup needed. It is distinct from stopped without ownership. Only an explicit operation can release ownership. Editing still requires fresh stopped results before opening and before saving. Pending outcomes stay errors, even when a pending SSH master appears absent. No observation starts, stops, adopts, deletes, or repairs a backend.
