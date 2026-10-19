#!/bin/sh
set -eu

fail() {
    printf 'temo: %s\n' "$*" >&2
    exit 1
}

for tool in curl tar awk mktemp; do
    command -v "$tool" >/dev/null 2>&1 || fail "required command not found: $tool"
done
if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool=shasum
else
    fail "SHA-256 verification requires sha256sum or shasum"
fi

system="$(uname -s)" || fail "could not detect the operating system"
case "$system" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "unsupported operating system: $system (use Linux or macOS)" ;;
esac
machine="$(uname -m)" || fail "could not detect the CPU architecture"
case "$machine" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "unsupported CPU architecture: $machine (use amd64 or arm64)" ;;
esac

release_base="${TEMO_RELEASE_BASE_URL:-https://github.com/craftell/temo/releases}"
release_base="${release_base%/}"
requested="${TEMO_VERSION:-}"
if [ -z "$requested" ]; then
    resolved="$(curl -fsSLI --output /dev/null --write-out '%{url_effective}' "$release_base/latest")" ||
        fail "could not resolve the latest release; a published release is required"
    case "$resolved" in
        "$release_base"/tag/*) requested="${resolved##*/}" ;;
        *) fail "latest release did not resolve to a release tag" ;;
    esac
fi
version="${requested#v}"
case "$version" in
    ''|*[!0-9A-Za-z.+-]*) fail "invalid version: use a release version such as v1.2.3" ;;
esac
case "$version" in
    [0-9]*) ;;
    *) fail "invalid version: use a release version such as v1.2.3" ;;
esac
tag="v$version"
archive="temo_${version}_${os}_${arch}.tar.gz"

if [ -n "${TEMO_INSTALL_DIR:-}" ]; then
    install_dir="$TEMO_INSTALL_DIR"
elif [ -n "${HOME:-}" ]; then
    install_dir="$HOME/.local/bin"
else
    fail "set HOME or TEMO_INSTALL_DIR to choose an install directory"
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/temo-install.XXXXXXXX")" || fail "could not create a temporary directory"
staged=
cleanup() {
    if [ -n "$staged" ]; then
        rm -f "$staged"
    fi
    rm -f "$tmp_dir/$archive" "$tmp_dir/checksums.txt" "$tmp_dir/temo"
    rmdir "$tmp_dir" 2>/dev/null || :
}
trap cleanup 0
trap 'fail "installation interrupted"' 1 2 3 15

curl -fsSL --output "$tmp_dir/$archive" "$release_base/download/$tag/$archive" ||
    fail "could not download $archive; check the release version and platform"
curl -fsSL --output "$tmp_dir/checksums.txt" "$release_base/download/$tag/checksums.txt" ||
    fail "could not download the release checksums"
expected="$(awk -v file="$archive" '$2 == file { print $1; count++ } END { if (count != 1) exit 1 }' "$tmp_dir/checksums.txt")" ||
    fail "the checksums file must contain exactly one entry for $archive"
case "$expected" in
    ''|*[!0-9a-fA-F]*) fail "the release checksum is not a SHA-256 value" ;;
esac
[ "${#expected}" -eq 64 ] || fail "the release checksum is not a SHA-256 value"
expected="$(printf '%s' "$expected" | tr 'A-F' 'a-f')"
if [ "$checksum_tool" = sha256sum ]; then
    actual="$(sha256sum < "$tmp_dir/$archive")" || fail "could not compute the archive checksum"
else
    actual="$(shasum -a 256 < "$tmp_dir/$archive")" || fail "could not compute the archive checksum"
fi
actual="${actual%% *}"
[ "$actual" = "$expected" ] || fail "checksum mismatch for $archive; nothing was installed"

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" temo || fail "could not extract temo from the verified archive"
[ -f "$tmp_dir/temo" ] && [ ! -L "$tmp_dir/temo" ] || fail "the archive does not contain a regular temo binary"
mkdir -p "$install_dir" || fail "could not create the install directory; choose TEMO_INSTALL_DIR without sudo"
install_dir="$(cd "$install_dir" && pwd -P)" || fail "could not access the install directory"
[ ! -d "$install_dir/temo" ] || fail "the install target is a directory, not a binary"
staged="$(mktemp "$install_dir/.temo.XXXXXXXX")" || fail "could not stage the installed binary"
cp "$tmp_dir/temo" "$staged" || fail "could not copy the verified binary"
chmod 755 "$staged" || fail "could not make temo executable"
mv -f "$staged" "$install_dir/temo" || fail "could not install temo"
staged=
printf 'Installed temo %s to %s/temo\n' "$version" "$install_dir"
case ":${PATH:-}:" in
    *":$install_dir:"*) ;;
    *) printf 'Add this directory to PATH: %s\n' "$install_dir" ;;
esac
