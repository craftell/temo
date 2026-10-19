#!/usr/bin/env python3
"""Linux-only real-PTY keyboard smoke; all backend commands are denying stubs."""
import argparse
import codecs
import contextlib
import errno
import fcntl
import hashlib
import html
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


STUB = r'''
import json, os, shlex, sys, time
from pathlib import Path
root = Path(os.environ["TEMO_KEYBOARD_ROOT"])
log = Path(os.environ["TEMO_KEYBOARD_LOG"])
tool, args = Path(sys.argv[0]).name, sys.argv[1:]
def record(phase, allowed):
    data = {"pid": os.getpid(), "tool": tool, "args": args,
            "phase": phase, "allowed": allowed}
    fd = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        os.write(fd, (json.dumps(data) + "\n").encode())
    finally:
        os.close(fd)
allowed, operation = False, None
if tool == "ssh":
    entries = json.loads((root / "cfg" / "config.json").read_text())["entries"]
    owners = {str(root / "cfg" / (e["id"] + ".sock")): e["ssh_host"]
              for e in entries if e["kind"] == "ssh"}
    if (len(args) == 8 and args[:3] == ["-F", "/dev/null", "-S"]
            and args[4:7] == ["-O", "check", "--"]
            and owners.get(args[3]) == args[7]):
        allowed, operation = True, "ssh-check"
    else:
        values = ["BatchMode=yes", "StrictHostKeyChecking=yes", "PermitLocalCommand=no",
                  "RemoteCommand=none", "ForwardAgent=no", "ForwardX11=no",
                  "ConnectTimeout=10", "ServerAliveInterval=15", "ServerAliveCountMax=3",
                  "ControlMaster=no", "ControlPath=none", "ClearAllForwardings=yes"]
        prefix = [part for value in values for part in ("-o", value)]
        prefix += ["-T", "--", "smoke-remote"]
        if args[:-1] == prefix:
            try:
                words = shlex.split(args[-1])
            except ValueError:
                words = []
            if words == ["tailscale", "status", "--json"]:
                allowed, operation = True, "node-status"
            elif words == ["tailscale", "serve", "status", "--json"]:
                allowed, operation = True, "serve-status"
elif tool == "tailscale":
    if args == ["status", "--json"]:
        allowed, operation = True, "node-status"
    elif args == ["serve", "status", "--json"]:
        allowed, operation = True, "serve-status"
record("call", allowed)
if not allowed:
    print("DENIED: keyboard smoke permits only exact read-only status commands", file=sys.stderr)
    sys.exit(90)
if operation == "node-status":
    record("blocked", True)
    deadline = time.monotonic() + 60
    while not (root / "release-status").exists():
        if time.monotonic() > deadline:
            print("fixture gate was never released", file=sys.stderr)
            sys.exit(91)
        time.sleep(0.02)
    print(json.dumps({"BackendState": "Running", "Self": {
        "ID": "keyboard-node", "DNSName": "keyboard.invalid."}}))
elif operation == "serve-status":
    print("{}")
record("done", True)
if operation == "ssh-check":
    sys.exit(1)
'''


class Cell(str):
    def __new__(cls, text, style):
        cell = super().__new__(cls, text)
        cell.style = style
        return cell


