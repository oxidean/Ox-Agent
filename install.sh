#!/usr/bin/env bash
set -euo pipefail
repo="${OX_AGENT_REPOSITORY:-oxidean/Ox-Agent}"
version="${OX_AGENT_VERSION:-latest}"
install_dir="${OX_AGENT_INSTALL_DIR:-$HOME/.local/bin}"
os_name="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os_name:$arch" in
  darwin:arm64|darwin:aarch64) target="darwin_arm64" ;;
  darwin:x86_64|darwin:amd64) target="darwin_amd64" ;;
  linux:arm64|linux:aarch64) target="linux_arm64" ;;
  linux:x86_64|linux:amd64) target="linux_amd64" ;;
  *) echo "Unsupported platform: $os_name/$arch" >&2; exit 1 ;;
esac
if [[ "$version" == "latest" ]]; then
  version="$(curl -fsSIL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed -n 's|.*/tag/||p' || true)"
fi
[[ -n "$version" ]] || { echo "Could not determine Ox-Agent version" >&2; exit 1; }
archive="ox-agent_${version#v}_${target}.tar.gz"
base="https://github.com/$repo/releases/download/$version"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
expected="$(awk -v file="$archive" '$2 == file { print $1 }' "$tmp/checksums.txt")"
if [[ "$os_name" == "darwin" ]]; then
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1}')"
else
  actual="$(sha256sum "$tmp/$archive" | awk '{ print $1}')"
fi
[[ -n "$expected" && "$expected" == "$actual" ]] || { echo "Checksum verification failed" >&2; exit 1; }
mkdir -p "$install_dir"
tar -xzf "$tmp/$archive" -C "$tmp"
install -m 0755 "$tmp/ox-agent" "$install_dir/ox-agent"
echo "Installed ox-agent $version to $install_dir/ox-agent"
command -v ox-agent >/dev/null 2>&1 || echo "Add $install_dir to PATH to use ox-agent" >&2
