# Lifecycle decisions

temo stores version-1 connection definitions separately from per-entry ownership records. The TUI sends asynchronous commands to one lifecycle service. The service returns a state, URL, and explanation. Backend setup and application health remain separate.

Two designs were considered. A desired-state reconciler would continually restart missing backends. A command-oriented service changes backends only when the user acts. The command-oriented design avoids unexpected restarts and does not need a daemon.

The data shape follows Model the Domain. Saved entries describe desired connection settings. Ownership records describe resources that temo attempted to create. A matching port alone never grants ownership. Pending Serve records remain uncertain after failure.

Prove It Works separates evidence levels. Source review, mocked tests, CI builds, terminal interaction, and live connection tests are different checks. The initial implementation was authored through the GitHub connector. No local CLI recording or live tunnel was executed during authoring.

## Work sequence

1. Blocking checks established current Bubble Tea v2 APIs, OpenSSH forwarding behavior, and Tailscale status shape.
2. Independent work separated the TUI and connection lifecycle design. Implementation writes were serialized on one branch.
3. Shared state is limited to the private config directory and the external Tailscale configuration. An OS file lock excludes another temo writer.
4. One engine owns backend mutations. The TUI never starts competing operations.

The smallest safe implementation has no auto-reconnect, deletion, arbitrary command field, credentials, Funnel, or global reset. Remote Serve runs only fixed tailscale commands with quoted arguments over SSH. There is no arbitrary remote shell text field.

## Known limits

The CLI-based Serve read/change/read sequence can race with another writer. Exact-state comparison detects observed drift, not all concurrent changes or identical replacement. A future LocalAPI adapter should preserve unknown JSON fields and use ETag/If-Match.

SSH control checks prove a master is alive. ExitOnForwardFailure proves forwarding setup. Neither proves application health. SSH config may contain trusted ProxyCommand or Match exec directives; temo honors the user's SSH configuration.

A stopped backend must be confirmed before changing a definition. Missing dependencies and unreadable backend state are errors, not a stopped result.

Persistent backends remain after TUI quit. This reversible default avoids interrupting development work. Use explicit stop before removing the config directory.

Remote Serve requires a stable single-node SSH alias. Each fixed command uses a separate SSH connection. Alias changes, load balancing, and Tailscale profile switches during an operation are unsupported. Node identity checks are not atomic across those external changes. Session-pinned transport is future work.