class Screen:
    """Small VT screen model: retain current cells, never grep old output."""
    def __init__(self, rows=32, cols=120):
        self.rows, self.cols = rows, cols
        self.grid = [[" "] * cols for _ in range(rows)]
        self.row = self.col = 0
        self.saved = (0, 0)
        self.pending = ""
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
        self.replies = []
        self.style = ("#d8dee9", "#111827", False)
        self.color_events = 0

    def newline(self):
        self.row += 1
        if self.row == self.rows:
            self.grid.pop(0)
            self.grid.append([" "] * self.cols)
            self.row -= 1

    def feed(self, data):
        self.pending += self.decoder.decode(data)
        while self.pending:
            value = self.pending
            if value.startswith("\x1b["):
                match = re.match(r"\x1b\[([0-?]*)([ -/]*)([@-~])", value)
                if not match:
                    return
                self.csi(*match.groups())
                self.pending = value[match.end():]
                continue
            if value.startswith(("\x1b]", "\x1bP", "\x1b_", "\x1b^")):
                match = re.search(r"\x07|\x1b\\", value[2:])
                if not match:
                    return
                body = value[2:2 + match.start()]
                if value.startswith("\x1b]") and body in ("10;?", "11;?"):
                    color = "ffff/ffff/ffff" if body.startswith("10") else "0000/0000/0000"
                    self.replies.append(("\x1b]" + body[:2] + ";rgb:" + color + "\x1b\\").encode())
                self.pending = value[2 + match.end():]
                continue
            if value[0] == "\x1b":
                if len(value) < 2:
                    return
                code = value[1]
                length = 3 if code in "()#%" else 2
                if len(value) < length:
                    return
                if code == "7":
                    self.saved = (self.row, self.col)
                elif code == "8":
                    self.row, self.col = self.saved
                elif code == "D":
                    self.newline()
                elif code == "E":
                    self.newline()
                    self.col = 0
                elif code == "M":
                    self.row = max(0, self.row - 1)
                elif code == "c":
                    self.grid = [[" "] * self.cols for _ in range(self.rows)]
                    self.row = self.col = 0
                elif code not in "()#%=>\\":
                    raise AssertionError("unsupported terminal escape: " + repr(value[:length]))
                self.pending = value[length:]
                continue
            self.pending = value[1:]
            char = value[0]
            if char == "\r":
                self.col = 0
            elif char == "\n":
                self.newline()
            elif char == "\b":
                self.col = max(0, self.col - 1)
            elif char == "\t":
                self.col = min(self.cols - 1, (self.col // 8 + 1) * 8)
            elif char >= " " and char != "\x7f":
                if self.col >= self.cols:
                    self.col = 0
                    self.newline()
                self.grid[self.row][self.col] = Cell(char, self.style)
                self.col += 1

    def csi(self, raw, intermediate, final):
        if final == "m" and not raw.startswith(("?", ">", "=")):
            self.sgr(raw)
            return
        private = bool(raw) and raw[:1] in "?=>"
        values = [int(x) if x.isdigit() else 0 for x in raw.lstrip("?=>").split(";")]
        n = values[0] or 1
        r, c = self.row, min(self.col, self.cols - 1)
        if final in ("H", "f"):
            self.row = min(self.rows - 1, n - 1)
            self.col = min(self.cols - 1, (values[1] if len(values) > 1 and values[1] else 1) - 1)
        elif final in "ABCD":
            self.row = max(0, min(self.rows - 1, r + (n if final == "B" else -n if final == "A" else 0)))
            self.col = max(0, min(self.cols - 1, c + (n if final == "C" else -n if final == "D" else 0)))
        elif final in "EF":
            self.row = max(0, min(self.rows - 1, r + (n if final == "E" else -n)))
            self.col = 0
        elif final in ("G", chr(96)):
            self.col = min(self.cols - 1, n - 1)
        elif final == "d":
            self.row = min(self.rows - 1, n - 1)
        elif final in "JK":
            mode = values[0]
            if final == "J":
                positions = [(y, x) for y in range(self.rows) for x in range(self.cols)]
            else:
                positions = [(r, x) for x in range(self.cols)]
            for y, x in positions:
                if mode == 2 or (mode == 0 and (y, x) >= (r, c)) or (mode == 1 and (y, x) <= (r, c)):
                    self.grid[y][x] = " "
        elif final == "X":
            self.grid[r][c:min(self.cols, c + n)] = [" "] * min(n, self.cols - c)
        elif final == "P":
            self.grid[r] = (self.grid[r][:c] + self.grid[r][c + n:] + [" "] * n)[:self.cols]
        elif final == "@":
            self.grid[r] = (self.grid[r][:c] + [" "] * n + self.grid[r][c:])[:self.cols]
        elif final in "LM":
            for _ in range(min(n, self.rows - r)):
                if final == "L":
                    self.grid.insert(r, [" "] * self.cols)
                    self.grid.pop()
                else:
                    self.grid.pop(r)
                    self.grid.append([" "] * self.cols)
        elif final in "ST":
            for _ in range(min(n, self.rows)):
                if final == "S":
                    self.grid.pop(0)
                    self.grid.append([" "] * self.cols)
                else:
                    self.grid.pop()
                    self.grid.insert(0, [" "] * self.cols)
        elif final == "b":
            char = self.grid[r][max(0, c - 1)]
            for _ in range(n):
                if self.col >= self.cols:
                    self.col = 0
                    self.newline()
                self.grid[self.row][self.col] = Cell(str(char), self.style)
                self.col += 1
        elif final == "s":
            self.saved = (r, c)
        elif final == "u" and not private:
            self.row, self.col = self.saved
        elif final == "n" and values[0] == 6:
            self.replies.append(f"\x1b[{r + 1};{c + 1}R".encode())
        elif final == "c":
            self.replies.append(b"\x1b[>0;0;0c" if raw.startswith(">") else b"\x1b[?1;2c")
        elif final == "u" and raw == "?":
            self.replies.append(b"\x1b[?0u")
        elif final == "h" and raw == "?1049":
            self.grid = [[" "] * self.cols for _ in range(self.rows)]
            self.row = self.col = 0
        elif final == "p" and intermediate == "$" and raw.startswith("?"):
            self.replies.append(("\x1b[" + raw + ";0$y").encode())
        elif final == "t" and values[0] == 18:
            self.replies.append(f"\x1b[8;{self.rows};{self.cols}t".encode())
        elif final not in "mhlqntu":
            raise AssertionError("unsupported terminal CSI: " + repr((raw, intermediate, final)))

    def sgr(self, raw):
        palette = ("#000000", "#cc5555", "#55aa55", "#c4a000",
                   "#5555cc", "#aa55aa", "#55aaaa", "#cccccc",
                   "#777777", "#ff7777", "#77dd77", "#eeee77",
                   "#7777ff", "#ff77ff", "#77eeee", "#ffffff")
        raw = raw.replace("38:2::", "38;2;").replace("48:2::", "48;2;")
        values = [int(value) if value else 0 for value in raw.replace(":", ";").split(";")]
        fg, bg, bold = self.style
        index = 0
        while index < len(values):
            value = values[index]
            index += 1
            if value == 0:
                fg, bg, bold = "#d8dee9", "#111827", False
            elif value == 1:
                bold = True
            elif value == 22:
                bold = False
            elif value == 39:
                fg = "#d8dee9"
            elif value == 49:
                bg = "#111827"
            elif 30 <= value <= 37 or 90 <= value <= 97:
                fg = palette[value - 30 if value < 90 else value - 90 + 8]
                self.color_events += 1
            elif 40 <= value <= 47 or 100 <= value <= 107:
                bg = palette[value - 40 if value < 100 else value - 100 + 8]
                self.color_events += 1
            elif value in (38, 48) and index < len(values):
                mode = values[index]
                index += 1
                if mode == 5 and index < len(values):
                    number = values[index]
                    index += 1
                    if number < 16:
                        color = palette[number]
                    elif number < 232:
                        number -= 16
                        levels = (0, 95, 135, 175, 215, 255)
                        color = "#%02x%02x%02x" % (levels[number // 36],
                            levels[(number // 6) % 6], levels[number % 6])
                    else:
                        level = 8 + (number - 232) * 10
                        color = "#%02x%02x%02x" % (level, level, level)
                elif mode == 2 and index + 2 < len(values):
                    color = "#%02x%02x%02x" % tuple(values[index:index + 3])
                    index += 3
                else:
                    raise AssertionError("unsupported terminal color: " + repr(raw))
                if value == 38:
                    fg = color
                else:
                    bg = color
                self.color_events += 1
        self.style = fg, bg, bold

    def resize(self, rows, cols):
        self.grid = [(row[:cols] + [" "] * cols)[:cols] for row in self.grid[:rows]]
        self.grid += [[" "] * cols for _ in range(rows - len(self.grid))]
        self.rows, self.cols = rows, cols
        self.row, self.col = min(self.row, rows - 1), min(self.col, cols - 1)

    def svg(self):
        # Render the observed VT cells, including SGR colors, without a browser
        # or image package. terminal.raw remains the unmodified source evidence.
        width, height = self.cols * 9 + 24, self.rows * 18 + 24
        parts = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
                 '<title>temo PTY current-screen capture</title>',
                 f'<rect width="{width}" height="{height}" fill="#111827"/>',
                 '<g font-family="monospace" font-size="15" xml:space="preserve">']
        for y, row in enumerate(self.grid):
            for x, cell in enumerate(row):
                fg, bg, bold = getattr(cell, "style", ("#d8dee9", "#111827", False))
                if bg != "#111827":
                    parts.append(f'<rect x="{12 + x * 9}" y="{8 + y * 18}" width="9" height="18" fill="{bg}"/>')
                if cell != " ":
                    weight = ' font-weight="bold"' if bold else ""
                    parts.append(f'<text x="{12 + x * 9}" y="{22 + y * 18}" fill="{fg}"{weight}>{html.escape(cell)}</text>')
        return "\n".join(parts + ["</g></svg>"])

    def text(self):
        return "\n".join("".join(row).rstrip() for row in self.grid)


class Terminal:
    def __init__(self, binary, root, out, no_color=True):
        self.out, self.index = out, 0
        self.screen = Screen()
        self.raw = (out / "terminal.raw").open("wb")
        self.actions = (out / "actions.log").open("w")
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 120, 0, 0))
        env = dict(os.environ, HOME=str(root / "home"), PATH=str(root / "bin"),
                   TERM="xterm-256color", TEMO_KEYBOARD_ROOT=str(root),
                   TEMO_KEYBOARD_LOG=str(out / "backend.jsonl"))
        if no_color:
            env["NO_COLOR"] = "1"
        else:
            env.pop("NO_COLOR", None)
        env.pop("CLICOLOR", None)
        env.pop("CLICOLOR_FORCE", None)
        def child_setup():
            os.setsid()
            fcntl.ioctl(0, termios.TIOCSCTTY, 0)
        try:
            self.proc = subprocess.Popen([str(binary), "--config-dir", str(root / "cfg")],
                                         stdin=slave, stdout=slave, stderr=slave, env=env,
                                         preexec_fn=child_setup)
        except Exception:
            os.close(self.master)
            self.raw.close()
            self.actions.close()
            raise
        finally:
            os.close(slave)

    def pump(self, seconds=0.05):
        if not select.select([self.master], [], [], seconds)[0]:
            return
        try:
            data = os.read(self.master, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                return
            raise
        if data:
            self.raw.write(data)
            self.raw.flush()
            self.screen.feed(data)
            for reply in self.screen.replies:
                os.write(self.master, reply)
            self.screen.replies.clear()

    def capture(self, label):
        self.index += 1
        text = self.screen.text()
        (self.out / f"{self.index:03}-{label}.txt").write_text(text + "\n")
        (self.out / f"{self.index:03}-{label}.svg").write_text(self.screen.svg() + "\n")
        return text

    def send(self, data):
        self.capture("before-input")
        self.actions.write(repr(data) + "\n")
        self.actions.flush()
        os.write(self.master, data)
        end = time.monotonic() + 0.15
        while time.monotonic() < end:
            self.pump()

    def expect(self, predicate, label, timeout=10):
        deadline, last, stable = time.monotonic() + timeout, None, 0
        while time.monotonic() < deadline:
            self.pump()
            text = self.screen.text()
            if predicate(text):
                if text == last and time.monotonic() - stable >= 0.12:
                    return self.capture(label)
                if text != last:
                    last, stable = text, time.monotonic()
            else:
                last = None
            if self.proc.poll() is not None:
                break
        self.capture("FAILED-" + label)
        raise AssertionError("terminal did not reach " + label)

    def resize(self, rows, cols):
        self.actions.write(f"resize {cols}x{rows}\n")
        self.actions.flush()
        self.screen.resize(rows, cols)
        fcntl.ioctl(self.master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        os.kill(self.proc.pid, signal.SIGWINCH)

    def quit(self):
        self.send(b"q")
        deadline = time.monotonic() + 3
        while self.proc.poll() is None and time.monotonic() < deadline:
            self.pump()
        assert self.proc.poll() == 0, "q did not quit promptly and successfully"

    def close(self):
        # Cancellation assertions precede cleanup; cleanup cannot manufacture a pass.
        with contextlib.suppress(ProcessLookupError):
            os.killpg(self.proc.pid, signal.SIGTERM)
        with contextlib.suppress(subprocess.TimeoutExpired):
            self.proc.wait(timeout=2)
        with contextlib.suppress(ProcessLookupError):
            os.killpg(self.proc.pid, signal.SIGKILL)
        self.proc.wait(timeout=2)
        os.close(self.master)
        self.raw.close()
        self.actions.close()


def snapshot(config):
    return {path.name: {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                       "mode": path.stat().st_mode & 0o777}
            for path in sorted(config.glob("*.json"))}


def calls(out):
    path = out / "backend.jsonl"
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def alive(pid):
    try:
        # A killed process awaiting reaping is no longer executing.
        return Path(f"/proc/{pid}/stat").read_text().split(")", 1)[1].split()[0] != "Z"
    except FileNotFoundError:
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", required=True, type=Path)
    parser.add_argument("--binary", type=Path, help="already-built temo; otherwise build once")
    args = parser.parse_args()
    assert sys.platform.startswith("linux"), "this smoke currently supports Linux PTYs only"
    out = args.evidence.resolve()
    out.mkdir(parents=True, exist_ok=True)
    assert not any(out.iterdir()), "use an empty evidence directory"
    root = Path(tempfile.mkdtemp(prefix="temo-keys-"))
    for name in ("home", "cfg", "bin"):
        (root / name).mkdir(mode=0o700)
    ui = None
    try:
        binary = args.binary.resolve() if args.binary else root / "temo"
        if not args.binary:
            with (out / "build.log").open("w") as log:
                subprocess.run(["go", "build", "-o", str(binary), "."],
                               cwd=Path(__file__).resolve().parents[3], stdout=log,
                               stderr=subprocess.STDOUT, check=True)
        (out / "environment.json").write_text(json.dumps({
            "binary": str(binary), "scratch": str(root), "terminals": ["120x32", "50x14", "50x20", "40x10"],
            "color_modes": ["NO_COLOR=1", "xterm-256color"],
            "backends": "read-only denying stubs; no network, credentials or SSH server"}, indent=2))
        for name in ("ssh", "tailscale", "open", "xdg-open"):
            path = root / "bin" / name
            path.write_text("#!" + sys.executable + "\n" + STUB)
            path.chmod(0o700)
        entries = []
        for number, name, kind, host in ((1, "ssh-alpha", "ssh", "smoke-a"),
                                        (2, "ssh-bravo", "ssh", "smoke-b"),
                                        (3, "serve-charlie", "serve", ""),
                                        (4, "serve-delta", "serve", "smoke-remote")):
            entry = {"id": f"{number:016x}", "name": name, "kind": kind,
                     "ssh_host": host, "target_host": "127.0.0.1", "target_port": 3000}
            entry["local_port" if kind == "ssh" else "serve_port"] = 8000 + number
            entries.append(entry)
            owner = {"version": 1, "entry": entry, "phase": "active"}
            if kind == "serve":
                owner.update(node_id="keyboard-node", snapshot="{}")
            path = root / "cfg" / ("owner-" + entry["id"] + ".json")
            path.write_text(json.dumps(owner) + "\n")
            path.chmod(0o600)
        cfg = root / "cfg" / "config.json"
        cfg.write_text(json.dumps({"version": 1, "entries": entries}) + "\n")
        cfg.chmod(0o600)
        before = snapshot(cfg.parent)
        (out / "before.json").write_text(json.dumps(before, indent=2))
        ui = Terminal(binary, root, out)
        def row(name, selected=False, stopped=False):
            marker = r">\s+" if selected else r"(?:>\s+)?"
            state = r"stopped(?:; cleanup needed)?" if stopped else r"[^│\n]+"
            return lambda s: bool(re.search(rf"^\s*│\s*{marker}{re.escape(name)}\s+{state}(?:\s|│|$)", s, re.M))
        def table(text, kind, narrow=False, selected_only=False):
            labels = ["NAME", "STATUS", "SSH HOST", "LOCAL" if kind == "ssh" else "HTTPS", "TARGET"]
            lines = text.splitlines()
            heading = next(line for line in lines if "NAME" in line and "STATUS" in line)
            endpoint_heading = next(line for line in lines if "SSH HOST" in line and labels[3] in line and "TARGET" in line)
            assert narrow or heading == endpoint_heading, "wide table headers are not on one row"
            fixtures = entries[:2] if kind == "ssh" else entries[2:]
            if selected_only:
                fixtures = [entry for entry in fixtures if row(entry["name"], selected=True)(text)]
                assert len(fixtures) == 1, "minimum-size table lost its selected route"
            for entry in fixtures:
                name = entry["name"]
                index = next(index for index, line in enumerate(lines) if row(name)(line))
                line = lines[index]
                assert line[heading.index("NAME"):heading.index("STATUS")].strip() == name
                status_end = len(line) - 1 if narrow else heading.index("SSH HOST")
                state = line[heading.index("STATUS"):status_end].strip()
                assert state in ("unknown", "queued", "checking", "stopped", "stopped; cleanup needed"), state
                endpoints = lines[index + 1] if narrow else line
                host_at, port_at, target_at = (endpoint_heading.index(label) for label in labels[2:])
                host = endpoints[host_at:port_at].strip()
                expected_host = entry["ssh_host"] or "local"
                assert host == expected_host or (narrow and host.endswith("…") and
                    expected_host.startswith(host[:-1])), (name, host)
                port = entry["local_port" if kind == "ssh" else "serve_port"]
                assert endpoints[port_at:target_at].strip() == f":{port}", endpoints
                assert endpoints[target_at:].strip(" │") == "127.0.0.1:3000", endpoints
        def field_value(text, label):
            match = re.search(r"^[ >]\s+" + re.escape(label) + r"\s+(.*)$", text, re.M)
            assert match, ("missing form field", label)
            return match.group(1).replace("│", "").strip()
        def ports(target, local):
            return lambda s: field_value(s, "Target port") == target and field_value(s, "Local port") == local
        def focus(label):
            return lambda s: bool(re.search(r"^>\s+" + re.escape(label) + r"(?:\s|$)", s, re.M))
        def move(data, predicate, label):
            ui.send(data)
            return ui.expect(predicate, label)
        first = ui.expect(lambda s: "[1 SSH]" in s and "[2 Serve]" in s and row("ssh-alpha", True)(s), "launch")
        assert not row("serve-charlie")(first)
        table(first, "ssh")
        for data, name in ((b"\x1b[B", "ssh-bravo"), (b"k", "ssh-alpha"),
                           (b"j", "ssh-bravo"), (b"\x1b[A", "ssh-alpha")):
            move(data, row(name, True), "list-" + name)
        for data, name in ((b"2", "serve-charlie"), (b"\x1b[D", "ssh-alpha"),
                           (b"\x1b[C", "serve-charlie"), (b"1", "ssh-alpha")):
            screen = move(data, row(name, True), "tab-" + name)
            assert not row("ssh-alpha" if name.startswith("serve") else "serve-charlie")(screen)
            table(screen, "serve" if name.startswith("serve") else "ssh")
        for tab, labels in ((b"1", ["Name", "SSH host", "Target host", "Target port", "Local port"]),
                            (b"2", ["Name", "SSH host", "Target port", "HTTPS port"])):
            ui.send(tab)
            screen = move(b"a", focus("Name"), "add-" + tab.decode())
            field_labels = re.findall(r"^[ >]\s+(Name|Kind[^ ]*|SSH host|Target host|Target port|Local port|HTTPS port)\b", screen, re.M)
            assert field_labels == labels, (field_labels, labels)
            screen = move(b"jk", focus("Name"), "literal-jk-" + tab.decode())
            assert re.search(r"^>\s+Name\s+jk", screen, re.M), "j/k were not entered as text"
            move(b"\x1b[B", focus(labels[1]), "down-field")
            move(b"\t", focus(labels[2]), "tab-field")
            move(b"\x1b[Z", focus(labels[1]), "shift-tab-field")
            move(b"\r", focus(labels[2]), "enter-field")
            move(b"\x1b[A", focus(labels[1]), "up-field")
            move(b"\x1b", lambda s: "Edit cancelled" in s, "cancel-form")
        # Add-form defaults are exercised without saving or contacting a backend.
        ui.send(b"1")
        screen = move(b"a", focus("Name"), "linked-ports-default")
        assert ports("3000", "3000")(screen)
        assert "Local port follows target until edited." in screen
        move(b"\t\t\t", focus("Target port"), "linked-target-field")
        ui.send(b"\x15")
        move(b"4100", ports("4100", "4100"), "linked-ports-typed")
        ui.send(b"\x15")
        move(b"\x1b[200~4200\x1b[201~", ports("4200", "4200"), "linked-ports-pasted")
        move(b"\t", focus("Local port"), "linked-local-navigation")
        ui.send(b"\x1b[H")
        ui.send(b"\x1b[F")
        move(b"\x1b[Z", focus("Target port"), "linked-target-return")
        ui.send(b"\x15")
        move(b"4500", ports("4500", "4500"), "linked-after-cursor-moves")
        move(b"\t", focus("Local port"), "override-local-field")
        ui.send(b"\x15")
        move(b"\x1b[200~9100\x1b[201~", ports("4500", "9100"), "explicit-local-port")
        move(b"\x1b[Z", focus("Target port"), "override-target-field")
        ui.send(b"\x15")
        screen = move(b"4600", ports("4600", "9100"), "explicit-local-preserved")
        assert "Local port is set independently." in screen
        move(b"\x1b", lambda s: "Edit cancelled" in s, "cancel-linked-form")
        screen = move(b"a", focus("Name"), "linked-defaults-reopened")
        assert ports("3000", "3000")(screen), "a cancelled override leaked into a new form"
        move(b"\x1b", lambda s: "Edit cancelled" in s, "cancel-reopened-form")
        assert snapshot(cfg.parent) == before, "cancelled port edits changed saved files"
        # Resize through stacked columns and the explicit too-small fallback.
        ui.resize(14, 50)
        screen = ui.expect(lambda s: row("ssh-alpha", True)(s) and "TARGET" in s, "minimum-ssh-table")
        table(screen, "ssh", narrow=True, selected_only=True)
        assert not row("ssh-bravo")(screen), "minimum viewport unexpectedly contains two routes"
        screen = move(b"\x1b[B", row("ssh-bravo", True), "minimum-next-route")
        table(screen, "ssh", narrow=True, selected_only=True)
        move(b"\x1b[A", row("ssh-alpha", True), "minimum-previous-route")
        ui.resize(20, 50)
        screen = ui.expect(lambda s: row("ssh-alpha", True)(s) and row("ssh-bravo")(s), "narrow-ssh-table")
        table(screen, "ssh", narrow=True)
        screen = move(b"2", row("serve-charlie", True), "narrow-serve-table")
        table(screen, "serve", narrow=True)
        ui.resize(10, 40)
        ui.expect(lambda s: "Resize to at least" in s, "too-small-resize")
        ui.resize(32, 120)
        screen = ui.expect(lambda s: row("serve-charlie", True)(s) and "TARGET" in s, "wide-table-restored")
        table(screen, "serve")
        move(b"p", lambda s: str(cfg) in "".join(s.split()), "config-path")
        ui.send(b"\x1b")
        move(b"?", lambda s: bool(re.search(r"\bp\b.*[Cc]onfig", s)), "help-config-hint")
        move(b"p", lambda s: str(cfg) in "".join(s.split()), "config-path-from-help")
        ui.send(b"\x1b")
        ui.send(b"\x1b")
        ui.send(b"1")
        move(b"/ssh-alpha\r", row("ssh-alpha", True), "filtered-one-entry")
        ui.send(b"r")
        ui.expect(lambda s: bool(re.search(r"Refreshing \d+/4 \(all tabs\)", s)), "refresh-progress")
        # The pending Serve row remains guarded; completed SSH rows may be acted on.
        ui.send(b"\x1b")
        move(b"2", row("serve-charlie", True), "refresh-other-tab")
        for key in (b"s", b"x", b"e", b"\x13"):
            ui.send(key)
        move(b"\x1b[B", row("serve-delta", True), "refresh-navigation")
        assert snapshot(cfg.parent) == before, "refresh/guarded keys changed saved files"
        (root / "release-status").touch()
        ui.expect(lambda s: row("serve-charlie", stopped=True)(s) and row("serve-delta", stopped=True)(s)
                  and "Refreshing " not in s, "serve-refreshed")
        move(b"1", lambda s: row("ssh-alpha", stopped=True)(s) and row("ssh-bravo", stopped=True)(s), "ssh-refreshed")
        observed = [item for item in calls(out) if item["phase"] == "call"]
        assert len(observed) == 6 and all(item["allowed"] for item in observed), observed
        assert {item["args"][-1] for item in observed if "-O" in item["args"]} == {"smoke-a", "smoke-b"}
        assert sum(item["tool"] == "tailscale" for item in observed) == 2
        assert sum(item["tool"] == "ssh" and "-T" in item["args"] for item in observed) == 2
        assert snapshot(cfg.parent) == before, "read-only refresh altered config or ownership"
        (root / "release-status").unlink()
        baseline = len(calls(out))
        ui.send(b"r")
        ui.expect(lambda s: "Refreshing " in s and any(item["phase"] == "blocked" for item in calls(out)[baseline:]), "refresh-to-cancel")
        blocked = {item["pid"] for item in calls(out)[baseline:] if item["phase"] == "blocked"}
        assert blocked and all(alive(pid) for pid in blocked), "no live delayed probe to cancel"
        started = time.monotonic()
        ui.quit()
        deadline = time.monotonic() + 3
        while any(alive(pid) for pid in blocked) and time.monotonic() < deadline:
            time.sleep(0.05)
        assert not any(alive(pid) for pid in blocked), "refresh child survived q cancellation"
        (out / "cancellation.json").write_text(json.dumps({"blocked_pids": sorted(blocked),
            "elapsed_seconds": time.monotonic() - started, "children_running": False}, indent=2))
        assert snapshot(cfg.parent) == before, "cancellation altered config or ownership"
        assert all(item["allowed"] for item in calls(out)), "a non-read-only command was attempted"
        assert ui.screen.color_events == 0, "NO_COLOR emitted a colored SGR sequence"
        ui.close()
        ui = None
        # Reopen the same immutable fixtures with color enabled. This is another
        # read-only status sweep through the same exact denying stubs.
        color_out = out / "color"
        color_out.mkdir()
        (root / "release-status").touch()
        ui = Terminal(binary, root, color_out, no_color=False)
        screen = ui.expect(lambda s: row("ssh-alpha", True, stopped=True)(s) and "Refreshing " not in s,
                           "colored-ssh-table")
        table(screen, "ssh")
        assert ui.screen.color_events > 0, "color-capable terminal received no color"
        lines = screen.splitlines()
        selected_y = next(index for index, line in enumerate(lines) if row("ssh-alpha", True)(line))
        other_y = next(index for index, line in enumerate(lines) if row("ssh-bravo")(line))
        selected_name = ui.screen.grid[selected_y][lines[selected_y].index("ssh-alpha")]
        other_name = ui.screen.grid[other_y][lines[other_y].index("ssh-bravo")]
        status_cell = ui.screen.grid[selected_y][lines[selected_y].index("stopped")]
        assert getattr(selected_name, "style", (None, None))[1] != getattr(other_name, "style", (None, None))[1], "selection has no distinct background"
        assert getattr(status_cell, "style", (None,))[0] != getattr(selected_name, "style", (None,))[0], "cleanup status has no distinct foreground"
        screen = move(b"2", row("serve-charlie", True, stopped=True), "colored-serve-table")
        table(screen, "serve")
        ui.quit()
        color_calls = [item for item in calls(color_out) if item["phase"] == "call"]
        assert len(color_calls) == 6 and all(item["allowed"] for item in color_calls), color_calls
        assert snapshot(cfg.parent) == before, "color-mode launch altered config or ownership"
        (out / "color-checks.json").write_text(json.dumps({
            "no_color_colored_sgr_count": 0,
            "enabled_colored_sgr_count": ui.screen.color_events,
            "captures": "numbered .txt and .svg current screens plus terminal.raw in each mode"
        }, indent=2))
    except Exception as error:
        (out / "result.txt").write_text("FAIL: " + repr(error) + "\n")
        raise
    finally:
        try:
            if ui is not None:
                ui.close()
            if (root / "cfg" / "config.json").exists():
                (out / "after.json").write_text(json.dumps(snapshot(root / "cfg"), indent=2))
                for path in (root / "cfg").glob("*.json"):
                    (out / path.name).write_bytes(path.read_bytes())
        except Exception as error:
            (out / "result.txt").write_text("FAIL during cleanup: " + repr(error) + "\n")
            raise
        finally:
            print("Evidence:", out, "Fixture:", root, flush=True)
    (out / "result.txt").write_text("PASS: Linux PTY aligned SSH/Serve tables, visible ports, narrow resize, color and NO_COLOR, linked SSH port defaults and explicit override, tabs, navigation, form keys, config path, all-entry read-only refresh and cancellation. Backends were stubs; no real SSH, Tailscale, network or browser was tested.\n")


if __name__ == "__main__":
    main()
