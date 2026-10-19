#!/usr/bin/env python3
"""Build a local snapshot and test the unmodified installer on loopback HTTP."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import stat
import subprocess
import tarfile
import tempfile
import threading


class ReleaseLab:
    def __init__(self, evidence, goreleaser, shellcheck):
        self.repo = Path(__file__).resolve().parents[3]
        self.out = evidence.resolve()
        if self.out.is_relative_to(self.repo):
            raise ValueError("evidence must be outside the checkout")
        if self.out.exists() and any(self.out.iterdir()):
            raise ValueError("evidence directory must be empty")
        self.out.mkdir(parents=True, exist_ok=True)
        self.root = Path(tempfile.mkdtemp(prefix="temo-release-"))
        self.goreleaser = goreleaser
        self.shellcheck = shellcheck
        self.requests = []
        self.bad_checksum = False
        self.latest_allowed = True
        self.server = None
        self.build_env = dict(os.environ)
        for name in ("GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITEA_TOKEN"):
            self.build_env.pop(name, None)

    def run(self, args, label, env=None, failure=False):
        result = subprocess.run([str(a) for a in args], cwd=self.repo,
                                env=env or self.build_env, text=True, capture_output=True)
        (self.out / (label + ".stdout.txt")).write_text(result.stdout)
        (self.out / (label + ".stderr.txt")).write_text(result.stderr)
        with (self.out / "commands.jsonl").open("a") as log:
            log.write(json.dumps({"label": label, "args": [str(a) for a in args],
                                  "exit": result.returncode}) + "\n")
        print(label, "exit", result.returncode, flush=True)
        if failure:
            assert result.returncode != 0, label + " unexpectedly succeeded"
        else:
            result.check_returncode()
        return result

    def build(self):
        assert self.goreleaser, "provide --goreleaser or install it in a scratch directory"
        self.run([self.goreleaser, "--version"], "goreleaser-version")
        self.run([self.goreleaser, "check"], "goreleaser-check")
        tags = self.run(["git", "for-each-ref", "refs/tags", "--format=%(refname) %(objectname)"], "tags-before").stdout
        self.run(["/bin/sh", "-n", "install.sh"], "shell-syntax")
        if self.shellcheck:
            self.run([self.shellcheck, "install.sh"], "shellcheck")
        self.run(["go", "build", "-o", self.root / "temo-dev", "."], "build-dev")
        no_home = dict(self.build_env, HOME="", XDG_CONFIG_HOME="")
        for flag in ("--version", "-v"):
            result = self.run([self.root / "temo-dev", flag], "dev-version-" + flag.lstrip("-"), env=no_home)
            assert result.stdout.startswith("temo dev") and not result.stderr
        config = (self.repo / ".goreleaser.yaml").read_text()
        snapshot_config = self.root / "snapshot.yaml"
        self.dist = self.root / "dist"
        assert not self.dist.exists()
        snapshot_config.write_text(config + "\ndist: " + json.dumps(str(self.dist)) + "\n")
        shutil.copyfile(snapshot_config, self.out / "snapshot-config.yaml")
        self.run([self.goreleaser, "release", "--snapshot", "--clean", "--config", snapshot_config,
                  "--parallelism", "2", "--timeout", "5m"], "snapshot")
        assert self.run(["git", "for-each-ref", "refs/tags", "--format=%(refname) %(objectname)"], "tags-after").stdout == tags
        archives = sorted(self.dist.glob("temo_*_linux_amd64.tar.gz"))
        assert len(archives) == 1, "missing predictable Linux amd64 archive"
        self.version = archives[0].name.removeprefix("temo_").removesuffix("_linux_amd64.tar.gz")
        expected = {f"temo_{self.version}_{system}_{arch}.tar.gz"
                    for system in ("linux", "darwin") for arch in ("amd64", "arm64")}
        sums = (self.dist / "checksums.txt").read_text()
        self.sums = dict((line.split()[1], line.split()[0]) for line in sums.splitlines())
        assert set(self.sums) == expected, "snapshot artifact matrix differs from release contract"
        for name in expected:
            assert hashlib.sha256((self.dist / name).read_bytes()).hexdigest() == self.sums[name]
            with tarfile.open(self.dist / name) as archive:
                assert sorted(archive.getnames()) == ["LICENSE", "README.md", "temo"], "archive layout differs from the updater contract"
                assert all(member.isreg() for member in archive.getmembers()), "archive has a non-regular member"
                assert archive.getmember("temo").mode & 0o111, "archive binary is not executable"
                assert b"Copyright (c) 2026 kenfdev" in archive.extractfile("LICENSE").read()
        system = {"Linux": "linux", "Darwin": "darwin"}[platform.system()]
        arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
        self.archive = f"temo_{self.version}_{system}_{arch}.tar.gz"
        with tarfile.open(self.dist / self.archive) as archive:
            self.binary = archive.extractfile("temo").read()
        (self.out / "checksums.txt").write_text(sums)
        meta = {"commit": self.run(["git", "rev-parse", "HEAD"], "git-head").stdout.strip(),
                "dirty": bool(self.run(["git", "status", "--porcelain"], "git-status").stdout),
                "scratch": str(self.root), "snapshot_version": self.version,
                "native_archive": self.archive, "GOFLAGS": os.environ.get("GOFLAGS", ""),
                "shellcheck": self.shellcheck or "not installed",
                "publishing": "snapshot only; no tag or release created"}
        (self.out / "environment.json").write_text(json.dumps(meta, indent=2))

    def serve(self):
        lab = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def respond(self, head):
                lab.requests.append({"method": self.command, "path": self.path})
                if self.path == "/releases/latest" and lab.latest_allowed:
                    self.send_response(302)
                    self.send_header("Location", "/releases/tag/v" + lab.version)
                    self.end_headers()
                    return
                if self.path == "/releases/tag/v" + lab.version:
                    self.send_response(200)
                    self.end_headers()
                    return
                prefix = "/releases/download/v" + lab.version + "/"
                if not self.path.startswith(prefix):
                    self.send_error(404)
                    return
                name = self.path[len(prefix):]
                if name == "checksums.txt":
                    value = (lab.dist / name).read_bytes()
                    if lab.bad_checksum:
                        value = value.replace(lab.sums[lab.archive].encode(), b"0" * 64)
                elif name in lab.sums:
                    value = (lab.dist / name).read_bytes()
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header("Content-Length", str(len(value)))
                self.end_headers()
                if not head:
                    self.wfile.write(value)

            def do_HEAD(self):
                self.respond(True)

            def do_GET(self):
                self.respond(False)

            def log_message(self, *args):
                pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.base = f"http://127.0.0.1:{self.server.server_port}/releases"

    def install(self, label, env, failure=False):
        result = self.run(["/bin/sh", self.repo / "install.sh"], label, env=env, failure=failure)
        assert not list((self.root / "tmp").iterdir()), label + " left temporary download files"
        return result

    def drive(self):
        home = self.root / "home"
        home.mkdir(mode=0o700)
        (self.root / "tmp").mkdir(mode=0o700)
        profile = home / ".profile"
        profile.write_text("# untouched verification profile\n")
        env = dict(self.build_env, HOME=str(home), TMPDIR=str(self.root / "tmp"),
                   TEMO_RELEASE_BASE_URL=self.base)
        env.pop("TEMO_INSTALL_DIR", None)
        env.pop("TEMO_VERSION", None)
        default = home / ".local" / "bin"
        result = self.install("install-latest", env)
        assert "Add this directory to PATH:" in result.stdout
        assert (default / "temo").read_bytes() == self.binary
        assert stat.S_IMODE((default / "temo").stat().st_mode) == 0o755
        for flag in ("--version", "-v"):
            result = self.run([default / "temo", flag], "installed-" + flag.lstrip("-"), env=env)
            assert result.stdout.strip() == "temo " + self.version
        stamp = self.run(["go", "version", "-m", default / "temo"], "installed-build-info").stdout
        assert "-X main.version=" + self.version in stamp, "release binary lacks the tag-derived version"
        assert "-X main.updateChannel=github-release" in stamp, "release binary lacks the official release channel"
        dev_stamp = self.run(["go", "version", "-m", self.root / "temo-dev"], "dev-build-info").stdout
        assert "main.updateChannel" not in dev_stamp, "source build inherited the release channel"
        (self.out / "release-build-info.txt").write_text(stamp)
        custom = self.root / "custom bin"
        explicit = dict(env, TEMO_VERSION=self.version, TEMO_INSTALL_DIR=str(custom),
                        PATH=str(custom) + os.pathsep + env["PATH"])
        self.latest_allowed = False
        result = self.install("no-published-release", env, failure=True)
        assert "could not resolve the latest release" in result.stderr
        assert (default / "temo").read_bytes() == self.binary
        before = len(self.requests)
        result = self.install("install-explicit-version", explicit)
        assert not any(r["path"].endswith("/latest") for r in self.requests[before:])
        assert "Add this directory to PATH:" not in result.stdout
        assert (custom / "temo").read_bytes() == self.binary
        self.bad_checksum = True
        result = self.install("wrong-checksum-existing", explicit, failure=True)
        assert "checksum mismatch" in result.stderr
        assert (custom / "temo").read_bytes() == self.binary
        absent = self.root / "checksum-must-not-create"
        result = self.install("wrong-checksum-new", dict(explicit, TEMO_INSTALL_DIR=str(absent)), failure=True)
        assert "checksum mismatch" in result.stderr and not absent.exists()
        self.bad_checksum = False
        fake_bin = self.root / "fake-bin"
        fake_bin.mkdir()
        for label, system, machine, message in [
            ("unsupported-os", "Plan9", "x86_64", "unsupported operating system"),
            ("unsupported-arch", "Linux", "riscv64", "unsupported CPU architecture")]:
            uname = fake_bin / "uname"
            uname.write_text("#!/bin/sh\ncase \"$1\" in\n-s) printf '%s\\n' " + shlex.quote(system) +
                             ";;\n-m) printf '%s\\n' " + shlex.quote(machine) + ";;\nesac\n")
            uname.chmod(0o700)
            before = len(self.requests)
            target = self.root / label
            result = self.install(label, dict(explicit, PATH=str(fake_bin) + os.pathsep + env["PATH"],
                                              TEMO_INSTALL_DIR=str(target)), failure=True)
            assert message in result.stderr and not target.exists()
            assert len(self.requests) == before, "unsupported platform made a download request"
        result = self.install("invalid-version", dict(explicit, TEMO_VERSION="../invalid"), failure=True)
        assert "invalid version" in result.stderr
        result = self.install("missing-release", dict(explicit, TEMO_VERSION="v999.0.0"), failure=True)
        assert "could not download" in result.stderr
        if shutil.which("shasum"):
            hash_bin = self.root / "shasum-bin"
            hash_bin.mkdir()
            for name in ("curl", "tar", "awk", "mktemp", "shasum", "uname", "tr", "mkdir", "rmdir", "rm", "cp", "chmod", "mv", "gzip"):
                path = shutil.which(name)
                assert path, "missing fixture utility " + name
                (hash_bin / name).symlink_to(path)
            alternate = self.root / "shasum install"
            result = self.install("install-shasum", dict(explicit, PATH=str(hash_bin),
                                                       TEMO_INSTALL_DIR=str(alternate), TEMO_VERSION="v" + self.version))
            assert (alternate / "temo").read_bytes() == self.binary
        assert profile.read_text() == "# untouched verification profile\n"
        assert not (home / ".ssh").exists(), "release verification touched SSH state"
        (self.out / "http-requests.json").write_text(json.dumps(self.requests, indent=2))
        (self.out / "installed-version.txt").write_text("temo " + self.version + "\n")

    def cleanup(self):
        if self.server:
            self.server.shutdown()
            self.server.server_close()
        (self.out / "cleanup.txt").write_text("Loopback HTTP fixture stopped. Scratch snapshot and installations retained at " + str(self.root) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--goreleaser", default=shutil.which("goreleaser"))
    parser.add_argument("--shellcheck", default=shutil.which("shellcheck"))
    args = parser.parse_args()
    lab = ReleaseLab(args.evidence, args.goreleaser, args.shellcheck)
    try:
        try:
            lab.build()
            lab.serve()
            lab.drive()
        finally:
            lab.cleanup()
    except Exception as error:
        (lab.out / "result.txt").write_text("FAIL: " + repr(error) + "\n")
        raise
    else:
        (lab.out / "result.txt").write_text("PASS snapshot matrix, checksums, version flags, stamped release channel, installer successes/refusals, profile preservation, and HTTP cleanup. Nothing published.\n")


if __name__ == "__main__":
    main()
