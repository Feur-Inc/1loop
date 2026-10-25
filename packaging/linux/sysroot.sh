#!/usr/bin/env bash
# Download verified Debian 12 development packages into the build directory.
# No system package is installed and no maintainer script is executed.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."
root=$PWD/bin/linux/sysroot
apt=$PWD/bin/linux/sysroot-apt
if [[ ! -f $root/.complete ]]; then
  mkdir -p "$root" "$apt/lists/partial" "$apt/archives/partial"
  touch "$apt/status"
  # Keep the host's apt hooks, sources and status outside this operation.
  cat > "$apt/apt.conf" <<'CONFIG'
Dir::Etc::parts "-";
Dir::Etc::main "-";
Acquire::Languages "none";
CONFIG
  cat > "$apt/sources.list" <<'SOURCES'
deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://deb.debian.org/debian bookworm main
deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://deb.debian.org/debian bookworm-updates main
deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://security.debian.org/debian-security bookworm-security main
SOURCES
  options=(
    -o "Dir::State=$apt"
    -o "Dir::Cache=$apt"
    -o "Dir::Log=$apt"
    -o "Dir::Etc::sourcelist=$apt/sources.list"
    -o Dir::Etc::sourceparts=-
    -o "Dir::State::status=$apt/status"
    -o "Dir::State::lists=$apt/lists"
    -o "Dir::Cache::archives=$apt/archives"
    -o "APT::Sandbox::User=$(id -un)"
    -o APT::Architecture=amd64
    -o APT::Get::List-Cleanup=0
  )
  APT_CONFIG="$apt/apt.conf" apt-get "${options[@]}" update >&2
  APT_CONFIG="$apt/apt.conf" apt-get "${options[@]}" --download-only --yes --no-install-recommends install \
    libc6-dev linux-libc-dev libgl1-mesa-dev libx11-dev libxrandr-dev \
    libxxf86vm-dev libxi-dev libxcursor-dev libxinerama-dev \
    libwayland-dev libxkbcommon-dev >&2
  for package in "$apt/archives/"*.deb; do
    dpkg-deb --extract "$package" "$root"
  done
  touch "$root/.complete"
fi
# Absolute package symlinks must resolve inside the sysroot, not on the host.
python3 - "$root" <<'PYLINKS'
import os
from pathlib import Path
import sys
root = Path(sys.argv[1])
for link in root.rglob("*"):
    if link.is_symlink():
        target = os.readlink(link)
        if target.startswith("/"):
            relative = os.path.relpath(root / target.lstrip("/"), link.parent)
            link.unlink()
            link.symlink_to(relative)
PYLINKS
printf '%s\n' "$root"
