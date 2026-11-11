#!/bin/sh
# Runs the editing-performance benchmarks and saves the output.
#
#   scripts/bench.sh LABEL [OUTDIR]
#
# writes OUTDIR/LABEL.txt (OUTDIR defaults to bench-results). Compare runs with
#
#   python3 scripts/benchtable.py bench-results/baseline.txt bench-results/stage1.txt ...
#
# Every package run is bounded by a timeout, so a regression that makes a
# benchmark hang cannot keep the machine busy. The end-to-end benchmarks in
# internal/app run BENCHTIME times (default 3x, each one a whole session); the
# micro-benchmarks run for MICRO_BENCHTIME (default 300ms) each. COUNT
# (default 1) repeats everything.
set -eu

label=${1:?usage: scripts/bench.sh LABEL [OUTDIR]}
outdir=${2:-bench-results}
benchtime=${BENCHTIME:-3x}
micro=${MICRO_BENCHTIME:-300ms}
count=${COUNT:-1}
mkdir -p "$outdir"
out="$outdir/$label.txt"

{
	echo "# label: $label"
	echo "# commit: $(git rev-parse --short HEAD 2>/dev/null || echo unknown)$(git diff --quiet 2>/dev/null || echo ' (with uncommitted changes)')"
	echo "# date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# benchtime: app $benchtime, others $micro; count: $count"
} >"$out"

# Build every test binary before running any, so editing the source while
# the benchmarks run cannot change what is measured.
bindir=$(mktemp -d)
trap 'rm -rf "$bindir"' EXIT INT TERM
pkgs=""
for pkg in term rope buffer syntax app; do
	# A package this tree does not have (an older stage) is skipped.
	if [ -d "internal/$pkg" ]; then
		go test -c -o "$bindir/$pkg.test" "./internal/$pkg"
		pkgs="$pkgs $pkg"
	fi
done
for pkg in $pkgs; do
	time=$micro
	if [ "$pkg" = app ]; then
		time=$benchtime
	fi
	(cd "internal/$pkg" && "$bindir/$pkg.test" -test.run '^$' -test.bench . -test.benchmem \
		-test.benchtime "$time" -test.count "$count" -test.timeout 20m) | tee -a "$out"
done
echo "wrote $out"
