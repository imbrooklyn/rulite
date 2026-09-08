"""Collect repeatable Go benchmark samples and report advisory timing changes."""

import argparse
import datetime
import json
import math
import os
import pathlib
import platform
import random
import re
import statistics
import subprocess
import sys


METRICS = ("ns/op", "B/op", "allocs/op")


def parse_run(text):
    """Require a successful, complete benchmark invocation with unique names."""
    headers, benchmarks = {}, {}
    lines = text.splitlines()
    if "PASS" not in lines or any(line.startswith(("FAIL", "--- FAIL")) for line in lines):
        raise ValueError("benchmark invocation did not pass")
    for line in lines:
        if line.startswith(("goos:", "goarch:", "pkg:", "cpu:")):
            key, value = line.split(":", 1)
            headers[key] = value.strip()
        if not line.startswith("Benchmark"):
            continue
        fields = line.split()
        if len(fields) < 8 or len(fields) % 2:
            raise ValueError("incomplete benchmark row")
        match = re.fullmatch(r"(.+)-(\d+)", fields[0])
        if int(fields[1]) < 1:
            raise ValueError("invalid benchmark name or iteration count")
        name, workers = match.groups() if match else (fields[0], "1")
        if int(workers) < 1:
            raise ValueError("invalid benchmark CPU count")
        if name in benchmarks:
            raise ValueError("duplicate benchmark in one sample")
        metrics = {}
        for index in range(2, len(fields), 2):
            metric, value = fields[index + 1], float(fields[index])
            if metric in metrics or not math.isfinite(value) or value < 0:
                raise ValueError("invalid benchmark metric")
            metrics[metric] = value
        if any(metric not in metrics for metric in METRICS) or metrics["ns/op"] == 0:
            raise ValueError("required benchmark metrics missing or invalid")
        benchmarks[name] = {"workers": int(workers), **metrics}
    if not benchmarks or not all(headers.get(key) for key in ("goos", "goarch", "pkg", "cpu")):
        raise ValueError("missing benchmark results or environment headers")
    return {"headers": headers, "benchmarks": benchmarks}


def paired_interval(ratios):
    """Return a seeded percentile bootstrap interval for the paired median ratio."""
    if len(ratios) < 6 or any(not math.isfinite(x) or x <= 0 for x in ratios):
        raise ValueError("at least six positive finite paired ratios are required")
    rng = random.Random(271828)
    medians = sorted(statistics.median(rng.choices(ratios, k=len(ratios))) for _ in range(10000))
    return statistics.median(ratios), medians[249], medians[9749]


def summarize(runs, tolerance=0.15):
    if len(runs) < 6 or not math.isfinite(tolerance) or not 0 <= tolerance <= 1:
        raise ValueError("six or more samples and a relative tolerance in [0, 1] are required")
    sides = set(runs[0])
    if sides not in ({"candidate"}, {"baseline", "candidate"}):
        raise ValueError("invalid sample sides")
    names = {side: set(runs[0][side]["benchmarks"]) for side in sides}
    headers = runs[0]["candidate"]["headers"]
    for run in runs:
        if set(run) != sides:
            raise ValueError("incomplete paired samples")
        for side in sides:
            if run[side]["headers"] != headers or set(run[side]["benchmarks"]) != names[side]:
                raise ValueError("environment or workload set changed during sampling")
            for name, metrics in run[side]["benchmarks"].items():
                if metrics["workers"] != runs[0][side]["benchmarks"][name]["workers"]:
                    raise ValueError("CPU setting changed during sampling")
                if any(not math.isfinite(metrics[m]) or metrics[m] < 0 for m in METRICS) or metrics["ns/op"] == 0:
                    raise ValueError("invalid metric in sample data")
    rows = []
    for name in sorted(set.union(*names.values())):
        row = {"benchmark": name}
        for side in sorted(sides):
            if name in names[side]:
                row[side] = {m: statistics.median(run[side]["benchmarks"][name][m] for run in runs) for m in METRICS}
        if "baseline" not in sides:
            row["timing"] = "baseline unavailable"
        elif "baseline" not in row:
            row["timing"] = "new workload"
        elif "candidate" not in row:
            row["timing"] = "removed workload"
        else:
            for run in runs:
                if run["baseline"]["benchmarks"][name]["workers"] != run["candidate"]["benchmarks"][name]["workers"]:
                    raise ValueError("baseline and candidate CPU settings differ")
            ratios = [run["candidate"]["benchmarks"][name]["ns/op"] / run["baseline"]["benchmarks"][name]["ns/op"] for run in runs]
            median, lower, upper = paired_interval(ratios)
            row.update(ratio=median, interval95=[lower, upper])
            row["timing"] = "review regression" if lower > 1 + tolerance else "within tolerance or inconclusive"
        rows.append(row)
    return rows


