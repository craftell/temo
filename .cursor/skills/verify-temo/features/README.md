# Feature map

Use the [verification skill](../SKILL.md) for the Linux-only [safe keyboard smoke](../verify-keyboard.py), the extended [loopback journey](../verify.py), and release checks. Read the source entry points in each feature file when behavior changes.

- [Saved entries](saved-entries.md) covers SSH/Serve tabs, kind-specific forms, host selection, validation, edit, persistence, filtered search, actual config location, and one-writer locking.
- [SSH forwarding](ssh-forwarding.md) covers real loopback forwarding, ownership, status, URL dispatch, stop, and quit/reopen.
- [Tailscale Serve](tailscale-serve.md) covers local and SSH execution, lifecycle checks, conflicts, and uncertain outcomes against a stub.
- [Terminal navigation](terminal-navigation.md) covers real arrow keys, mode-specific j/k, tabs and fields, details, help, resize, asynchronous read-only refresh, and quit cancellation.
- [Self-update](self-update.md) covers stable release checks, archive validation, atomic replacement, provenance, and isolated failure fixtures.
- [Release and installation](release-and-install.md) covers version flags, local snapshots, archive checksums, and safe installer journeys.

The extended loopback journey and the Linux keyboard smoke both passed on Linux for this PR branch (base `8a17233` plus the start-failure wording fix), with `GOFLAGS=-buildvcs=false` set for the local build. The extended run now covers stale SSH and Serve ownership, a failed SSH start, the in-flight cap, and quit during refresh. The local snapshot installer recipe was executed earlier and was not rerun. Tailscale remains stubbed, and the URL opener logs rather than launches a browser. Not run: native macOS terminal, real Tailscale Serve or Funnel, tailnet HTTPS, and a graphical browser.
