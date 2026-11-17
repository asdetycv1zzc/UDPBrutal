#!/usr/bin/env bash
# Hysteria-style binary + YAML + systemd installation. No service is started.
set -euo pipefail

task_script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
task_source_dir="$(dirname -- "$task_script_dir")"
task_binary=""
task_version=""
task_repo="${UDPBRUTAL_REPO:-asdetycv1zzc/UDPBrutal}"
task_download_dir=""
cleanup() { if [[ -n "$task_download_dir" ]]; then rm -rf -- "$task_download_dir"; fi; }
trap cleanup EXIT

while (($#)); do
  case "$1" in
    --local) [[ $# -ge 2 ]] || { echo '--local requires a binary path' >&2; exit 1; }; task_binary="$2"; shift 2 ;;
    --version) [[ $# -ge 2 ]] || { echo '--version requires a release tag' >&2; exit 1; }; task_version="$2"; shift 2 ;;
    -h|--help) echo 'Usage: sudo bash scripts/install_server.sh [--local ./udpbrutal | --version vX.Y.Z]'; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done
[[ "$(id -u)" == 0 ]] || { echo 'Run this installer as root.' >&2; exit 1; }
[[ "$(uname -s)" == Linux ]] || { echo 'This installer supports Linux; use the binary directly on other platforms.' >&2; exit 1; }

if [[ -z "$task_binary" && -z "$task_version" && -f "$task_source_dir/go.mod" ]]; then
  command -v go >/dev/null || { echo 'Build with Go 1.23+, or pass --local /path/to/udpbrutal.' >&2; exit 1; }
  task_download_dir="$(mktemp -d)"
  task_binary="$task_download_dir/udpbrutal"
  (cd -- "$task_source_dir" && CGO_ENABLED=0 go build -trimpath -o "$task_binary" ./cmd/udpbrutal)
fi
if [[ -z "$task_binary" ]]; then
  command -v curl >/dev/null || { echo 'curl is required.' >&2; exit 1; }
  command -v tar >/dev/null || { echo 'tar is required.' >&2; exit 1; }
  case "$(uname -m)" in
    x86_64|amd64) task_arch=amd64 ;;
    aarch64|arm64) task_arch=arm64 ;;
    *) echo 'Unsupported prebuilt architecture. Build locally, then use --local.' >&2; exit 1 ;;
  esac
  task_download_dir="$(mktemp -d)"
  if [[ -z "$task_version" ]]; then task_release_path=latest/download; else task_release_path="download/$task_version"; fi
  curl --fail --location --retry 2 "https://github.com/$task_repo/releases/$task_release_path/udpbrutal-linux-$task_arch.tar.gz" -o "$task_download_dir/release.tar.gz"
  tar -xzf "$task_download_dir/release.tar.gz" -C "$task_download_dir" udpbrutal
  task_binary="$task_download_dir/udpbrutal"
fi
[[ -f "$task_binary" ]] || { echo "Binary not found: $task_binary" >&2; exit 1; }
[[ -f "$task_source_dir/examples/server.yaml" && -f "$task_script_dir/udpbrutal-server.service" ]] || { echo 'Run this script from a complete source checkout.' >&2; exit 1; }
install -m 0755 -- "$task_binary" /usr/local/bin/udpbrutal
install -d -m 0755 /etc/udpbrutal
if [[ ! -e /etc/udpbrutal/config.yaml ]]; then
  install -m 0644 "$task_source_dir/examples/server.yaml" /etc/udpbrutal/config.yaml
fi
install -m 0644 "$task_script_dir/udpbrutal-server.service" /etc/systemd/system/udpbrutal-server.service
if command -v systemctl >/dev/null; then systemctl daemon-reload; fi
echo 'Installed /usr/local/bin/udpbrutal and udpbrutal-server.service.'
echo 'Edit /etc/udpbrutal/config.yaml, then: systemctl enable --now udpbrutal-server'
