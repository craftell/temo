#!/usr/bin/env python3
"""Run temo in a private tmux server with loopback SSH and fake Tailscale."""
import argparse
import contextlib
import http.server
import json
import os
from pathlib import Path
import pwd
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
    if mode == "slow":
        time.sleep(1.5)
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
        self.config = self.root / "config"
        self.config.mkdir(mode=0o700)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.socket_name = "temo-verify-" + str(os.getpid())
        self.env = dict(os.environ, TEMO_LAB=str(self.root),
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
            if text in screen and "Working:" not in screen:
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
        client = self.root / "ssh_config"
        client.write_text(f"""Host lab inherited
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
                              "TEMO_LAB=" + str(self.root), str(self.root / "temo"),
                              "--config-dir", str(self.config)])
        self.tmux("new-session", "-d", "-s", "app", "-x", "100", "-y", "32", command)
        self.tmux("set-option", "-g", "remain-on-exit", "on")
        self.wait("temo | SSH", "launch")
        self.note("doctor pane pid " + self.tmux("display-message", "-p", "-t", "app", "#{pane_pid}").stdout.strip())

    def quit(self):
        self.key("q")
        deadline = time.monotonic() + 5
        while self.tmux("display-message", "-p", "-t", "app", "#{pane_dead}").stdout.strip() != "1":
            assert time.monotonic() < deadline, "TUI did not exit"
            time.sleep(0.1)
        self.tmux("kill-session", "-t", "app")

    def add(self, name, kind="ssh", host="lab", local=None, serve=8443):
        self.key("a")
        self.wait("Add route", "add-form")
        values = [name, kind, host, "127.0.0.1", str(self.target), str(local or self.local), str(serve)]
        for value in values:
            self.key("C-u")
            if value:
                self.text(value)
            self.key("Tab")
        self.capture("filled-form")
        self.key("C-s")
        self.wait("Saved " + name, "saved")

    def select(self, name):
        self.key("Escape", "/", "C-u")
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

    def drive(self):
        self.wait("No matching routes", "empty")
        locked = subprocess.run([str(self.root / "temo"), "--config-dir", str(self.config)], text=True, capture_output=True)
        assert locked.returncode != 0 and "another temo process" in locked.stderr
        (self.out / "one-writer.txt").write_text(locked.stderr)
        self.key("a", "C-s")
        self.wait("Error (Ctrl+D): name", "validation-error")
        self.action("C-d", "Form error", "form-error-details")
        self.key("Escape", "Escape")
        self.add("local-q-ssh")
        cfgpath = self.config / "config.json"
        assert cfgpath.stat().st_mode & 0o777 == 0o600
        self.action("e", "Edit route", "edit")
        self.key("C-u")
        self.text("local-ssh")
        self.key("C-s")
        self.wait("Saved local-ssh", "edited")
        saved = json.loads(cfgpath.read_text())["entries"][0]
        assert saved["name"] == "local-ssh" and saved["kind"] == "ssh"
        assert saved["ssh_host"] == "lab" and saved["target_host"] == "127.0.0.1"
        assert saved["target_port"] == self.target and saved["local_port"] == self.local
        self.key("/")
        self.text("does-not-match")
        self.wait("0 of 1 routes", "search-empty")
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
        self.add("inherited-ssh", host="inherited", local=free_port())
        self.action("s", "inherits forwarding rules", "inherited-refused")
        self.add("local-serve", kind="serve", host="")
        self.action("r", "State: stopped", "serve-stopped")
        (self.root / "mode").write_text("slow")
        before = self.mutation_count()
        self.key("r", "s", "q")
        busy = self.capture("busy-quit-refused")
        assert "Wait for the current operation before quitting" in busy
        self.wait("State: stopped", "busy-completed")
        assert self.mutation_count() == before
        (self.root / "mode").write_text("normal")
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
