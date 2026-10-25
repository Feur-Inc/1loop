#!/usr/bin/env bash
# The same checks as GitHub Actions; an optional argument selects one platform.
set -euo pipefail
exec bash "$(dirname -- "${BASH_SOURCE[0]}")/.github/scripts/build-and-verify.sh" "$@"
