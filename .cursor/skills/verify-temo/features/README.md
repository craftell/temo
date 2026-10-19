# Feature map

Use the [verification skill](../SKILL.md) for the Linux-only [safe keyboard smoke](../verify-keyboard.py), the extended [loopback journey](../verify.py), and release checks. Read the source entry points in each feature file when behavior changes.

- [Saved entries](saved-entries.md) covers SSH/Serve tabs, kind-specific forms, host selection, validation, edit, persistence, filtered search, actual config location, and one-writer locking.
- [SSH forwarding](ssh-forwarding.md) covers real loopback forwarding, ownership, status, URL dispatch, stop, and quit/reopen.
- [Tailscale Serve](tailscale-serve.md) covers local and SSH execution, lifecycle checks, conflicts, and uncertain outcomes against a stub.
- [Terminal navigation](terminal-navigation.md) covers real arrow keys, mode-specific j/k, tabs and fields, details, help, resize, asynchronous read-only refresh, and quit cancellation.
- [Release and installation](release-and-install.md) covers version flags, local snapshots, archive checksums, and safe installer journeys.

The earlier Linux loopback and local snapshot installer recipes were executed. The Linux keyboard smoke passed for `8333e4a` in [CI](https://github.com/craftell/temo/actions/runs/39228871170). The updated extended loopback journey has not been rerun. Inspect the relevant CI result and safe-smoke artifact after later changes. Tailscale remains stubbed, and the URL opener logs rather than launches a browser. These are integration gaps, not live-tailnet or graphical-browser proof.
