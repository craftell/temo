#!/usr/bin/env python3
"""Run temo in a private tmux server with loopback SSH and fake Tailscale."""
import argparse
import contextlib
import http.server
import json
import os
from pathlib import Path
import pwd
import re
import shlex
import shutil
import socket
import subprocess
import tempfile
import threading
import time


STUB = '''#!/usr/bin/env python3
import json, os, sys, time
from pathlib import Path
p = Path(os.environ["TEMO_LAB"])
a = sys.argv[1:]
with (p / "tailscale-calls.jsonl").open("a") as f:
    f.write(json.dumps(a) + "\\n")
mode = (p / "mode").read_text().strip()
if a == ["status", "--json"]:
    if mode == "diagnostic":
        print("diagnostic failure " + " ".join("line-%03d" % i for i in range(150)), file=sys.stderr)
        sys.exit(1)
    def span(event):
        with (p / "status-spans.jsonl").open("a") as f:
            f.write(json.dumps({"event": event, "pid": os.getpid(), "ppid": os.getppid(), "t": time.time()}) + "\\n")
    span("start")
    if mode == "slow":
        time.sleep(1.5)
    span("end")
    print(json.dumps({"BackendState":"Running", "Self":{
        "ID":"changed" if mode == "node" else "lab-node",
        "DNSName":"lab.example.ts.net."}}))
elif a == ["serve", "status", "--json"]:
    print("not json" if mode == "invalid" else (p / "serve.json").read_text())
elif len(a) in (5, 6) and a[:2] == ["serve", "--bg"] and a[3] == "--set-path=/":
    port = a[2].removeprefix("--https=")
    assert a[2] == "--https=" + port and port.isdigit()
    assert a[4].startswith("http://127.0.0.1:")
    assert len(a) == 5 or a[5] == "off"
    value = {} if len(a) == 6 else {
        "TCP":{port:{"HTTPS":True}},
        "Web":{"lab.example.ts.net:"+port:{"Handlers":{"/":{"Proxy":a[4]}}}}}
    (p / "serve.json").write_text(json.dumps(value))
    if mode == "uncertain":
        print("simulated transport failure after applying route", file=sys.stderr)
        sys.exit(1)
else:
    print("unexpected tailscale invocation", file=sys.stderr)
    sys.exit(2)
'''


def has_table_state(screen, state, selected=False):
    lines = screen.splitlines()
    header = next((line for line in lines if "NAME" in line and "STATUS" in line), None)
    if header is None:
        return False
    start = header.index("STATUS")
    end = header.index("SSH HOST") if "SSH HOST" in header else len(header.rstrip()) - 2
    return any(line.lstrip().startswith("│") and
               (not selected or line.lstrip().startswith("│ > ")) and
               line[start:end].strip() == state for line in lines)


