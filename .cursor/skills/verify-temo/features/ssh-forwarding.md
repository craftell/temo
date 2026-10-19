# SSH forwarding

## Sub-features

Create a loopback forward, inspect state, open its URL, and stop the owned master.

## How to get to it (user POV)

Create an ssh entry with an authorized test alias and unused local port.

## Driving it with tmux

Start with s. Capture ready state. Verify the dedicated socket and HTTP response separately. Quit and reopen. Stop with x. Confirm an unrelated SSH session stays alive.

## Gotchas

Requires a known host and noninteractive authentication. Inherited forwarding rules must be rejected. Ready does not prove application health.

Live status is UNEXECUTED.
