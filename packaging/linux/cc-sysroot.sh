#!/usr/bin/env bash
set -euo pipefail
: "${ONELOOP_LINUX_SYSROOT:?}"
# -B selects Debian's startup objects; rpath-link resolves indirect libraries
# while linking, without embedding the build directory in the executable.
exec gcc --sysroot="$ONELOOP_LINUX_SYSROOT" \
  -B"$ONELOOP_LINUX_SYSROOT/usr/lib/x86_64-linux-gnu/" \
  -L"$ONELOOP_LINUX_SYSROOT/usr/lib/x86_64-linux-gnu" \
  -L"$ONELOOP_LINUX_SYSROOT/lib/x86_64-linux-gnu" \
  -Wl,-rpath-link,"$ONELOOP_LINUX_SYSROOT/usr/lib/x86_64-linux-gnu:$ONELOOP_LINUX_SYSROOT/lib/x86_64-linux-gnu" \
  "$@"
