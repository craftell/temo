#!/usr/bin/env python3
"""Check duplicate definitions and offline editing in a disposable real Linux PTY."""
import argparse
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile

spec = importlib.util.spec_from_file_location("keyboard", Path(__file__).with_name("verify-keyboard.py"))
keyboard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(keyboard)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    assert sys.platform.startswith("linux"), "Linux PTY required"
    out = args.evidence.resolve()
    out.mkdir(parents=True, exist_ok=True)
    assert not any(out.iterdir()), "use an empty evidence directory"
    root = Path(tempfile.mkdtemp(prefix="temo-definitions-"))
    for name in ("home", "cfg", "bin"):
        (root / name).mkdir(mode=0o700)
    # PATH is deliberately empty: no installed SSH/Tailscale is available.
    entries = [{"id": f"{i:016x}", "name": "same-name", "kind": "serve",
                "target_host": "127.0.0.1", "target_port": 3000 + i,
                "serve_port": 8443} for i in (1, 2)]
    cfg = root / "cfg" / "config.json"
    cfg.write_text(json.dumps({"version": 1, "entries": entries}) + "\n")
    cfg.chmod(0o600)
    (out / "environment.json").write_text(json.dumps({"fixture": str(root), "binary": str(args.binary.resolve()),
        "backend": "empty PATH; no SSH, Tailscale or network calls", "commit": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True).strip()}, indent=2))
    ui = None
    try:
        ui = keyboard.Terminal(args.binary.resolve(), root, out)
        ui.expect(lambda s: "Refreshing " not in s, "initial-refresh")
        ui.send(b"2")
        ui.expect(lambda s: s.count("same-name") >= 2 and "stopped" in s, "duplicate-definitions-loaded")
        ui.send(b"j")
        ui.expect(lambda s: "> same-name" in s, "select-second")
        ui.send(b"e")
        ui.expect(lambda s: "Edit route" in s and "3002" in s, "offline-editor")
        ui.send(b"\t\t\x154000\x13")
        ui.expect(lambda s: "Saved same-name" in s and "Edit route" not in s, "offline-save")
        saved = json.loads(cfg.read_text())["entries"]
        assert saved[0] == entries[0] and saved[1]["id"] == entries[1]["id"]
        assert saved[1]["target_port"] == 4000 and saved[1]["serve_port"] == 8443
        # Add another definition with the same display name and HTTPS port.
        ui.send(b"asame-name\x13")
        ui.expect(lambda s: "Saved same-name" in s and "Add route" not in s and "SERVE routes [3]" in s, "duplicate-definition-added")
        saved = json.loads(cfg.read_text())["entries"]
        assert len(saved) == 3 and len({e["id"] for e in saved}) == 3
        assert all(e["name"] == "same-name" and e["serve_port"] == 8443 for e in saved)
        ui.send(b"D")
        ui.expect(lambda s: "Delete definition" in s, "delete-duplicate-dialog")
        ui.send(b"y")
        ui.expect(lambda s: "Deleted definition same-name" in s, "delete-duplicate")
        assert json.loads(cfg.read_text())["entries"] == saved[:2]
        assert not list(cfg.parent.glob("owner-*.json"))
        ui.quit()
        # Reopen the duplicated config, not just the existing in-memory model.
        ui.close()
        ui = keyboard.Terminal(args.binary.resolve(), root, out)
        ui.send(b"2")
        ui.expect(lambda s: s.count("same-name") >= 2 and "stopped" in s, "duplicates-after-reopen")
        ui.quit()
    except Exception as error:
        (out / "result.txt").write_text("FAIL: " + repr(error) + "\n")
        raise
    finally:
        if ui is not None:
            ui.close()
        (out / "config.json").write_bytes(cfg.read_bytes())
    (out / "result.txt").write_text("PASS: duplicate names/HTTPS ports load, edit offline by ID, save duplicate definition, delete only selected ID and reopen. No SSH/Tailscale installed in PATH; no routes started.\n")
    print("Evidence:", out)


if __name__ == "__main__":
    main()
