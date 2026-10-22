#!/usr/bin/env python3
"""Exercise modal focus and definition deletion in a disposable Linux PTY."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

spec = importlib.util.spec_from_file_location("keyboard", Path(__file__).with_name("verify-keyboard.py"))
keyboard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(keyboard)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", required=True, type=Path)
    parser.add_argument("--binary", type=Path)
    args = parser.parse_args()
    assert sys.platform.startswith("linux"), "Linux PTY required"
    out = args.evidence.resolve()
    out.mkdir(parents=True, exist_ok=True)
    assert not any(out.iterdir()), "use an empty evidence directory"
    root = Path(tempfile.mkdtemp(prefix="temo-modal-"))
    for name in ("home", "cfg", "bin"):
        (root / name).mkdir(mode=0o700)
    binary = args.binary.resolve() if args.binary else root / "temo"
    if not args.binary:
        subprocess.run(["go", "build", "-o", str(binary), "."],
                       cwd=Path(__file__).resolve().parents[3], check=True)
    for name in ("ssh", "tailscale", "open", "xdg-open"):
        path = root / "bin" / name
        path.write_text("#!" + sys.executable + "\n" + keyboard.STUB)
        path.chmod(0o700)
    ssh = root / "home" / ".ssh"
    ssh.mkdir(mode=0o700)
    (ssh / "config").write_text("Host first-alias second-alias\n HostName example.invalid\n")
    entries = [{"id": f"{i:016x}", "name": name, "kind": "ssh", "ssh_host": "smoke-a",
                "target_host": "127.0.0.1", "target_port": 3000, "local_port": 8100 + i}
               for i, name in enumerate(("delete-alpha", "delete-bravo", "keep-owned"), 1)]
    cfg = root / "cfg" / "config.json"
    cfg.write_text(json.dumps({"version": 1, "entries": entries}) + "\n")
    cfg.chmod(0o600)
    owner = root / "cfg" / ("owner-" + entries[2]["id"] + ".json")
    owner.write_text(json.dumps({"version": 1, "entry": entries[2], "phase": "active"}) + "\n")
    owner.chmod(0o600)
    before = keyboard.snapshot(cfg.parent)
    (out / "before.json").write_text(json.dumps(before, indent=2))
    ui = None
    try:
        (out / "environment.json").write_text(json.dumps({"binary": str(binary), "fixture": str(root),
            "commit": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
            "GOFLAGS": os.environ.get("GOFLAGS", ""),
            "backend": "denying read-only SSH stub; no network or real user's config"}, indent=2))
        ui = keyboard.Terminal(binary, root, out)
        def move(key, predicate, label):
            ui.send(key)
            return ui.expect(predicate, label)
        def dialog(s):
            return "Delete definition" in s
        def no_dialog(s):
            return "Delete definition" not in s
        def focus(label):
            return lambda s: bool(re.search(r"(?:^|│) *>\s+" + re.escape(label) + r"\s", s, re.M))
        ui.expect(lambda s: "delete-alpha" in s and "stopped" in s and "Refreshing " not in s, "launch")
        cancelled_calls = keyboard.calls(out)
        for key, label in ((b"\r", "enter"), (b"\x1b", "escape"), (b"n", "n")):
            screen = move(b"D", dialog, "delete-default-cancel-" + label)
            assert "[ Cancel ]" in screen and "Name: delete-alpha" in screen
            move(key, no_dialog, label + "-cancels")
            assert keyboard.snapshot(cfg.parent) == before
            assert keyboard.calls(out) == cancelled_calls, "cancellation called a backend"
        move(b"D", dialog, "frozen-target")
        screen = move(b"j\x1b[B\x1b[200~y\x1b[201~", dialog, "dialog-navigation-and-paste")
        assert "Name: delete-alpha" in screen and "[ Cancel ]" in screen
        assert keyboard.snapshot(cfg.parent) == before
        assert keyboard.calls(out) == cancelled_calls
        changed_owner = cfg.parent / ("owner-" + entries[0]["id"] + ".json")
        changed_owner.write_text(json.dumps({"version": 1, "entry": entries[0], "phase": "active"}) + "\n")
        changed_owner.chmod(0o600)
        unsafe = keyboard.snapshot(cfg.parent)
        try:
            move(b"y", lambda s: dialog(s) and "deletion requires" in s and "Working:" not in s,
                 "fresh-ownership-refused")
            assert keyboard.snapshot(cfg.parent) == unsafe
            checked = [call for call in keyboard.calls(out)[len(cancelled_calls):] if call["phase"] == "call"]
            assert len(checked) == 1 and checked[0]["allowed"] and entries[0]["id"] + ".sock" in checked[0]["args"][3]
        finally:
            changed_owner.unlink()
        move(b"\x1b", no_dialog, "fresh-refusal-return")
        move(b"D", dialog, "delete-before-resize")
        move(b"\t", lambda s: "[ Delete ]" in s, "delete-selected-before-resize")
        ui.resize(10, 40)
        ui.expect(lambda s: "Resize to at least" in s, "hidden-delete")
        ui.send(b"\ry")
        assert keyboard.snapshot(cfg.parent) == before, "hidden confirmation changed disk"
        ui.resize(14, 50)
        screen = ui.expect(lambda s: dialog(s) and "[ Cancel ]" in s, "delete-minimum-restored")
        assert "Enter/y" in screen and "Esc/n cancel" in screen
        move(b"\r", no_dialog, "restored-enter-cancels")
        ui.resize(32, 120)
        ui.expect(lambda s: "delete-alpha" in s and no_dialog(s), "list-restored")
        move(b"D", dialog, "save-failure-confirmation")
        backup = root / "config-before-failure.json"
        cfg.rename(backup)
        cfg.mkdir()
        try:
            move(b"y", lambda s: dialog(s) and "configuration reload failed" in s and "Working:" not in s,
                 "unreadable-save-outcome")
            move(b"\x1b", no_dialog, "save-failure-return")
            blocked_calls = keyboard.calls(out)
            for key in (b"a", b"e", b"s", b"x", b"o", b"D"):
                screen = move(key, lambda s: "Configuration state is uncertain" in s,
                              "unknown-outcome-blocks-" + key.decode())
                assert no_dialog(screen) and "Edit route" not in screen and "Add route" not in screen
            assert keyboard.calls(out) == blocked_calls, "unknown outcome called a backend"
            move(b"r", lambda s: "Configuration reload failed" in s and "Working:" not in s,
                 "reload-still-unreadable")
        finally:
            cfg.rmdir()
            backup.rename(cfg)
        assert keyboard.snapshot(cfg.parent) == before
        move(b"r", lambda s: "Configuration reloaded. Mutations are available again." in s, "reload-recovered")
        move(b"a", focus("Name"), "add-modal")
        move(b"unsaved", focus("Name"), "unsaved-form")
        move(b"\x10", lambda s: "SSH hosts" in s and "first-alias" in s, "host-picker-modal")
        screen = move(b"\x1b", focus("Name"), "picker-return")
        assert "unsaved" in screen
        ui.resize(14, 50)
        ui.expect(focus("Name"), "small-form")
        for label in ("SSH host", "Target host", "Target port", "Local port"):
            move(b"\t", focus(label), "small-focus-" + label.replace(" ", "-"))
        ui.resize(10, 40)
        ui.expect(lambda s: "Resize to at least" in s, "too-small-form")
        ui.resize(32, 120)
        ui.expect(lambda s: "unsaved" in s and focus("Local port")(s), "restore-form")
        move(b"\x1b", lambda s: "Edit cancelled" in s, "cancel-form")
        assert keyboard.snapshot(cfg.parent) == before
        move(b"?", lambda s: "temo help" in s, "help-modal")
        move(b"p", lambda s: "Configuration file" in s, "config-modal")
        move(b"\x1b", lambda s: "temo help" in s, "config-return-help")
        move(b"\x1b", lambda s: "temo help" not in s, "help-return-list")
        move(b"/keep-owned\r", lambda s: "Filter: keep-owned" in s, "owned-filter")
        move(b"D", dialog, "owned-delete-confirmation")
        move(b"y", lambda s: dialog(s) and "Error:" in s and "deletion requires" in s and "Working:" not in s, "owned-delete-refused")
        assert keyboard.snapshot(cfg.parent) == before
        move(b"\x1b", no_dialog, "refusal-return")
        ui.send(b"\x1b")
        move(b"/delete-alpha\r", lambda s: "Filter: delete-alpha" in s, "delete-filter")
        move(b"D", dialog, "filtered-delete-confirmation")
        screen = move(b"y", lambda s: "Deleted definition delete-alpha" in s and no_dialog(s), "filtered-delete-saved")
        assert "Filter: delete-alpha" in screen and "No matching routes" in screen
        assert json.loads(cfg.read_text()) == {"version": 1, "entries": entries[1:]}
        assert keyboard.snapshot(cfg.parent)[owner.name] == before[owner.name]
        ui.send(b"\x1b")
        move(b"/delete-bravo\r", lambda s: "Filter: delete-bravo" in s, "last-matching-filter")
        move(b"D", dialog, "last-matching-confirm")
        move(b"\t\r", lambda s: "Deleted definition delete-bravo" in s, "button-delete-saved")
        assert json.loads(cfg.read_text()) == {"version": 1, "entries": entries[2:]}
        assert keyboard.snapshot(cfg.parent)[owner.name] == before[owner.name]
        assert cfg.stat().st_mode & 0o777 == 0o600
        ui.quit()
        assert all(call["allowed"] for call in keyboard.calls(out)), "mutation backend command attempted"
    except Exception as error:
        (out / "result.txt").write_text("FAIL: " + repr(error) + "\n")
        raise
    finally:
        try:
            if ui is not None:
                ui.close()
            (out / "after.json").write_text(json.dumps(keyboard.snapshot(cfg.parent), indent=2))
            for path in cfg.parent.glob("*.json"):
                (out / path.name).write_bytes(path.read_bytes())
        except Exception as error:
            (out / "result.txt").write_text("FAIL during cleanup: " + repr(error) + "\n")
            raise
        finally:
            print("Evidence:", out, "Fixture:", root, flush=True)
    (out / "result.txt").write_text("PASS: real Linux PTY modal nesting, form focus and resize, frozen target, cancellation without backend calls, fresh unsafe-status refusal, hidden-confirmation refusal, unreadable-save blocking and reload recovery, confirmed filtered deletion, exact surviving definitions and unchanged ownership. Cleanup finished. No real SSH or Tailscale routes.\n")


if __name__ == "__main__":
    main()
