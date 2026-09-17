#!/usr/bin/env bash
# Install gVisor (runsc) and register it with Docker inside WSL2 Ubuntu.
# Needs sudo. Run from Windows with:
#   wsl -d Ubuntu -- sudo bash /mnt/c/Users/user/documents/buggies/deploy/wsl-gvisor-setup.sh
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then echo "run with sudo" >&2; exit 1; fi

ARCH=$(uname -m)
URL="https://storage.googleapis.com/gvisor/releases/release/latest/${ARCH}"
TMP=$(mktemp -d)
cd "$TMP"
curl -fsSLO "${URL}/runsc" -O "${URL}/runsc.sha512" -O "${URL}/containerd-shim-runsc-v1" -O "${URL}/containerd-shim-runsc-v1.sha512"
sha512sum -c runsc.sha512 containerd-shim-runsc-v1.sha512
chmod a+rx runsc containerd-shim-runsc-v1
mv runsc containerd-shim-runsc-v1 /usr/local/bin/

# Register the runtime. --network=none inside the sandbox is set per container
# by the executor; here we only make runsc available and keep runc the default
# so unrelated containers on this machine are untouched.
/usr/local/bin/runsc install --runtime runsc
systemctl restart docker

echo "runtimes now:"; docker info --format '{{range $k,$v := .Runtimes}}{{$k}} {{end}}'
echo "smoke test (should print the gVisor kernel banner):"
docker run --rm --runtime=runsc --network=none alpine dmesg | head -3
