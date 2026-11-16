#!/bin/sh
# SPDX-License-Identifier: MPL-2.0
#
# Runs 1loop's in-app benchmarks (internal/ui/bench.go) on a graphical
# session, each mode on a fresh copy of a synthetic state, and prints the
# reports. Meant for the test VM, never the desktop in use:
#
#   go build -o /tmp/1loop ./cmd/1loop
#   go run ./scripts/perf/genstate -out /tmp/1loop-perf -theme aurora   # and once without -theme
#   scripts/perf/run.sh /tmp/1loop /tmp/1loop-perf out/ idle scroll stream ...
#
# Each mode writes out/<mode>.txt (report), out/<mode>.gctrace and, with
# PROFILE=1, out/<mode>/{cpu,heap,allocs}.pprof. SECONDS_<mode>=N
# overrides a mode's span (ONELOOP_BENCH_SECONDS). The script also notes
# the wall time from exec to package init, for the startup table.
set -eu
bin=$1 state=$2 out=$3
shift 3
mkdir -p "$out"
for mode in "$@"; do
	work=$(mktemp -d)
	cp -r "$state" "$work/config"
	mkdir -p "$work/cache/1loop"
	span=$(eval echo "\${SECONDS_$mode:-}")
	profile=""
	[ "${PROFILE:-0}" = 1 ] && profile="$out/$mode"
	t0=$(date +%s%3N)
	XDG_CONFIG_HOME="$work/config" XDG_CACHE_HOME="$work/cache" HOME="$work" \
		ONELOOP_BENCH=$mode ONELOOP_BENCH_SECONDS=$span ONELOOP_BENCH_PROFILE=$profile \
		ONELOOP_BROWSER=none GODEBUG=gctrace=1 \
		timeout 900 "$bin" -dev >"$out/$mode.stdout" 2>"$out/$mode.gctrace" || true
	if [ -f "$work/cache/1loop/bench.txt" ]; then
		cp "$work/cache/1loop/bench.txt" "$out/$mode.txt"
		init=$(sed -n 's/.*package init at unix \([0-9]*\) ms.*/\1/p' "$out/$mode.txt")
		[ -n "$init" ] && echo "  exec to package init  $((init - t0)) ms" >>"$out/$mode.txt"
		cat "$out/$mode.txt"
	else
		echo "bench $mode: no report (see $out/$mode.gctrace)"
	fi
	rm -rf "$work"
done