class QuietHTTP(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"temo loopback verification\n")

    def log_message(self, *args):
        pass


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class Lab:
    def __init__(self, evidence):
        self.out = evidence.resolve()
        if self.out.exists() and any(self.out.iterdir()):
            raise ValueError("evidence directory must be empty")
        self.out.mkdir(parents=True, exist_ok=True)
        self.root = Path(tempfile.mkdtemp(prefix="temo-v-"))
        self.home = self.root / "home"
        (self.home / ".ssh").mkdir(parents=True, mode=0o700)
        self.config = self.root / "config"
        self.config.mkdir(mode=0o700)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.socket_name = "temo-verify-" + str(os.getpid())
        self.env = dict(os.environ, TEMO_LAB=str(self.root), HOME=str(self.home),
                        PATH=str(self.bin) + os.pathsep + os.environ["PATH"])
        self.processes = []
        self.screen = 0
        self.http = None
        self.logfile = (self.out / "actions.log").open("w")

    def note(self, text):
        self.logfile.write(text + "\n")
        self.logfile.flush()
        print(text, flush=True)

    def run(self, args, **kwargs):
        result = subprocess.run([str(a) for a in args], text=True,
                                capture_output=True, **kwargs)
        with (self.out / "commands.jsonl").open("a") as log:
            log.write(json.dumps({"args": [str(a) for a in args], "exit": result.returncode}) + "\n")
        if result.returncode:
            (self.out / "command-failure.txt").write_text(
                shlex.join([str(a) for a in args]) + "\n" + result.stdout + result.stderr)
            result.check_returncode()
        return result

    def tmux(self, *args):
        return self.run(["tmux", "-L", self.socket_name, "-f", "/dev/null", *args])

    def capture(self, label):
        text = self.tmux("capture-pane", "-p", "-t", "app").stdout
        self.screen += 1
        (self.out / f"{self.screen:03}-{label}.txt").write_text(text)
        return text

    def wait(self, text, label):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            screen = self.tmux("capture-pane", "-p", "-t", "app").stdout
            # State waits refer to the selected table cell, not old preview text.
            present = (has_table_state(screen, text.removeprefix("State: "), selected=True)
                       if text.startswith("State: ") else text in screen)
            if present and "Working:" not in screen and "Refreshing " not in screen:
                self.capture(label)
                return screen
            time.sleep(0.1)
        self.capture("FAILED-" + label)
        raise AssertionError(f"screen did not reach {text!r} ({label})")

    def key(self, *keys):
        self.note("keys " + " ".join(keys))
        for key in keys:
            self.tmux("send-keys", "-t", "app", key)
            time.sleep(0.15)

    def text(self, text):
        self.note("type " + repr(text))
        self.tmux("send-keys", "-t", "app", "-l", text)

    def executable(self, name, content):
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o700)

    def setup(self):
        for tool in ("go", "tmux", "ssh", "ssh-keygen", "curl"):
            assert shutil.which(tool), f"missing {tool}"
        sshd = shutil.which("sshd") or "/usr/sbin/sshd"
        assert Path(sshd).exists(), "missing sshd"
        repo = Path(__file__).resolve().parents[3]
        self.note("build " + str(repo))
        self.run(["go", "build", "-o", self.root / "temo", "."], cwd=repo)
        metadata = {"commit": self.run(["git", "rev-parse", "HEAD"], cwd=repo).stdout.strip(),
                    "platform": self.run(["uname", "-a"]).stdout.strip(),
                    "go": self.run(["go", "version"]).stdout.strip(),
                    "scratch": str(self.root), "terminal": "100x32",
                    "GOFLAGS": os.environ.get("GOFLAGS", ""),
                    "helper_pid": os.getpid(), "tmux_server": self.socket_name,
                    "backends": "real loopback sshd; fake local and SSH Tailscale; fake URL opener"}
        (self.out / "environment.json").write_text(json.dumps(metadata, indent=2))
        self.http = http.server.ThreadingHTTPServer(("127.0.0.1", 0), QuietHTTP)
        threading.Thread(target=self.http.serve_forever, daemon=True).start()
        self.target = self.http.server_port
        self.local = free_port()
        self.sshport = free_port()
        for name in ("host-key", "client-key"):
            self.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", self.root / name])
        public = (self.root / "host-key.pub").read_text().split()
        (self.root / "known_hosts").write_text(f"[127.0.0.1]:{self.sshport} {public[0]} {public[1]}\n")
        (self.root / "serve.json").write_text("{}")
        (self.root / "mode").write_text("normal")
        self.executable("tailscale", STUB)
        self.executable("xdg-open", '#!/bin/sh\nprintf "%s\\n" "$@" >> "$TEMO_LAB/opened-urls"\n')
        self.executable("open", '#!/bin/sh\nexec "' + str(self.bin / "xdg-open") + '" "$@"\n')
        self.executable("remote", '#!/bin/sh\nexport TEMO_LAB=' + shlex.quote(str(self.root)) + '\nexport PATH=' + shlex.quote(self.env["PATH"]) + '\nprintf "%s\\n" "$SSH_ORIGINAL_COMMAND" >> "$TEMO_LAB/remote-commands"\ncase "$SSH_ORIGINAL_COMMAND" in\n  tailscale\\ *) exec /bin/sh -c "$SSH_ORIGINAL_COMMAND";;\n  *) exit 2;;\nesac\n')
        cfg = self.root / "sshd_config"
        cfg.write_text(f"""Port {self.sshport}
ListenAddress 127.0.0.1
HostKey {self.root}/host-key
PidFile {self.root}/sshd.pid
AuthorizedKeysFile {self.root}/client-key.pub
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
StrictModes no
AllowUsers {pwd.getpwuid(os.getuid()).pw_name}
AllowTcpForwarding local
GatewayPorts no
ForceCommand {self.bin}/remote
""")
        self.run([sshd, "-t", "-f", cfg])
        log = (self.out / "sshd.log").open("w")
        proc = subprocess.Popen([sshd, "-D", "-e", "-f", str(cfg)], stdout=log, stderr=log)
        self.processes.append(proc)
        deadline = time.monotonic() + 5
        while True:
            assert proc.poll() is None, "throwaway sshd exited; inspect sshd.log"
            try:
                with socket.create_connection(("127.0.0.1", self.sshport), timeout=0.2):
                    break
            except OSError:
                assert time.monotonic() < deadline, "sshd did not listen"
                time.sleep(0.1)
        client = self.home / ".ssh" / "config"
        aliases = self.home / ".ssh" / "hosts.conf"
        aliases.write_text("Host lab inherited\n")
        client.write_text(f"""Include {aliases}
Host *
 HostName 127.0.0.1
 Port {self.sshport}
 User {pwd.getpwuid(os.getuid()).pw_name}
 IdentityFile {self.root}/client-key
 IdentitiesOnly yes
 UserKnownHostsFile {self.root}/known_hosts
 GlobalKnownHostsFile /dev/null
 StrictHostKeyChecking yes
 GatewayPorts yes
Host inherited
 LocalForward 127.0.0.1:1 127.0.0.1:1
""")
        self.executable("ssh", '#!/bin/sh\nexec /usr/bin/ssh -F ' + shlex.quote(str(client)) + ' "$@"\n')
        self.unrelated = self.root / "unrelated.sock"
        self.run([self.bin / "ssh", "-M", "-S", self.unrelated, "-o", "ControlPersist=yes", "-fN", "lab"], env=self.env)
        self.launch()

    def launch(self):
        command = shlex.join(["env", "TERM=xterm-256color", "PATH=" + self.env["PATH"],
                              "TEMO_LAB=" + str(self.root), "HOME=" + str(self.home), str(self.root / "temo"),
                              "--config-dir", str(self.config)])
        self.tmux("new-session", "-d", "-s", "app", "-x", "100", "-y", "32", command)
        self.tmux("set-option", "-g", "remain-on-exit", "on")
        self.wait("[1 SSH]", "launch")
        self.note("doctor pane pid " + self.tmux("display-message", "-p", "-t", "app", "#{pane_pid}").stdout.strip())

    def quit(self):
        self.key("q")
        deadline = time.monotonic() + 5
        while self.tmux("display-message", "-p", "-t", "app", "#{pane_dead}").stdout.strip() != "1":
            assert time.monotonic() < deadline, "TUI did not exit"
            time.sleep(0.1)
        self.tmux("kill-session", "-t", "app")

    def add(self, name, kind="ssh", host="lab", local=None, serve=8443, pick_host=False, advance="Tab"):
        self.key("1" if kind == "ssh" else "2", "a")
        form = self.wait("Add route", "add-form")
        assert ("HTTPS port" in form) == (kind == "serve"), "form shows the wrong kind's fields"
        assert ("Local port" in form) == (kind == "ssh"), "form shows the wrong kind's fields"
        values = ([name, host, "127.0.0.1", str(self.target), str(local or self.local)]
                  if kind == "ssh" else [name, host, str(self.target), str(serve)])
        for index, value in enumerate(values):
            self.key("C-u")
            if index == 1 and pick_host:
                self.key("C-p")
                screen = self.wait("temo | SSH hosts", "ssh-host-list")
                assert "inherited" in screen and "lab" in screen and "HostName" not in screen
                self.key("Down")
                self.wait("> lab", "ssh-host-selected")
                self.key("Enter")
                self.wait("Add route", "ssh-host-in-form")
            elif value:
                self.text(value)
            self.key(advance)
        self.capture("filled-form")
        self.key("C-s")
        self.wait("Saved " + name, "saved")

    def select(self, name):
        entries = json.loads((self.config / "config.json").read_text())["entries"]
        entry = next(e for e in entries if e["name"] == name)
        self.key("Escape", "1" if entry["kind"] == "ssh" else "2", "/", "C-u")
        self.text(name)
        self.key("Enter")
        self.wait(name, "filtered")

    def action(self, key, expected, label):
        self.capture("before-" + label)
        self.key(key)
        return self.wait(expected, label)

    def fixture(self, value):
        (self.root / "serve.json").write_text(json.dumps(value))

    def mutation_count(self):
        path = self.root / "tailscale-calls.jsonl"
        calls = [json.loads(s) for s in path.read_text().splitlines()] if path.exists() else []
        return sum("--bg" in a for a in calls)

    def refused(self, key, expected, label):
        before = self.mutation_count()
        self.action(key, expected, label)
        assert self.mutation_count() == before, "refused operation mutated Tailscale"

    def entry_named(self, cfgpath, name):
        return next(e for e in json.loads(cfgpath.read_text())["entries"] if e["name"] == name)

    def stale_ssh(self, cfgpath):
        entry = self.entry_named(cfgpath, "local-ssh")
        owner = self.config / ("owner-" + entry["id"] + ".json")
        control = self.config / (entry["id"] + ".sock")
        self.action("s", "State: setup ready", "stale-ssh-start")
        assert owner.exists() and control.exists()
        self.run(["/usr/bin/ssh", "-F", "/dev/null", "-S", control, "-O", "exit", "lab"])
        deadline = time.monotonic() + 5
        while control.exists():
            assert time.monotonic() < deadline, "owned SSH master did not exit"
            time.sleep(0.1)
        with socket.socket() as s:
            assert s.connect_ex(("127.0.0.1", entry["local_port"])) != 0, "forward survived master exit"
        before, mtime = owner.read_bytes(), owner.stat().st_mtime_ns
        screen = self.action("r", "stopped; cleanup needed", "stale-ssh-observed")
        assert re.search(r"^\s*│\s*>?\s*local-ssh\s+stopped; cleanup needed(?:\s|│)", screen, re.M)
        assert owner.read_bytes() == before and owner.stat().st_mtime_ns == mtime, "refresh changed the ownership journal"
        self.action("e", "Editing requires a stopped route", "stale-ssh-edit-refused")
        assert owner.read_bytes() == before
        self.action("x", "stop local-ssh: No managed backend is active", "stale-ssh-released")
        assert not owner.exists(), "explicit stop kept confirmed-stale ownership"
        self.action("e", "Edit route", "stale-ssh-edit-allowed")
        self.key("Escape")
        self.wait("Edit cancelled", "stale-ssh-edit-cancelled")
        self.note("SSH: absent master with retained ownership shows cleanup needed; refresh kept the journal; Stop released it")

    def failed_ssh_start(self, cfgpath):
        self.add("busy-ssh", local=self.target)
        self.select("busy-ssh")
        owner = self.config / ("owner-" + self.entry_named(cfgpath, "busy-ssh")["id"] + ".json")
        self.action("s", "start outcome may be uncertain", "busy-ssh-start-failed")
        self.key("d")
        failure = self.capture("busy-ssh-start-failure-details")
        assert "Stop" in failure and "refresh before retrying" not in failure, "failure does not say how to recover"
        self.key("Escape")
        assert json.loads(owner.read_text())["phase"] == "pending"
        before = owner.read_bytes()
        self.action("r", "use explicit stop before retrying", "busy-ssh-refresh-keeps-pending")
        assert owner.read_bytes() == before, "refresh changed a pending ownership journal"
        self.action("s", "use explicit stop before retrying", "busy-ssh-retry-refused")
        assert owner.read_bytes() == before
        self.action("x", "stop busy-ssh: No managed backend is active", "busy-ssh-released")
        assert not owner.exists()
        self.note("SSH: failed start keeps a pending journal; refresh and retry refuse; Stop releases it")

    def stale_serve(self, cfgpath):
        entry = self.entry_named(cfgpath, "local-serve")
        owner = self.config / ("owner-" + entry["id"] + ".json")
        self.action("s", "State: setup ready", "stale-serve-start")
        assert json.loads(owner.read_text())["phase"] == "active"
        self.fixture({})
        before, mutations = owner.read_bytes(), self.mutation_count()
        screen = self.action("r", "stopped; cleanup needed", "stale-serve-observed")
        assert re.search(r"^\s*│\s*>?\s*local-serve\s+stopped; cleanup needed(?:\s|│)", screen, re.M)
        assert owner.read_bytes() == before and self.mutation_count() == mutations
        self.action("e", "Editing requires a stopped route", "stale-serve-edit-refused")
        self.action("x", "stop local-serve: No managed backend is active", "stale-serve-released")
        assert not owner.exists(), "explicit stop kept confirmed-stale ownership"
        assert self.mutation_count() == mutations, "releasing stale ownership mutated Serve"
        self.note("Serve: route removed outside temo shows cleanup needed; refresh kept the journal; Stop released it without a mutation")

    def spans_since(self, mark):
        path = self.root / "status-spans.jsonl"
        rows = [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []
        return rows[mark:]

    def span_count(self):
        return len(self.spans_since(0))

    def pane_pid(self):
        return int(self.tmux("display-message", "-p", "-t", "app", "#{pane_pid}").stdout.strip())

    def tabs_and_refresh(self, cfgpath):
        saved = cfgpath.read_bytes()
        self.quit()
        config = json.loads(saved)
        for i in range(1, 6):
            seed = {"id": "%016x" % (0xfeed0000 + i), "name": f"seed-{i}", "kind": "serve",
                    "target_host": "127.0.0.1", "target_port": self.target, "serve_port": 8600 + i}
            if i >= 4:
                seed["ssh_host"] = "lab"
            config["entries"].append(seed)
        cfgpath.write_text(json.dumps(config, indent=2) + "\n")
        total = len(config["entries"])
        checked = f"Checked {total} routes; 0 errors"
        self.launch()
        self.wait(checked, "seeded-startup-sweep")
        ssh_tab = self.capture("tab-ssh")
        assert "> [1 SSH]" in ssh_tab and "local-ssh" in ssh_tab and "inherited-ssh" in ssh_tab
        assert "seed-1" not in ssh_tab and "local-serve" not in ssh_tab
        self.key("Right")
        serve_tab = self.wait("seed-1", "tab-serve")
        assert "> [2 Serve]" in serve_tab and "local-serve" in serve_tab and "local-ssh" not in serve_tab
        self.key("Left", "/")
        self.text("inherited")
        self.key("Enter", "Right", "Left")
        filtered = self.capture("tab-filter-kept")
        assert "Filter: inherited" in filtered and "seed-1" not in filtered
        self.key("Right")
        assert "Filter: inherited" not in self.capture("tab-filter-independent")
        (self.root / "mode").write_text("slow")
        mark = self.span_count()
        calls_before = len((self.root / "tailscale-calls.jsonl").read_text().splitlines())
        self.key("r", "r", "r")
        progress = self.capture("refresh-in-flight")
        assert "Refreshing " in progress and has_table_state(progress, "checking") and has_table_state(progress, "queued")
        self.key("Down", "Down", "Up")
        moved = self.capture("navigation-during-refresh")
        assert "Refreshing " in moved
        assert re.search(r"^\s*│ > seed-1\s+", moved, re.M)
        self.key("p")
        assert str(cfgpath) in self.capture("config-path-during-refresh")
        self.key("Escape")
        self.wait(checked, "refresh-complete")
        spans = self.spans_since(mark)
        starts = [row for row in spans if row["event"] == "start"]
        assert len(starts) == 6, f"expected one status call per Serve entry, saw {len(starts)}"
        level = peak = 0
        for row in sorted(spans, key=lambda r: r["t"]):
            level += 1 if row["event"] == "start" else -1
            peak = max(peak, level)
        assert peak == 4, f"expected exactly four concurrent checks, saw {peak}"
        calls = [json.loads(s) for s in (self.root / "tailscale-calls.jsonl").read_text().splitlines()[calls_before:]]
        assert sum(c == ["serve", "status", "--json"] for c in calls) == 6 and not any("--bg" in c for c in calls)
        self.note(f"Refresh: {len(starts)} checks for 3 coalesced r presses, peak {peak} in flight")
        guarded = [self.config / name for name in sorted(os.listdir(self.config)) if name.endswith(".json")]
        hashes = {str(path): path.read_bytes() for path in guarded}
        mark = self.span_count()
        self.key("r")
        time.sleep(0.5)
        pid = self.pane_pid()
        self.quit()
        time.sleep(2.5)
        children = [row for row in self.spans_since(mark) if row["ppid"] == pid]
        started = {row["pid"] for row in children if row["event"] == "start"}
        finished = {row["pid"] for row in children if row["event"] == "end"}
        assert started and not finished, f"quit did not cancel in-flight checks: started={started} finished={finished}"
        for child in started:
            with contextlib.suppress(ProcessLookupError):
                os.kill(child, 0)
                raise AssertionError(f"check process {child} survived quit")
        assert {str(path): path.read_bytes() for path in guarded} == hashes, "quit during refresh changed config or ownership"
        self.note(f"Quit during refresh killed {len(started)} local check processes before they finished")
        cfgpath.write_bytes(saved)
        (self.root / "mode").write_text("normal")
        self.launch()

    def drive(self):
        self.wait("No matching routes", "empty")
        locked = subprocess.run([str(self.root / "temo"), "--config-dir", str(self.config)], text=True, capture_output=True)
        assert locked.returncode != 0 and "another temo process" in locked.stderr
        (self.out / "one-writer.txt").write_text(locked.stderr)
        self.key("a", "C-s")
        self.wait("Error (Ctrl+D): name", "validation-error")
        self.action("C-d", "Form error", "form-error-details")
        self.key("Escape", "Escape")
        self.add("local-q-ssh", pick_host=True)
        cfgpath = self.config / "config.json"
        assert cfgpath.stat().st_mode & 0o777 == 0o600
        self.action("e", "Edit route", "edit")
        self.key("C-u")
        self.text("local-ssh")
        self.key("C-p")
        self.wait("> lab", "edit-ssh-host-list")
        self.key("Enter")
        self.wait("Edit route", "edit-ssh-host-in-form")
        self.key("C-s")
        self.wait("Saved local-ssh", "edited")
        saved = json.loads(cfgpath.read_text())["entries"][0]
        assert saved["name"] == "local-ssh" and saved["kind"] == "ssh"
        assert saved["ssh_host"] == "lab" and saved["target_host"] == "127.0.0.1"
        assert saved["target_port"] == self.target and saved["local_port"] == self.local
        self.action("p", str(cfgpath), "actual-config-path")
        self.key("Escape")
        self.key("/")
        self.text("does-not-match")
        self.wait("No matching routes", "search-empty")
        self.key("Escape")
        self.select("local-ssh")
        self.action("?", "temo help", "help")
        self.key("Escape")
        self.tmux("resize-window", "-t", "app", "-x", "60", "-y", "20")
        self.wait("local-ssh", "resize-60x20")
        self.tmux("resize-window", "-t", "app", "-x", "40", "-y", "10")
        self.wait("Resize to at least", "resize-minimum")
        self.tmux("resize-window", "-t", "app", "-x", "100", "-y", "32")
        self.wait("local-ssh", "resize-restored")
        self.action("r", "State: stopped", "ssh-stopped")
        self.action("s", "State: setup ready", "ssh-start")
        e = json.loads(cfgpath.read_text())["entries"][0]
        owner = self.config / ("owner-" + e["id"] + ".json")
        assert owner.stat().st_mode & 0o777 == 0o600
        assert json.loads(owner.read_text())["phase"] == "active"
        response = self.run(["curl", "--noproxy", "*", "--fail", "--silent", f"http://127.0.0.1:{self.local}"]).stdout
        assert response == "temo loopback verification\n"
        (self.out / "ssh-http.txt").write_text(response)
        with socket.socket() as s:
            assert s.connect_ex(("127.0.0.2", self.local)) != 0, "forward leaked beyond 127.0.0.1"
        self.note("SSH HTTP reached target; 127.0.0.2 refused despite GatewayPorts=yes")
        self.action("o", "Opened URL", "ssh-open")
        self.action("e", "Editing requires a stopped route", "edit-active-refused")
        self.quit()
        assert self.run(["curl", "--noproxy", "*", "--fail", "--silent", f"http://127.0.0.1:{self.local}"]).stdout == response
        self.note("HTTP still reachable after TUI quit")
        self.launch()
        self.wait("local-ssh", "persisted-name")
        self.action("r", "State: setup ready", "ssh-reopened")
        self.action("x", "State: stopped", "ssh-stop")
        deadline = time.monotonic() + 5
        while True:
            with socket.socket() as s:
                if s.connect_ex(("127.0.0.1", self.local)) != 0:
                    break
            assert time.monotonic() < deadline, "forward still listening after stop"
            time.sleep(0.1)
        self.run(["/usr/bin/ssh", "-F", "/dev/null", "-S", self.unrelated, "-O", "check", "lab"])
        self.note("Unrelated ControlMaster survives managed stop")
        self.stale_ssh(cfgpath)
        self.failed_ssh_start(cfgpath)
        self.add("inherited-ssh", host="inherited", local=free_port(), advance="Down")
        self.action("s", "inherits forwarding rules", "inherited-refused")
        self.add("local-serve", kind="serve", host="")
        self.action("r", "State: stopped", "serve-stopped")
        (self.root / "mode").write_text("slow")
        before = self.mutation_count()
        self.key("r", "s", "x", "e")
        busy = self.capture("refresh-mutation-guard")
        assert "Refreshing " in busy, "bulk status did not expose in-progress state"
        self.key("Left", "Right")
        self.wait("State: stopped", "refresh-completed")
        assert self.mutation_count() == before
        self.key("r")
        self.quit()
        assert self.mutation_count() == before
        (self.root / "mode").write_text("normal")
        self.launch()
        self.select("local-serve")
        self.action("s", "State: setup ready", "serve-start")
        original = json.loads((self.root / "serve.json").read_text())
        self.action("o", "Opened URL", "serve-open")
        self.action("r", "State: setup ready", "serve-status")
        self.quit()
        assert json.loads((self.root / "serve.json").read_text()) == original
        self.launch()
        self.select("local-serve")
        self.action("r", "State: setup ready", "serve-reopened")
        self.fixture({**original, "Web":{"lab.example.ts.net:8443":{"Handlers":{"/":{"Text":"drift"}}}}})
        self.refused("x", "changed outside temo", "serve-drift-refused")
        self.fixture(original)
        (self.root / "mode").write_text("node")
        self.refused("x", "node identity changed", "serve-node-refused")
        (self.root / "mode").write_text("normal")
        self.action("x", "State: stopped", "serve-stop")
        self.stale_serve(cfgpath)
        for label, value, expected in [
            ("unmanaged", original, "already configured outside temo"),
            ("funnel", {"AllowFunnel":{"lab.example.ts.net:8443":True}}, "Funnel is enabled"),
            ("foreground", {"Foreground":{"lab":{"TCP":{"8443":{"HTTPS":True}}}}}, "foreground Serve process"),
            ("unknown-schema", {"FutureRoutes":{}}, "unsupported Serve status JSON")]:
            self.fixture(value)
            self.refused("s", expected, "serve-" + label + "-refused")
        self.fixture({})
        (self.root / "mode").write_text("invalid")
        self.refused("s", "unsupported Serve status JSON", "serve-invalid-refused")
        (self.root / "mode").write_text("normal")
        self.tabs_and_refresh(cfgpath)
        self.add("remote-serve", kind="serve", host="lab", serve=9443)
        self.action("s", "State: setup ready", "remote-serve-start")
        assert "9443" in json.loads((self.root / "serve.json").read_text())["TCP"]
        self.action("r", "State: setup ready", "remote-serve-status")
        self.action("o", "Opened URL", "remote-serve-open")
        self.action("x", "State: stopped", "remote-serve-stop")
        self.select("local-serve")
        (self.root / "mode").write_text("uncertain")
        self.action("s", "Serve outcome uncertain", "serve-uncertain")
        self.refused("x", "previous Serve operation has an uncertain", "serve-pending-refused")
        local_serve = next(e for e in json.loads(cfgpath.read_text())["entries"] if e["name"] == "local-serve")
        journal = json.loads((self.config / ("owner-" + local_serve["id"] + ".json")).read_text())
        assert journal["phase"] == "pending" and journal["node_id"] == "lab-node"
        (self.root / "mode").write_text("diagnostic")
        self.action("r", "diagnostic failure", "long-error")
        self.tmux("resize-window", "-t", "app", "-x", "50", "-y", "14")
        initial = self.action("d", "Details 1-", "error-details")
        self.key("PageDown")
        scrolled = self.capture("error-scrolled")
        assert initial != scrolled and "Details 1-" not in scrolled
        self.key("PageUp")
        self.wait("Details 1-", "error-scroll-back")
        self.key("Escape")
        self.tmux("resize-window", "-t", "app", "-x", "100", "-y", "32")
        self.wait("diagnostic failure", "restored-after-error")
        self.quit()
        self.note("All PTY journeys passed; uncertain stub route retained for inspection, not adopted or rolled back")
        for path in self.config.glob("*.json"):
            shutil.copyfile(path, self.out / path.name)
        for name in ("tailscale-calls.jsonl", "serve.json", "opened-urls", "remote-commands"):
            shutil.copyfile(self.root / name, self.out / name)
        remote = (self.out / "remote-commands").read_text()
        assert "tailscale 'status' '--json'" in remote
        assert "tailscale 'serve' '--bg' '--https=9443' '--set-path=/'" in remote
        assert "'off'" in remote
        assert f"http://127.0.0.1:{self.local}" in (self.out / "opened-urls").read_text()
        assert "https://lab.example.ts.net:8443/" in (self.out / "opened-urls").read_text()
        assert "https://lab.example.ts.net:9443/" in (self.out / "opened-urls").read_text()

    def cleanup(self):
        with contextlib.suppress(FileNotFoundError):
            subprocess.run(["tmux", "-L", self.socket_name, "kill-server"],
                           text=True, capture_output=True)
            remaining = subprocess.run(["tmux", "-L", self.socket_name, "has-session", "-t", "app"],
                                       text=True, capture_output=True)
            assert remaining.returncode != 0, "private tmux session survived cleanup"
        for path in self.config.glob("*.sock"):
            with contextlib.suppress(Exception):
                self.run(["/usr/bin/ssh", "-F", "/dev/null", "-S", path, "-O", "exit", "lab"])
        if hasattr(self, "unrelated"):
            with contextlib.suppress(Exception):
                self.run(["/usr/bin/ssh", "-F", "/dev/null", "-S", self.unrelated, "-O", "exit", "lab"])
        for proc in self.processes:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=5)
        if self.http:
            self.http.shutdown()
            self.http.server_close()
        deadline = time.monotonic() + 5
        while list(self.config.glob("*.sock")) or (self.root / "unrelated.sock").exists():
            if time.monotonic() >= deadline:
                raise AssertionError("SSH control socket survived cleanup; inspect scratch directory")
            time.sleep(0.1)
        self.note("Stopped private tmux server, owned SSH masters, throwaway sshd, and HTTP server")
        self.note("Scratch retained at " + str(self.root) + "; do not reuse it as live configuration")
        self.logfile.close()
        assert (self.out / "actions.log").exists(), "cleanup lost evidence"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    lab = Lab(args.evidence)
    try:
        try:
            lab.setup()
            lab.drive()
        finally:
            lab.cleanup()
    except Exception as error:
        (lab.out / "result.txt").write_text("FAIL: " + repr(error) + "\n")
        raise
    else:
        (lab.out / "result.txt").write_text("PASS: PTY, real loopback SSH, stub Serve, cleanup. Real Tailscale and a graphical browser were not exercised.\n")


if __name__ == "__main__":
    main()
