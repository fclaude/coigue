#!/usr/bin/env python3
"""Compares benchmark runs saved by scripts/bench.sh as one Markdown table.

    python3 scripts/benchtable.py bench-results/baseline.txt bench-results/stage1.txt ...

Each column is a run (named by its "# label:" line, else its file name); each
row is a benchmark, showing time per operation and, after the first run, the
speed-up over the first run that has a value. Benchmarks a run skipped or did
not have show as "-". With --metric NAME (for example ms/key) the table shows
that metric instead of time per operation. Repeated runs (-count) are
averaged.
"""
import argparse
import os
import re
import sys

LINE = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(.*)$")


def parse(path):
    label = os.path.splitext(os.path.basename(path))[0]
    pkg = ""
    values = {}
    with open(path, encoding="utf-8") as f:
        for raw in f:
            line = raw.rstrip("\n")
            if line.startswith("# label: "):
                label = line[len("# label: "):]
            elif line.startswith("pkg: "):
                pkg = line[len("pkg: "):].rsplit("/", 1)[-1]
            match = LINE.match(line)
            if not match:
                continue
            name, rest = match.groups()
            fields = rest.split()
            metrics = {}
            for i in range(0, len(fields) - 1, 2):
                try:
                    metrics[fields[i + 1]] = float(fields[i])
                except ValueError:
                    pass
            values.setdefault(f"{pkg}.{name}", []).append(metrics)
    return label, values


def human_ns(ns):
    for unit, scale in (("s", 1e9), ("ms", 1e6), ("µs", 1e3)):
        if ns >= scale:
            return f"{ns / scale:.3g} {unit}"
    return f"{ns:.3g} ns"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("runs", nargs="+")
    parser.add_argument("--metric", default="ns/op")
    parser.add_argument("--filter", default="", help="regular expression on benchmark names")
    args = parser.parse_args()
    runs = [parse(p) for p in args.runs]
    names = []
    for _, values in runs:
        for name in values:
            if name not in names and re.search(args.filter, name):
                names.append(name)
    header = ["benchmark"] + [label for label, _ in runs]
    print("| " + " | ".join(header) + " |")
    print("|" + "---|" * len(header))
    for name in names:
        row = [name.replace("Benchmark", "", 1)]
        first = None
        for _, values in runs:
            samples = [m[args.metric] for m in values.get(name, []) if args.metric in m]
            if not samples:
                row.append("-")
                continue
            value = sum(samples) / len(samples)
            text = human_ns(value) if args.metric == "ns/op" else f"{value:.3g}"
            if first is None:
                first = value
            elif value > 0:
                text += f" ({first / value:.3g}×)"
            row.append(text)
        print("| " + " | ".join(row) + " |")


if __name__ == "__main__":
    sys.exit(main())
