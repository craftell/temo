# Tailscale Serve

## Sub-features

Manage a dedicated HTTPS root route locally or through SSH. Detect changed state, occupied ports, Funnel, foreground sessions, changed node identity, and uncertain outcomes.

Source entry points are `engine.go`'s `serve`, `portSnapshot`, `desiredSnapshot`, and `tailscale`.

## How to get to it (user POV)

Select the Serve tab with 2 or Left/Right, then press a. Enter a name, target port, and dedicated HTTPS port; the localhost target is not an editable form field. Blank SSH host selects this machine. An SSH alias selects remote execution. Use s, o, and x on the selected entry. Startup and r inspect every saved entry across both tabs, without changing the Serve configuration or ownership files.

## Driving it with tmux

Run the helper from [Launch](../SKILL.md#launch). Both local and SSH Serve use its `tailscale` stub. The SSH transport itself is real and loopback-only.

Require stopped, setup-ready, refresh, URL dispatch, and stop screens. Inspect `tailscale-calls.jsonl` and `serve.json` for the dedicated port and root handler. Local Serve must survive quitting and reopening the app. The remote journey uses port 9443 and must issue its fixed Tailscale commands through SSH.

Change only the stub fixture to introduce drift, a different node, an occupied port, Funnel, a foreground session, unknown fields, or invalid JSON. Each attempt must show the matching error and issue no mutating stub command. A simulated failure after applying a route must leave pending ownership. A later stop must refuse mutation rather than adopt or roll back the route.

## Gotchas

The stub does not provide HTTPS, certificates, a real Tailscale daemon, or a tailnet connection. Real Tailscale has not been exercised by this recipe. Do not mutate a user's node to fill that gap. A future real check needs a separately approved disposable node and explicit command approval.

The production CLI read/change/read sequence is not atomic against concurrent external writers. Remote commands use separate SSH connections and require a stable single-node alias and Tailscale profile. Pending production records require manual inspection, unlike the disposable stub fixture retained by the helper.
