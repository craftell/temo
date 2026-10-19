#!/usr/bin/env python3
"""Drive the real temo binary's `update` command against a loopback GitHub release imitation."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import pty
import select
import shutil
import socket
import ssl
import stat
import subprocess
import tarfile
import tempfile
import threading
import time
import urllib.parse

TAG = "v0.3.0"
HOSTS = ("api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com")
LIMIT = 64 << 20


class Release:
    """One imitation of the latest GitHub release. Tests mutate fields before a run."""

    def __init__(self, new_binary):
        self.tag = TAG
        self.draft = False
        self.prerelease = False
        self.archive = make_archive([("temo", new_binary), ("LICENSE", b"MIT\n"), ("README.md", b"readme\n")])
        self.checksums = None
        self.size_override = {}
        self.api_status = 200
        self.api_body = None
        self.api_redirect = None
        self.asset_status = {}
        self.redirect_to = {}
        self.body_mode = {}
        self.delay = {}

    @property
    def archive_name(self):
        return "temo_%s_linux_amd64.tar.gz" % self.tag.removeprefix("v")

    def files(self):
        sums = self.checksums
        if sums is None:
            sums = hashlib.sha256(self.archive).hexdigest() + "  " + self.archive_name + "\n"
        return {self.archive_name: self.archive, "checksums.txt": sums.encode()}

    def metadata(self):
        base = "https://github.com/craftell/temo/releases/download/%s/" % self.tag
        assets = [{"name": name, "browser_download_url": base + name,
                   "size": self.size_override.get(name, len(data))} for name, data in self.files().items()]
        return {"tag_name": self.tag, "draft": self.draft, "prerelease": self.prerelease, "assets": assets}


def make_archive(entries, pad=True):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w:gz", format=tarfile.PAX_FORMAT) as tar:
        for entry in entries:
            name, data = entry[0], entry[1]
            kind = entry[2] if len(entry) > 2 else tarfile.REGTYPE
            mode = entry[3] if len(entry) > 3 else 0o755
            info = tarfile.TarInfo(name)
            info.mode, info.type = mode, kind
            if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
                info.linkname = "../outside"
                tar.addfile(info)
            elif kind == tarfile.DIRTYPE:
                tar.addfile(info)
            else:
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
    return out.getvalue()


class Fixture:
    """TLS-terminating CONNECT proxy. The updater trusts it only through SSL_CERT_FILE."""

    def __init__(self, root):
        self.root = root
        self.release = None
        self.log = []
        self.lock = threading.Lock()
        self.make_pki()
        self.context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        self.context.load_cert_chain(root / "leaf.pem", root / "leaf.key")
        self.listener = socket.socket()
        self.listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.listener.bind(("127.0.0.1", 0))
        self.listener.listen(32)
        self.port = self.listener.getsockname()[1]
        self.closing = False
        threading.Thread(target=self.accept, daemon=True).start()

    def make_pki(self):
        def openssl(*args):
            subprocess.run(["openssl", *args], cwd=self.root, check=True, capture_output=True)
        ec = ["-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes"]
        openssl("req", "-x509", *ec, "-keyout", "ca.key", "-out", "ca.pem", "-subj", "/CN=temo-verify-ca", "-days", "2",
                "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign")
        openssl("req", *ec, "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", "/CN=temo-verify-leaf")
        (self.root / "leaf.ext").write_text("subjectAltName=" + ",".join("DNS:" + h for h in HOSTS) + "\n")
        openssl("x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial",
                "-out", "leaf.pem", "-days", "2", "-extfile", "leaf.ext")

    def record(self, **fields):
        with self.lock:
            self.log.append(fields)

    def mark(self):
        with self.lock:
            return len(self.log)

    def since(self, mark):
        with self.lock:
            return list(self.log[mark:])

    def accept(self):
        while not self.closing:
            try:
                conn, _ = self.listener.accept()
            except OSError:
                return
            threading.Thread(target=self.handle, args=(conn,), daemon=True).start()

    def read_head(self, conn):
        data = b""
        while b"\r\n\r\n" not in data:
            chunk = conn.recv(4096)
            if not chunk:
                return None
            data += chunk
        return data.split(b"\r\n\r\n")[0].decode("latin-1")

    def handle(self, conn):
        try:
            conn.settimeout(30)
            head = self.read_head(conn)
            if not head:
                return
            method, target, _ = head.split("\r\n")[0].split(" ")
            if method != "CONNECT":
                self.record(kind="non-connect", method=method, target=target)
                conn.sendall(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
                return
            host = target.rsplit(":", 1)[0]
            if host not in HOSTS:
                self.record(kind="connect-refused", host=host)
                conn.sendall(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
                return
            conn.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            tls = self.context.wrap_socket(conn, server_side=True)
            request = self.read_head(tls)
            if not request:
                return
            lines = request.split("\r\n")
            method, path, _ = lines[0].split(" ")
            headers = {k.lower(): v for k, v in (line.split(": ", 1) for line in lines[1:] if ": " in line)}
            self.record(kind="request", host=host, method=method, path=path, headers=headers)
            self.respond(tls, host, path)
            tls.close()
        except (OSError, ssl.SSLError, ValueError):
            pass
        finally:
            conn.close()

    def send(self, tls, status, headers, body=b"", declared=None):
        lines = ["HTTP/1.1 %d X" % status]
        headers = dict(headers)
        headers.setdefault("Content-Length", str(len(body) if declared is None else declared))
        headers["Connection"] = "close"
        lines += ["%s: %s" % item for item in headers.items()]
        tls.sendall(("\r\n".join(lines) + "\r\n\r\n").encode() + body)

    def respond(self, tls, host, path):
        rel = self.release
        parsed = urllib.parse.urlparse(path)
        if host == "api.github.com":
            if rel.api_redirect:
                return self.send(tls, 302, {"Location": rel.api_redirect})
            body = rel.api_body if rel.api_body is not None else json.dumps(rel.metadata()).encode()
            return self.send(tls, rel.api_status, {"Content-Type": "application/json"}, body)
        files = rel.files()
        if host == "github.com":
            name = parsed.path.rsplit("/", 1)[-1]
            if name in rel.asset_status:
                return self.send(tls, rel.asset_status[name], {})
            location = rel.redirect_to.get(name) or (
                "https://release-assets.githubusercontent.com/github-production-release-asset/1?sig=x&name=" + name)
            return self.send(tls, 302, {"Location": location})
        if host == "release-assets.githubusercontent.com" and "/loop/" in parsed.path:
            return self.send(tls, 302, {"Location": "https://release-assets.githubusercontent.com/loop/" + str(time.time_ns())})
        name = urllib.parse.parse_qs(parsed.query).get("name", [""])[0]
        data = files.get(name)
        if data is None:
            return self.send(tls, 404, {})
        time.sleep(rel.delay.get(name, 0))
        mode = rel.body_mode.get(name)
        if mode == "cut":
            return self.send(tls, 200, {}, data[: len(data) // 2], declared=len(data))
        if mode and mode.startswith("stream:"):
            total = int(mode.split(":")[1])
            tls.sendall(b"HTTP/1.1 200 X\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
            chunk = b"\0" * (1 << 20)
            for _ in range(total >> 20):
                tls.sendall(b"%x\r\n" % len(chunk) + chunk + b"\r\n")
            return tls.sendall(b"0\r\n\r\n")
        return self.send(tls, 200, {"Content-Type": "application/octet-stream"}, data)

    def close(self):
        self.closing = True
        self.listener.close()


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def tree_state(root):
    state = {}
    for path in sorted(Path(root).rglob("*")):
        info = path.lstat()
        digest = sha(path) if stat.S_ISREG(info.st_mode) else (os.readlink(path) if path.is_symlink() else "")
        state[str(path.relative_to(root))] = [oct(info.st_mode), info.st_mtime_ns, digest]
    return state


class UpdateLab:
    def __init__(self, evidence, release_archive=None):
        self.release_archive = release_archive
        self.repo = Path(__file__).resolve().parents[3]
        self.out = evidence.resolve()
        if self.out.is_relative_to(self.repo):
            raise ValueError("evidence must be outside the checkout")
        if self.out.exists() and any(self.out.iterdir()):
            raise ValueError("evidence directory must be empty")
        self.out.mkdir(parents=True, exist_ok=True)
        self.root = Path(tempfile.mkdtemp(prefix="temo-update-")).resolve()
        self.records = []
        self.failures = []

    def build(self, name, version, channel):
        flags = []
        if version:
            flags += ["-X main.version=" + version]
        if channel:
            flags += ["-X main.updateChannel=" + channel]
        target = self.root / "bin" / name
        subprocess.run(["go", "build", "-ldflags", " ".join(flags), "-o", str(target), "."],
                       cwd=self.repo, check=True, capture_output=True)
        return target

    def prepare(self):
        self.root.chmod(0o700)
        (self.root / "bin").mkdir()
        self.old = self.build("temo-0.2.0", "0.2.0", "github-release")
        self.new = self.build("temo-0.3.0", "0.3.0", "github-release")
        self.newer = self.build("temo-0.4.0", "0.4.0", "github-release")
        self.source = self.build("temo-source", "", "")
        self.fixture = Fixture(self.root)
        self.fixture.release = Release(self.new.read_bytes())
        stubs = self.root / "stubs"
        stubs.mkdir()
        for tool in ("ssh", "tailscale", "ssh-keygen"):
            stub = stubs / tool
            stub.write_text('#!/bin/sh\necho "$0 $*" >> "%s"\nexit 99\n' % (self.root / "stub-calls.log"))
            stub.chmod(0o755)
        self.stubs = stubs
        (self.out / "environment.json").write_text(json.dumps({
            "commit": subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.repo, capture_output=True, text=True).stdout.strip(),
            "dirty": bool(subprocess.run(["git", "status", "--porcelain"], cwd=self.repo, capture_output=True, text=True).stdout),
            "scratch": str(self.root), "GOFLAGS": os.environ.get("GOFLAGS", ""),
            "binaries": {p.name: sha(p) for p in sorted((self.root / "bin").iterdir())},
            "publishing": "none; every URL is intercepted by a loopback proxy and no tag or release exists",
        }, indent=2))

    def sandbox(self, name, binary=None, populate_home=True):
        base = self.root / name
        install, home = base / "install", base / "home"
        install.mkdir(parents=True)
        home.mkdir()
        target = install / "temo"
        shutil.copyfile(binary or self.old, target)
        target.chmod(0o755)
        if populate_home:
            (home / ".config" / "temo").mkdir(parents=True)
            (home / ".config" / "temo" / "config.json").write_text('{"sentinel": true}\n')
            (home / ".config" / "temo" / "ownership.json").write_text('{"sentinel": "ownership"}\n')
            (home / ".ssh").mkdir()
            (home / ".ssh" / "config").write_text("Host sentinel\n")
        return install, home

    def env(self, home, proxy=True, trust=True):
        env = {"PATH": str(self.stubs) + ":/usr/bin:/bin", "HOME": str(home), "XDG_CONFIG_HOME": str(home / ".config"),
               "TERM": "xterm", "NO_PROXY": "", "no_proxy": ""}
        url = "http://127.0.0.1:%d" % (self.fixture.port if proxy else 9)
        env.update(HTTPS_PROXY=url, https_proxy=url)
        if trust:
            env["SSL_CERT_FILE"] = str(self.root / "ca.pem")
        return env

    def scenario(self, name, args, *, binary=None, version_binary=None, expect_rc=0, out_has=(), err_has=(),
                 changed=False, new_sha=None, request_paths=None, setup=None, proxy=True,
                 trust=True, populate_home=True, release=None, extra_leftovers=(), after=None):
        install, home = self.sandbox(name, binary, populate_home)
        if setup:
            setup(install, home)
        self.fixture.release = release or Release(self.new.read_bytes())
        exe = install / "temo"
        base_listing = sorted(p.name for p in install.parent.iterdir())
        before_sha = sha(exe) if exe.exists() and not exe.is_symlink() else None
        before_home = tree_state(home)
        stub_before = (self.root / "stub-calls.log").read_text() if (self.root / "stub-calls.log").exists() else ""
        mark = self.fixture.mark()
        started = time.time()
        proc = subprocess.run([str(exe), *args], env=self.env(home, proxy, trust), cwd=str(home),
                              capture_output=True, text=True, timeout=180)
        elapsed = round(time.time() - started, 2)
        events = self.fixture.since(mark)
        paths = [e["host"] + e.get("path", "").split("?")[0] for e in events if e["kind"] == "request"]
        refused = [e["host"] for e in events if e["kind"] == "connect-refused"]
        stub_after = (self.root / "stub-calls.log").read_text() if (self.root / "stub-calls.log").exists() else ""
        problems = []
        if proc.returncode != expect_rc:
            problems.append("exit %d, expected %d" % (proc.returncode, expect_rc))
        for text in out_has:
            if text not in proc.stdout:
                problems.append("stdout lacks %r" % text)
        for text in err_has:
            if text not in proc.stderr:
                problems.append("stderr lacks %r" % text)
        after_sha = sha(exe) if exe.exists() and not exe.is_symlink() else None
        if changed:
            if after_sha != (new_sha or sha(self.new)):
                problems.append("installed executable is not the release binary")
        elif before_sha != after_sha:
            problems.append("installed executable changed")
        if tree_state(home) != before_home:
            problems.append("HOME (config, ownership, SSH sentinels) changed")
        if stub_after != stub_before:
            problems.append("ssh/tailscale stub was invoked")
        siblings = sorted(p.name for p in install.parent.iterdir())
        if siblings != base_listing:
            problems.append("files appeared beside the install directory: %s" % siblings)
        if Path("/tmp/temo-evil").exists():
            problems.append("an archive member escaped to an absolute path")
        leftovers = sorted(p.name for p in install.iterdir() if p.name not in ("temo", "temo.update.lock", *extra_leftovers))
        if leftovers:
            problems.append("stray files beside the executable: %s" % leftovers)
        if request_paths is not None and paths != request_paths:
            problems.append("requests %s, expected %s" % (paths, request_paths))
        for host in refused:
            problems.append("updater tried to contact the disallowed host " + host)
        if any(e["kind"] == "non-connect" for e in events):
            problems.append("updater sent a plain HTTP request")
        for pattern in ("Authorization", "Cookie"):
            if any(pattern.lower() in e.get("headers", {}) for e in events):
                problems.append(pattern + " header sent")
        if after:
            problems += after(install, home, proc) or []
        record = {"name": name, "args": args, "exit": proc.returncode, "seconds": elapsed,
                  "stdout": proc.stdout, "stderr": proc.stderr, "requests": paths, "refused_connects": refused,
                  "before_sha256": before_sha, "after_sha256": after_sha, "problems": problems}
        self.records.append(record)
        print(("PASS " if not problems else "FAIL ") + name, flush=True)
        for problem in problems:
            print("   ", problem, flush=True)
            self.failures.append("%s: %s" % (name, problem))
        return proc

    def drive(self):
        good_bytes = self.new.read_bytes()
        api = "api.github.com/repos/craftell/temo/releases/latest"
        archive_name = Release(b"x").archive_name
        asset = "github.com/craftell/temo/releases/download/%s/" % TAG
        cdn = "release-assets.githubusercontent.com/github-production-release-asset/1"
        full = [api, asset + "checksums.txt", cdn, asset + archive_name, cdn]

        self.scenario("check-newer", ["update", "--check"], out_has=["Update available: 0.2.0 → v0.3.0."],
                      request_paths=[api],
                      after=lambda i, h, p: ["check created a lock file"] if (i / "temo.update.lock").exists() else None)
        self.scenario("check-equal", ["update", "--check"], binary=self.new, out_has=["up to date"], request_paths=[api])
        self.scenario("check-source-build", ["update", "--check"], binary=self.source, out_has=["Current build:", "v0.3.0"],
                      request_paths=[api])
        self.scenario("update-success", ["update"], out_has=["Updated temo 0.2.0 → v0.3.0."], changed=True,
                      request_paths=full, after=self.after_success)
        if self.release_archive:
            with tarfile.open(self.release_archive) as tar:
                packaged = tar.extractfile("temo").read()
            real = Release(good_bytes)
            real.archive = self.release_archive.read_bytes()
            self.scenario("update-goreleaser-snapshot-archive", ["update"], out_has=["Updated temo 0.2.0 → v0.3.0."],
                          changed=True, new_sha=hashlib.sha256(packaged).hexdigest(), release=real, request_paths=full)
        self.scenario("update-no-downgrade", ["update"], binary=self.newer, out_has=["up to date"], request_paths=[api])
        self.scenario("update-already-current", ["update"], binary=self.new, out_has=["up to date"], request_paths=[api])

        self.scenario("source-build-refused", ["update"], binary=self.source, expect_rc=1,
                      err_has=["official GitHub release build"], request_paths=[])

        def release(**fields):
            r = Release(self.new.read_bytes())
            for key, value in fields.items():
                setattr(r, key, value)
            return r

        def archive_release(entries, **fields):
            r = release(**fields)
            r.archive = make_archive(entries)
            return r

        good = self.new.read_bytes()
        for name, kwargs, message in (
            ("prerelease-flag", {"prerelease": True}, "stable release"),
            ("draft-flag", {"draft": True}, "stable release"),
            ("prerelease-tag", {"tag": "v0.3.0-rc.1"}, "stable release"),
        ):
            self.scenario("unstable-" + name, ["update"], expect_rc=1, err_has=[message], release=release(**kwargs),
                          request_paths=[api])

        self.scenario("api-http-403", ["update"], expect_rc=1, err_has=["HTTP 403"], release=release(api_status=403), request_paths=[api])
        self.scenario("api-bad-json", ["update"], expect_rc=1, err_has=["invalid GitHub release metadata"],
                      release=release(api_body=b"{not json"), request_paths=[api])
        self.scenario("api-oversize", ["update"], expect_rc=1, err_has=["exceeds size limit"],
                      release=release(api_body=b" " * ((1 << 20) + 10)), request_paths=[api])
        self.scenario("api-redirect-offsite", ["update"], expect_rc=1, err_has=["update download failed"],
                      release=release(api_redirect="https://evil.example/steal"), request_paths=[api])

        r = release()
        r.checksums = "0" * 64 + "  " + r.archive_name + "\n"
        self.scenario("checksum-mismatch", ["update"], expect_rc=1, err_has=["does not match"], release=r)
        for name, text, message in (
            ("checksum-missing-entry", "%s  other.tar.gz\n" % ("0" * 64), "checksum not found"),
            ("checksum-malformed", "nothex  %s\n" % Release(b"x").archive_name, "invalid or duplicate"),
            ("checksum-extra-field", "%s  a b\n" % ("0" * 64), "invalid release checksums"),
        ):
            r = release()
            r.checksums = text
            self.scenario(name, ["update"], expect_rc=1, err_has=[message], release=r)
        r = release()
        line = hashlib.sha256(r.archive).hexdigest() + "  " + r.archive_name + "\n"
        r.checksums = line + line
        self.scenario("checksum-duplicate", ["update"], expect_rc=1, err_has=["invalid or duplicate"], release=r)

        r = release(body_mode={archive_name: "cut"})
        self.scenario("download-interrupted", ["update"], expect_rc=1,
                      err_has=["could not read update download"], release=r)
        r2 = release()
        r2.archive = r2.archive[: len(r2.archive) // 2]
        self.scenario("archive-truncated-consistent-checksum", ["update"], expect_rc=1,
                      err_has=["invalid or oversized release archive"], release=r2)
        r = release(size_override={archive_name: len(release().archive) + 5})
        self.scenario("archive-size-differs-from-metadata", ["update"], expect_rc=1, err_has=["does not match"], release=r)

        hostile = (
            ("traversal", [("../temo", b"evil")], "unexpected or unsafe entry"),
            ("absolute-path", [("/tmp/temo-evil", b"evil")], "unexpected or unsafe entry"),
            ("nested-path", [("dist/temo", b"evil")], "unexpected or unsafe entry"),
            ("symlink-member", [("temo", b"", tarfile.SYMTYPE)], "unexpected or unsafe entry"),
            ("hardlink-member", [("temo", b"", tarfile.LNKTYPE)], "unexpected or unsafe entry"),
            ("directory-member", [("temo/", b"", tarfile.DIRTYPE)], "unexpected or unsafe entry"),
            ("extra-member", [("temo", good), ("evil.sh", b"#!/bin/sh\n")], "unexpected or unsafe entry"),
            ("duplicate-binary", [("temo", good), ("temo", b"second")], "unexpected or unsafe entry"),
            ("not-executable", [("temo", good, tarfile.REGTYPE, 0o644)], "not executable"),
            ("empty-binary", [("temo", b"")], "empty or not executable"),
            ("no-binary", [("LICENSE", b"MIT\n")], "no temo executable"),
        )
        for name, entries, message in hostile:
            self.scenario("hostile-" + name, ["update"], expect_rc=1, err_has=[message],
                          release=archive_release(entries))
        self.scenario("hostile-not-gzip", ["update"], expect_rc=1, err_has=["invalid release gzip archive"],
                      release=release(archive=b"plain text, not gzip"))
        self.scenario("hostile-trailing-data", ["update"], expect_rc=1, err_has=["trailing data"],
                      release=release(archive=self.with_trailing(make_archive([("temo", good)]))))

        bomb = archive_release([("temo", b"\0" * (60 << 20)), ("LICENSE", b"\0" * (30 << 20))])
        self.scenario("oversize-expanded-archive", ["update"], expect_rc=1, err_has=["invalid or oversized release archive"], release=bomb)
        self.scenario("oversize-declared-asset", ["update"], expect_rc=1, err_has=["missing or invalid official release asset"],
                      release=release(size_override={archive_name: LIMIT + 1}), request_paths=[api])
        r = release(size_override={archive_name: 1 << 20}, body_mode={archive_name: "stream:%d" % (((LIMIT >> 20) + 2) << 20)})
        self.scenario("oversize-streamed-download", ["update"], expect_rc=1, err_has=["exceeds size limit"], release=r)

        self.scenario("redirect-disallowed-host", ["update"], expect_rc=1, err_has=["update download failed"],
                      release=release(redirect_to={archive_name: "https://evil.example/temo.tar.gz"}),
                      request_paths=[api, asset + "checksums.txt", cdn, asset + archive_name])
        self.scenario("redirect-http-downgrade", ["update"], expect_rc=1, err_has=["update download failed"],
                      release=release(redirect_to={archive_name: "http://release-assets.githubusercontent.com/x?name=" + archive_name}),
                      request_paths=[api, asset + "checksums.txt", cdn, asset + archive_name])
        self.scenario("redirect-loop", ["update"], expect_rc=1, err_has=["update download failed"],
                      release=release(redirect_to={archive_name: "https://release-assets.githubusercontent.com/loop/0"}))
        self.scenario("asset-http-404", ["update"], expect_rc=1, err_has=["HTTP 404"],
                      release=release(asset_status={archive_name: 404}))

        self.scenario("download-offline", ["update"], expect_rc=1, err_has=["update download failed; check network access"],
                      proxy=False, trust=False)
        self.scenario("download-untrusted-certificate", ["update"], expect_rc=1, err_has=["update download failed"], trust=False)
        self.scenario("check-offline", ["update", "--check"], expect_rc=1, err_has=["update download failed"], proxy=False, trust=False)

        self.scenario("install-dir-read-only", ["update"], expect_rc=1 if os.geteuid() else 0,
                      err_has=["cannot create update lock"] if os.geteuid() else [],
                      setup=lambda i, h: i.chmod(0o555), changed=not os.geteuid(),
                      after=lambda i, h, p: (i.chmod(0o755), None)[1])

        def link_setup(install, home):
            real = install.parent / "real"
            real.mkdir()
            shutil.move(str(install / "temo"), str(real / "temo"))
            (install / "temo").symlink_to(real / "temo")
            self.symlink_real = real / "temo"

        def link_after(install, home, proc):
            problems = []
            if not (install / "temo").is_symlink():
                problems.append("the launching symlink was replaced")
            if sha(self.symlink_real) != sha(self.new):
                problems.append("the symlink's regular target was not updated")
            return problems

        self.scenario("symlinked-binary-linux-resolves-to-target", ["update"], out_has=["Updated temo 0.2.0 → v0.3.0."],
                      setup=link_setup, after=link_after, request_paths=full)

        def package_setup(marker):
            def setup(install, home):
                real = install.parent / marker / "temo" / "0.2.0" / "bin"
                real.mkdir(parents=True)
                shutil.move(str(install / "temo"), str(real / "temo"))
                (install / "temo").symlink_to(real / "temo")
            return setup

        for marker in ("Cellar", "nix/store", "snap"):
            self.scenario("package-managed-" + marker.replace("/", "-"), ["update"], expect_rc=1,
                          err_has=["package-managed executable"], setup=package_setup(marker), request_paths=[],
                          after=lambda i, h, p: [] if (i / "temo").is_symlink() else ["link replaced"])
        self.concurrent()
        self.terminal()

    @staticmethod
    def after_success(install, home, proc):
        problems = []
        mode = stat.S_IMODE((install / "temo").stat().st_mode)
        if mode != 0o755:
            problems.append("mode %o, expected 755" % mode)
        version = subprocess.run([str(install / "temo"), "--version"], capture_output=True, text=True, env={"PATH": "/usr/bin:/bin"})
        if version.stdout != "temo 0.3.0\n":
            problems.append("installed binary reports %r" % version.stdout)
        return problems

    @staticmethod
    def with_trailing(archive):
        import gzip
        raw = gzip.decompress(archive)
        return gzip.compress(raw + b"\0" * 10 + b"garbage")

    def concurrent(self):
        install, home = self.sandbox("concurrent")
        release = Release(self.new.read_bytes())
        release.delay = {release.archive_name: 4}
        self.fixture.release = release
        exe, env = install / "temo", self.env(home)
        first = subprocess.Popen([str(exe), "update"], env=env, cwd=str(home), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        time.sleep(1.5)
        second = subprocess.run([str(exe), "update"], env=env, cwd=str(home), capture_output=True, text=True, timeout=60)
        out, err = first.communicate(timeout=60)
        problems = []
        if second.returncode != 1 or "another update is running" not in second.stderr:
            problems.append("second updater: exit %d %r" % (second.returncode, second.stderr))
        if first.returncode != 0 or "Updated temo" not in out:
            problems.append("first updater: exit %d %r %r" % (first.returncode, out, err))
        if sha(exe) != sha(self.new):
            problems.append("final executable is not the release binary")
        self.finish("concurrent-updaters", problems, {"first": out + err, "second": second.stdout + second.stderr})

    def finish(self, name, problems, details):
        self.records.append({"name": name, "problems": problems, **details})
        print(("PASS " if not problems else "FAIL ") + name, flush=True)
        for problem in problems:
            print("   ", problem, flush=True)
            self.failures.append("%s: %s" % (name, problem))

    def pty_run(self, args, env, cwd, raw_name):
        pid, fd = pty.fork()
        if pid == 0:
            os.chdir(cwd)
            os.execve(args[0], args, env)
        raw = b""
        deadline = time.time() + 60
        while time.time() < deadline:
            ready, _, _ = select.select([fd], [], [], 1)
            if ready:
                try:
                    chunk = os.read(fd, 4096)
                except OSError:
                    break
                if not chunk:
                    break
                raw += chunk
        _, status = os.waitpid(pid, 0)
        (self.out / raw_name).write_bytes(raw)
        return raw.decode("utf-8", "replace"), os.waitstatus_to_exitcode(status)

    def terminal(self):
        install, home = self.sandbox("terminal")
        self.fixture.release = Release(self.new.read_bytes())
        env, exe = self.env(home), str(install / "temo")
        problems = []
        before = tree_state(home)
        for label, args, expected in (
            ("version", [exe, "--version"], "temo 0.2.0\r\n"),
            ("short-version", [exe, "-v"], "temo 0.2.0\r\n"),
            ("check", [exe, "update", "--check"], "Update available: 0.2.0 → v0.3.0.\r\nRun temo update to install.\r\n"),
            ("help", [exe, "update", "--help"], "check for a stable release without changing files"),
        ):
            text, code = self.pty_run(args, env, str(home), "terminal-" + label + ".raw")
            if expected not in text:
                problems.append("%s printed %r" % (label, text))
            if code != 0:
                problems.append("%s exit %d" % (label, code))
            if "\x1b[?1049" in text or "\x1b[?25l" in text or "\x1b[2J" in text:
                problems.append("%s emitted full-screen terminal control sequences" % label)
        if sha(install / "temo") != sha(self.old):
            problems.append("terminal check changed the executable")
        if tree_state(home) != before:
            problems.append("terminal runs changed HOME")
        self.finish("real-terminal-version-and-check", problems, {})

    def report(self):
        (self.out / "scenarios.json").write_text(json.dumps(self.records, indent=2, ensure_ascii=False))
        with (self.out / "requests.jsonl").open("w") as log:
            for event in self.fixture.log:
                event = dict(event)
                event.pop("headers", None)
                log.write(json.dumps(event) + "\n")
        (self.out / "commands.txt").write_text(
            "\n".join("%s: %s -> exit %s" % (r["name"], " ".join(r.get("args", [])), r.get("exit", "n/a")) for r in self.records) + "\n")

    def cleanup(self):
        self.fixture.close()
        (self.out / "cleanup.txt").write_text(
            "Loopback proxy stopped. Scratch binaries, fixtures, and installs retained at " + str(self.root) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--release-archive", type=Path,
                        help="a GoReleaser linux/amd64 snapshot archive from verify-release.py to install as the release asset")
    args = parser.parse_args()
    lab = UpdateLab(args.evidence, args.release_archive.resolve() if args.release_archive else None)
    try:
        try:
            lab.prepare()
            lab.drive()
        finally:
            if hasattr(lab, "fixture"):
                lab.report()
                lab.cleanup()
    except Exception as error:
        (lab.out / "result.txt").write_text("FAIL: " + repr(error) + "\n")
        raise
    if lab.failures:
        (lab.out / "result.txt").write_text("FAIL:\n" + "\n".join(lab.failures) + "\n")
        raise SystemExit(1)
    (lab.out / "result.txt").write_text(
        "PASS %d update scenarios against the real binary through a loopback TLS proxy. Nothing was published, and no member or temporary file appeared outside each scratch install directory.\n" % len(lab.records))
    print("PASS", len(lab.records), "scenarios; evidence in", lab.out)


if __name__ == "__main__":
    main()