def render(rows, samples, tolerance):
    text = ["# Benchmark comparison", "", f"{samples} samples per revision; alternating execution order on one host. Timing is advisory.", "", f"Review when the lower endpoint of a 95% paired-median bootstrap interval exceeds {tolerance:.0%} slowdown.", "10000 seeded resamples; no correction for multiple comparisons. Intervals do not establish equivalence or tail latency.", "", "| Benchmark | Candidate ns/op | B/op | allocs/op | Paired ratio [95% interval] | Timing |", "| --- | ---: | ---: | ---: | --- | --- |"]
    for row in rows:
        current = row.get("candidate", {})
        values = [f"{current[m]:.3f}" if m in current else "-" for m in METRICS]
        ratio = "-"
        if "ratio" in row:
            lower, upper = row["interval95"]
            ratio = f"{row['ratio']:.3f} [{lower:.3f}, {upper:.3f}]"
        text.append(f"| `{row['benchmark']}` | {' | '.join(values)} | {ratio} | {row['timing']} |")
    return "\n".join(text) + "\n"


def run_command(args, cwd):
    result = subprocess.run(args, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {' '.join(args)}\n{result.stdout}")
    return result.stdout


def collect(output, candidate, baseline, samples=6, benchtime="100ms", pattern=".", tolerance=0.15):
    if not 6 <= samples <= 100 or not re.fullmatch(r"[1-9]\d*(?:ns|us|ms|s)", benchtime):
        raise ValueError("samples must be 6..100 and benchtime a positive duration")
    if not math.isfinite(tolerance) or not 0 <= tolerance <= 1:
        raise ValueError("relative tolerance must be in [0, 1]")
    output = pathlib.Path(output).resolve()
    if output.exists() and any(output.iterdir()):
        raise ValueError("output directory must be empty")
    output.mkdir(parents=True, exist_ok=True)
    paths = {"candidate": pathlib.Path(candidate).resolve()}
    if baseline:
        paths["baseline"] = pathlib.Path(baseline).resolve()
    metadata = {
        "utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "platform": {"system": platform.system(), "release": platform.release(), "machine": platform.machine()},
        "settings": {name: os.environ.get(name, "default") for name in ("GOMAXPROCS", "GOGC", "GOMEMLIMIT")},
        "samples": samples, "benchtime": benchtime, "pattern": pattern, "tolerance": tolerance,
        "revisions": {},
    }
    binaries = {}
    reference_env = None
    runs = []
    try:
        for side, directory in paths.items():
            go_env = json.loads(run_command(["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOEXPERIMENT", "CGO_ENABLED", "GOFLAGS"], directory))
            if reference_env is not None and go_env != reference_env:
                raise ValueError("baseline and candidate toolchains or target settings differ")
            reference_env = go_env
            metadata["go"] = go_env
            metadata["revisions"][side] = {"commit": run_command(["git", "rev-parse", "HEAD"], directory).strip(), "dirty": bool(run_command(["git", "status", "--porcelain"], directory).strip())}
            binary = output / (side + ".test")
            binaries[side] = binary
            # Build once, outside measurement; each sample is a fresh process.
            run_command(["go", "test", "-c", "-o", str(binary), "."], directory)
        for index in range(samples):
            order = ["baseline", "candidate"] if "baseline" in paths else ["candidate"]
            if index % 2:
                order.reverse()
            paired = {}
            for side in order:
                command = [str(binaries[side]), "-test.run=^$", "-test.bench=" + pattern, "-test.benchmem", "-test.benchtime=" + benchtime, "-test.count=1"]
                result = subprocess.run(command, cwd=paths[side], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                (output / f"{side}-{index + 1}.txt").write_text(result.stdout)
                if result.returncode:
                    raise RuntimeError(f"{side} benchmark sample {index + 1} failed; inspect its raw output")
                paired[side] = parse_run(result.stdout)
                print(f"Completed {side} sample {index + 1}/{samples}", flush=True)
            runs.append(paired)
            (output / "samples.json").write_text(json.dumps({"metadata": metadata, "runs": runs}, indent=2) + "\n")
        rows = summarize(runs, tolerance)
        (output / "comparison.json").write_text(json.dumps(rows, indent=2) + "\n")
        (output / "comparison.md").write_text(render(rows, samples, tolerance))
        return rows
    finally:
        (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
        for binary in binaries.values():
            binary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", help="empty directory for raw samples and comparison artifacts")
    parser.add_argument("--candidate", default=".", help="candidate checkout")
    parser.add_argument("--baseline", help="baseline checkout; omit for sampling without a comparison")
    parser.add_argument("--samples", type=int, default=6)
    parser.add_argument("--benchtime", default="100ms")
    parser.add_argument("--bench", default=".", help="Go benchmark expression")
    parser.add_argument("--tolerance", type=float, default=0.15)
    args = parser.parse_args()
    try:
        rows = collect(args.output, args.candidate, args.baseline, args.samples, args.benchtime, args.bench, args.tolerance)
    except (ValueError, RuntimeError, OSError) as error:
        print(str(error), file=sys.stderr)
        return 1
    flagged = sum(row["timing"] == "review regression" for row in rows)
    print(f"Recorded {len(rows)} workloads; {flagged} advisory timing regressions. Review comparison.md.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
