# Feature map

Run [verify.py](../verify.py) using the [verification skill](../SKILL.md). Read the source entry points in each feature file when behavior changes.

- [Saved entries](saved-entries.md) covers add, SSH host selection, validation, edit, persistence, search, and one-writer locking.
- [SSH forwarding](ssh-forwarding.md) covers real loopback forwarding, ownership, status, URL dispatch, stop, and quit/reopen.
- [Tailscale Serve](tailscale-serve.md) covers local and SSH execution, lifecycle checks, conflicts, and uncertain outcomes against a stub.
- [Terminal navigation](terminal-navigation.md) covers keyboard modes, details, help, resize, and waiting for an operation.

The Linux PTY recipe has been executed. Real SSH and HTTP reachability are covered. Tailscale remains stubbed, and the URL opener logs rather than launches a browser. These are integration gaps, not live-tailnet or graphical-browser proof.
