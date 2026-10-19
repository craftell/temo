# SSH forwarding

## Sub-features

Start a loopback forward, inspect status, dispatch its URL to the opener, and stop only its dedicated ControlMaster. Leave the forward running when the TUI quits.

Source entry points are `engine.go`'s `ssh`, `sshOptions`, `ownership`, and `Do`, plus `ui.go`'s `quit` and `runEntry`.

## How to get to it (user POV)

Create an `ssh` entry with a working noninteractive alias, an unused local port, and a target host and port. Select the entry. Press `s` to start, `r` to refresh, `o` to open, and `x` to stop.

## Driving it with tmux

Run the helper from [Launch](../SKILL.md#launch). It creates a high-port sshd bound to 127.0.0.1, a scratch SSH config, a throwaway identity, and a separate known_hosts file. The real client never reads `~/.ssh/config`.

Require setup-ready in the terminal, an active mode-0600 ownership journal, and the exact HTTP response `temo loopback verification` through the forwarded port. A connection to the same port on 127.0.0.2 must fail, even though the test alias sets `GatewayPorts yes`.

After `q`, require that HTTP still responds. Reopen and refresh the route, stop it, and require that the forwarded listener disappears. An unrelated scratch ControlMaster must still answer `-O check`. Starting an alias with inherited forwarding rules must fail before a master is created.

The opener log must contain the selected loopback HTTP URL. This checks URL dispatch, not a graphical browser.

## Gotchas

Ready describes routing setup, not application health. The helper verifies both independently. All SSH traffic stays on this machine. SSH authentication or host-key failures are test failures, not reasons to weaken temo's production checks. The scratch sshd configuration is test-only.
