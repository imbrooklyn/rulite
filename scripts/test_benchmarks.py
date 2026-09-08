"""Tests for benchmark parsing, paired statistics, and sampling failures."""

import copy
import importlib.util
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("benchmarks", pathlib.Path(__file__).with_name("benchmarks.py"))
benchmarks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmarks)

RAW = """goos: darwin
goarch: arm64
pkg: github.com/imbrooklyn/rulite
cpu: Example CPU
BenchmarkFireScale/all_miss/rules_1000-12 100 9000 ns/op 100000 evaluated/s 0 B/op 0 allocs/op
PASS
"""
NAME = "BenchmarkFireScale/all_miss/rules_1000"


def paired(ratios):
    runs = []
    for ratio in ratios:
        baseline = benchmarks.parse_run(RAW)
        candidate = copy.deepcopy(baseline)
        candidate["benchmarks"][NAME]["ns/op"] *= ratio
        runs.append({"baseline": baseline, "candidate": candidate})
    return runs


class BenchmarkTests(unittest.TestCase):
    def test_metrics_and_success_are_required(self):
        parsed = benchmarks.parse_run(RAW)
        self.assertEqual(parsed["benchmarks"][NAME]["workers"], 12)
        self.assertEqual(parsed["benchmarks"][NAME]["ns/op"], 9000)
        single = benchmarks.parse_run(RAW.replace("-12 ", " "))
        self.assertEqual(single["benchmarks"][NAME]["workers"], 1)
        for invalid in [RAW.replace("PASS", "FAIL"), RAW.replace("PASS", ""), RAW.replace("9000", "nan"), RAW.replace("9000", "0"), RAW.replace("0 B/op", "-1 B/op"), RAW.replace("0 B/op", ""), RAW.replace("100 9000", "0 9000"), RAW.replace("cpu: Example CPU\n", ""), RAW.replace("PASS", RAW.splitlines()[4] + "\nPASS")]:
            with self.subTest(raw=invalid), self.assertRaises(ValueError):
                benchmarks.parse_run(invalid)

    def test_paired_tolerance_and_noise(self):
        cases = [([1.0] * 6, False), ([1.14] * 6, False), ([1.3] * 6, True), ([0.5, 0.7, 1.0, 1.3, 1.8, 2.0], False)]
        for ratios, flagged in cases:
            with self.subTest(ratios=ratios):
                row = benchmarks.summarize(paired(ratios))[0]
                self.assertEqual(row["timing"] == "review regression", flagged)
                self.assertEqual(row, benchmarks.summarize(paired(ratios))[0])
        with self.assertRaises(ValueError):
            benchmarks.paired_interval([1] * 5)

    def test_rejects_incompatible_or_incomplete_samples(self):
        for mutation in [
            lambda runs: runs[-1].pop("baseline"),
            lambda runs: runs[-1]["candidate"]["headers"].update(cpu="Different CPU"),
            lambda runs: runs[-1]["candidate"]["benchmarks"].clear(),
            lambda runs: runs[-1]["candidate"]["benchmarks"][NAME].update(workers=8),
            lambda runs: runs[-1]["candidate"]["benchmarks"][NAME].update({"ns/op": float("inf")}),
        ]:
            runs = paired([1] * 6)
            mutation(runs)
            with self.assertRaises(ValueError):
                benchmarks.summarize(runs)
        with self.assertRaises(ValueError):
            benchmarks.summarize(paired([1] * 6), float("nan"))

    def test_new_removed_and_unpaired_workloads_are_explicit(self):
        runs = paired([1] * 6)
        for run in runs:
            run["candidate"]["benchmarks"]["BenchmarkAdded"] = run["candidate"]["benchmarks"].pop(NAME)
        rows = benchmarks.summarize(runs)
        self.assertEqual({row["timing"] for row in rows}, {"new workload", "removed workload"})
        rows = benchmarks.summarize([{"candidate": run["candidate"]} for run in runs])
        self.assertEqual(rows[0]["timing"], "baseline unavailable")

    def test_collect_alternates_pairs_and_removes_binaries(self):
        order = []
        def command(args, cwd):
            if args[:2] == ["go", "env"]:
                return '{"GOVERSION":"go1.27.0"}'
            if args[:2] == ["go", "test"]:
                pathlib.Path(args[4]).write_text("test binary")
                return ""
            return "revision" if args[1] == "rev-parse" else ""
        def sample(args, **kwargs):
            order.append(pathlib.Path(args[0]).stem)
            return subprocess.CompletedProcess(args, 0, RAW)
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(benchmarks, "run_command", side_effect=command), mock.patch.object(benchmarks.subprocess, "run", side_effect=sample):
            rows = benchmarks.collect(directory, ".", ".", 6)
            self.assertEqual(order, ["baseline", "candidate", "candidate", "baseline"] * 3)
            self.assertEqual(len(rows), 1)
            self.assertFalse(list(pathlib.Path(directory).glob("*.test")))
            self.assertEqual(len(list(pathlib.Path(directory).glob("*-*.txt"))), 12)
            self.assertIn("95%", pathlib.Path(directory, "comparison.md").read_text())

    def test_command_failure_and_existing_output_are_not_success(self):
        with tempfile.TemporaryDirectory() as directory:
            pathlib.Path(directory, "keep.txt").write_text("existing report")
            with self.assertRaises(ValueError):
                benchmarks.collect(directory, ".", None)
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(benchmarks, "run_command", side_effect=RuntimeError("build failed")):
            with self.assertRaises(RuntimeError):
                benchmarks.collect(directory, ".", None)
            self.assertFalse(pathlib.Path(directory, "comparison.md").exists())
            self.assertTrue(pathlib.Path(directory, "metadata.json").exists())

    def test_failed_sample_keeps_evidence_and_removes_binary(self):
        def command(args, cwd):
            if args[:2] == ["go", "env"]:
                return '{"GOVERSION":"go1.27.0"}'
            if args[:2] == ["go", "test"]:
                pathlib.Path(args[4]).write_text("test binary")
                return ""
            return "revision" if args[1] == "rev-parse" else ""
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(benchmarks, "run_command", side_effect=command), mock.patch.object(benchmarks.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "FAIL\n")):
            with self.assertRaises(RuntimeError):
                benchmarks.collect(directory, ".", None)
            self.assertEqual(pathlib.Path(directory, "candidate-1.txt").read_text(), "FAIL\n")
            self.assertFalse(pathlib.Path(directory, "comparison.md").exists())
            self.assertFalse(list(pathlib.Path(directory).glob("*.test")))

    def test_toolchain_mismatch_stops_before_sampling(self):
        versions = iter(['{"GOVERSION":"go1.27.0"}', '{"GOVERSION":"go1.28.0"}'])
        def command(args, cwd):
            if args[:2] == ["go", "env"]:
                return next(versions)
            if args[:2] == ["go", "test"]:
                pathlib.Path(args[4]).write_text("test binary")
                return ""
            return "revision" if args[1] == "rev-parse" else ""
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(benchmarks, "run_command", side_effect=command), mock.patch.object(benchmarks.subprocess, "run") as sample:
            with self.assertRaises(ValueError):
                benchmarks.collect(directory, ".", ".")
            sample.assert_not_called()
            self.assertFalse(list(pathlib.Path(directory).glob("*.test")))
            self.assertFalse(pathlib.Path(directory, "comparison.md").exists())


if __name__ == "__main__":
    unittest.main()
