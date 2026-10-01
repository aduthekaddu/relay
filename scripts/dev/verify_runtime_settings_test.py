#!/usr/bin/env python3
"""Cleanup boundary checks with owned processes and synthetic filesystem data."""
import contextlib
import importlib.util
import io
import json
import pathlib
import shutil
import signal
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "runtime_settings", pathlib.Path(__file__).with_name("verify-runtime-settings.py")
)
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


@unittest.skipUnless(pathlib.Path("/proc/self/stat").exists(), "Linux process identity probe")
class CleanupTests(unittest.TestCase):
    def fixture(self):
        root = pathlib.Path(tempfile.mkdtemp(prefix="rs135-cleanup-test-"))
        self.addCleanup(shutil.rmtree, root, True)
        return root

    def process(self):
        proc = subprocess.Popen(["/bin/sleep", "30"], start_new_session=True)
        def stop():
            if proc.poll() is None:
                proc.kill()
            proc.wait(timeout=3)
        self.addCleanup(stop)
        return proc

    def test_forced_terminal_cleanup_scans_race_and_removes_fixture(self):
        root, proc = self.fixture(), self.process()
        log = root / "owned.log"
        log.write_text("WARNING: DATA RACE\nsynthetic marker\n")
        results = {}
        errors = runtime.cleanup_fixture(
            root, [], [], {proc.pid: runtime.start_time(proc.pid)}, [log], [], results
        )
        self.assertEqual(proc.wait(timeout=3), -signal.SIGKILL)
        self.assertFalse(root.exists())
        self.assertTrue(results["race_warnings"])
        self.assertEqual(results["owned_cleanup"]["survivors"], [proc.pid])
        self.assertEqual(results["owned_cleanup"]["forced_kills"], [proc.pid])
        self.assertEqual(results["owned_cleanup"]["remaining_survivors"], [])
        self.assertTrue(any("reported a race" in e for e in errors))
        self.assertTrue(any("forced kill" in e for e in errors))

    def test_body_error_survives_cleanup_error_and_race_reporting(self):
        proc = self.process()
        original = runtime.cleanup_fixture
        def with_owned_terminal(root, procs, streams, terminals, logs, commands, results):
            terminals[proc.pid] = runtime.start_time(proc.pid)
            log = root / "owned-race.log"
            log.write_text("WARNING: DATA RACE\nsynthetic marker\n")
            logs.append(log)
            return original(root, procs, streams, terminals, logs, commands, results)
        output = io.StringIO()
        with patch.object(runtime, "cleanup_fixture", side_effect=with_owned_terminal):
            with contextlib.redirect_stdout(output):
                with self.assertRaisesRegex(AssertionError, "Fixture CLI failed: passwd") as caught:
                    runtime.scenario("/bin/false", "/bin/false", "normal", False)
        result = json.loads(output.getvalue())
        self.assertTrue(result["race_warnings"])
        self.assertTrue(result["owned_cleanup"]["fixture_removed"])
        self.assertEqual(result["owned_cleanup"]["survivors"], [proc.pid])
        self.assertEqual(proc.wait(timeout=3), -signal.SIGKILL)
        if hasattr(caught.exception, "__notes__"):
            self.assertIn("forced kill", caught.exception.__notes__[0])

    def test_failure_before_startup_still_removes_fixture_and_reports(self):
        output = io.StringIO()
        with patch.object(runtime, "reserve_port", side_effect=RuntimeError("no fixture port")):
            with contextlib.redirect_stdout(output):
                with self.assertRaisesRegex(RuntimeError, "no fixture port"):
                    runtime.scenario("/bin/false", "/bin/false", "normal", False)
        result = json.loads(output.getvalue())
        self.assertTrue(result["owned_cleanup"]["fixture_removed"])
        self.assertEqual(result["owned_cleanup"]["survivors"], [])

    def test_stop_error_does_not_skip_other_processes_or_race_scan(self):
        root = self.fixture()
        failed, other = self.process(), self.process()
        log = root / "owned.log"
        log.write_text("WARNING: DATA RACE\nsynthetic marker\n")
        results, stopped = {}, []
        original = runtime.stop_process
        def stop(proc):
            stopped.append(proc.pid)
            if proc is failed:
                raise OSError("injected stop error")
            original(proc)
        with patch.object(runtime, "stop_process", side_effect=stop):
            errors = runtime.cleanup_fixture(root, [other, failed], [], {}, [log], [], results)
        self.assertEqual(stopped, [failed.pid, other.pid])
        self.assertIsNotNone(other.poll())
        self.assertTrue(results["race_warnings"])
        self.assertFalse(root.exists())
        self.assertTrue(any("injected stop error" in e for e in errors))

    def test_unreadable_log_is_unknown_and_does_not_skip_removal(self):
        root = self.fixture()
        results = {}
        errors = runtime.cleanup_fixture(root, [], [], {}, [root / "missing.log"], [], results)
        self.assertIsNone(results["race_warnings"])
        self.assertTrue(errors)
        self.assertFalse(root.exists())

    def test_race_does_not_skip_later_unreadable_log(self):
        root = self.fixture()
        log = root / "race.log"
        log.write_text("WARNING: DATA RACE\n")
        results = {}
        errors = runtime.cleanup_fixture(root, [], [], {}, [log, root / "missing"], [], results)
        self.assertTrue(results["race_warnings"])
        self.assertTrue(any("Read owned log" in e for e in errors))
        self.assertFalse(root.exists())

    def test_report_error_preserves_original_exception_identity(self):
        original = RuntimeError("original boundary failure")
        output = io.StringIO()
        output.close()
        result = {}
        cleanup = runtime.cleanup_fixture
        def capture(*args):
            errors = cleanup(*args)
            result.update(args[-1])
            return errors
        with patch.object(runtime, "reserve_port", side_effect=original):
            with patch.object(runtime, "cleanup_fixture", side_effect=capture):
                with contextlib.redirect_stdout(output):
                    with self.assertRaises(RuntimeError) as caught:
                        runtime.scenario("/bin/false", "/bin/false", "normal", False)
        self.assertIs(caught.exception, original)
        self.assertTrue(result["owned_cleanup"]["fixture_removed"])

    def test_directory_removal_failure_is_reported(self):
        parent = self.fixture()
        root = parent / "owned"
        root.mkdir()
        self.addCleanup(parent.chmod, 0o700)
        parent.chmod(0o500)
        try:
            results = {}
            errors = runtime.cleanup_fixture(root, [], [], {}, [], [], results)
            self.assertTrue(root.exists())
            self.assertFalse(results["owned_cleanup"]["fixture_removed"])
            self.assertTrue(any("Remove owned fixture" in e for e in errors))
        finally:
            parent.chmod(0o700)

    def test_changed_pid_identity_is_never_signalled(self):
        root = self.fixture()
        proc = self.process()
        started = runtime.start_time(proc.pid)
        results = {}
        with patch.object(runtime, "start_time", side_effect=[started, "changed", "changed"]):
            with patch.object(runtime.os, "killpg") as group_kill:
                with patch.object(runtime.os, "kill") as single_kill:
                    runtime.cleanup_fixture(root, [], [], {proc.pid: started}, [], [], results)
        group_kill.assert_not_called()
        single_kill.assert_not_called()
        self.assertIsNone(proc.poll())
        self.assertEqual(results["owned_cleanup"]["forced_kills"], [])


if __name__ == "__main__":
    unittest.main()
