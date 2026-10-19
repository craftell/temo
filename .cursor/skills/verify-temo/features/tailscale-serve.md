# Tailscale Serve

## Sub-features

Manage a dedicated HTTPS root route locally or through SSH. Detect drift.

## How to get to it (user POV)

Create a serve entry with a loopback target and unused HTTPS port. Blank SSH host selects local execution.

## Driving it with tmux

Capture Serve status before start. Press s and inspect that port. Test its URL separately. On an authorized disposable node, add an external path and confirm x refuses deletion. Restore the recorded route manually and stop through temo.

## Gotchas

Requires an authorized disposable tailnet node. Concurrent external writes lack atomic isolation. Pending outcomes require manual inspection.

Live status is UNEXECUTED.
