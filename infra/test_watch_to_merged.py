"""Synthetic localhost/process tests only; no deployment or private tools."""
import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("watcher", Path(__file__).with_name("watch_to_merged.py"))
w = importlib.util.module_from_spec(spec)
spec.loader.exec_module(w)


class WatchTests(unittest.TestCase):
    def test_service_restarts_and_keeps_state_after_oom(self):
        unit = w.service_unit('/opt/watcher/file.py', '/var/lib/watcher/state.sqlite3', [])
        for required in ['Restart=always', 'RestartSec=5', 'OOMPolicy=stop',
                         'MemoryMax=256M', 'MemoryHigh=128M', 'KillMode=control-group',
                         'StartLimitIntervalSec=0', 'WantedBy=multi-user.target',
                         '--foreground', '/var/lib/watcher/state.sqlite3']:
            self.assertIn(required, unit)
        self.assertNotIn('ExecStartPre=', unit)  # Restart must never reset state.
        self.assertEqual(w.systemd_argument('a%$"b'), '"a%%$$\\"b"')

    def test_sigkill_restart_keeps_checkpoint_and_partial_line(self):
        source = self.root / 'kill-source.txt'
        source.write_text('before\npartial')
        result = self.merged / 'kill.txt'
        command = [sys.executable, str(Path(w.__file__)), '--foreground', '--file',
                   str(source)+'=kill.txt', '--merged-dir', str(self.merged),
                   '--state', str(self.state), '--interval', '0.05']
        def wait_for(expected, process):
            deadline = time.monotonic()+5
            while not result.exists() or result.read_text() != expected:
                if process.poll() is not None or time.monotonic() > deadline:
                    self.fail('Watcher did not recover expected output')
                time.sleep(0.02)
        process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            wait_for('before\n', process)
            process.kill(); process.wait(timeout=5)
            with source.open('a') as writer:
                writer.write('-after-kill\n')
            process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            wait_for('before\npartial-after-kill\n', process)
        finally:
            if process.poll() is None: process.terminate()
            process.wait(timeout=5)
        self.assertEqual(result.read_text(), 'before\npartial-after-kill\n')

    def test_editable_settings_and_cli_overrides(self):
        with patch.multiple(w, FILES=[('/tmp/source.txt', 'renamed.txt')],
                            SSH_TARGET='example-controller', MERGED_DIR='/tmp/merged',
                            STATE_PATH='/tmp/state.sqlite3', FROM_END=True):
            args = w.parse_args([])
            self.assertEqual(args.file, ['/tmp/source.txt=renamed.txt'])
            self.assertEqual(args.ssh, 'example-controller')
            self.assertEqual(args.merged_dir, '/tmp/merged')
            self.assertEqual(args.state, '/tmp/state.sqlite3')
            self.assertTrue(args.from_end)
            args = w.parse_args(['--file', '/tmp/other.txt=other.txt', '--from-start', '--interval', '0.1'])
            self.assertEqual(args.file, ['/tmp/other.txt=other.txt'])
            self.assertFalse(args.from_end)
            self.assertEqual(args.interval, 0.1)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.merged = self.root / "merged"
        self.state = self.root / "state" / "watch.sqlite3"

    def receiver(self):
        obj = w.Receiver(self.merged); self.addCleanup(obj.close); return obj

    def tailer(self, mappings, from_end=False):
        return w.Tailer(self.state, mappings, from_end)

    def packet(self, identifier, data=b"hello\n", name="result.txt"):
        return {"id": identifier, "filename": name, "data": base64.b64encode(data).decode()}

    def test_multiple_files_mapping_partial_unicode_and_restart(self):
        a, b = self.root / "a.txt", self.root / "b.txt"
        a.write_bytes(b"first\nunfinished"); b.write_text("α\n", encoding="utf-8")
        mappings = [(a, "renamed.txt"), (b, "other.txt")]
        receiver = self.receiver(); tailer = self.tailer(mappings)
        try:
            tailer.capture(); self.assertEqual(tailer.deliver(receiver), 2)
            self.assertEqual((self.merged / "renamed.txt").read_bytes(), b"first\n")
            self.assertEqual((self.merged / "other.txt").read_text(), "α\n")
        finally: tailer.close()
        with a.open("ab") as f: f.write(b" done\nnext\n")
        tailer = self.tailer(mappings)
        try:
            tailer.capture(); tailer.deliver(receiver)
            tailer.capture(); self.assertEqual(tailer.deliver(receiver), 0)
            self.assertEqual((self.merged / "renamed.txt").read_bytes(), b"first\nunfinished done\nnext\n")
        finally: tailer.close()

    def test_failed_delivery_and_lost_ack_retry_without_duplicate(self):
        source = self.root / "a.txt"; source.write_text("one\none\n")
        tailer = self.tailer([(source, "result.txt")]); receiver = self.receiver()
        class LostAck:
            def apply(self, packet): receiver.apply(packet); raise ConnectionError("fixture lost acknowledgement")
        try:
            tailer.capture()
            with self.assertRaises(ConnectionError): tailer.deliver(LostAck())
            self.assertEqual(tailer.db.execute("SELECT COUNT(*) FROM outbox").fetchone()[0], 1)
        finally: tailer.close()
        tailer = self.tailer([(source, "result.txt")])
        try:
            self.assertEqual(tailer.deliver(receiver), 1)
            self.assertEqual((self.merged / "result.txt").read_text(), "one\none\n")
        finally: tailer.close()

    def test_rotation_drains_old_inode_and_handles_missing_file(self):
        source = self.root / "a.txt"; receiver = self.receiver(); tailer = self.tailer([(source, "result.txt")])
        try:
            tailer.capture(); self.assertEqual(tailer.deliver(receiver), 0)
            source.write_text("first\n"); tailer.capture(); tailer.deliver(receiver)
            old = self.root / "old.txt"; source.rename(old)
            with old.open("a") as f: f.write("old-inode-last\n")
            source.write_text("new-inode\n")
            tailer.capture(); tailer.deliver(receiver)
            self.assertEqual((self.merged / "result.txt").read_text(), "first\nold-inode-last\nnew-inode\n")
        finally: tailer.close()

    def test_truncate_and_same_size_rewrite_are_detected(self):
        source = self.root / "a.txt"; source.write_text("one\n")
        tailer = self.tailer([(source, "result.txt")]); receiver = self.receiver()
        try:
            tailer.capture(); tailer.deliver(receiver)
            source.write_text("two\n"); tailer.capture(); tailer.deliver(receiver)
            source.write_text("x\n"); tailer.capture(); tailer.deliver(receiver)
            self.assertEqual((self.merged / "result.txt").read_text(), "one\ntwo\nx\n")
        finally: tailer.close()

    def test_from_end_does_not_publish_old_partial_line(self):
        source = self.root / "a.txt"; source.write_bytes(b"old\nold-partial")
        tailer = self.tailer([(source, "result.txt")], from_end=True); receiver = self.receiver()
        try:
            tailer.capture(); self.assertEqual(tailer.deliver(receiver), 0)
            with source.open("ab") as f: f.write(b"-end\nnew\n")
            tailer.capture(); tailer.deliver(receiver)
            self.assertEqual((self.merged / "result.txt").read_bytes(), b"new\n")
        finally: tailer.close()

    def test_from_end_still_captures_newly_created_files(self):
        source = self.root / "new.txt"
        tailer = self.tailer([(source, "result.txt")], from_end=True); receiver = self.receiver()
        try:
            tailer.capture()
            source.write_text("created-after-start\n")
            tailer.capture(); tailer.deliver(receiver)
            self.assertEqual((self.merged / "result.txt").read_text(), "created-after-start\n")
        finally: tailer.close()

    def test_two_sources_can_merge_into_one_file(self):
        a, b = self.root / "a.txt", self.root / "b.txt"
        a.write_text("from-a\n"); b.write_text("from-b\n")
        tailer = self.tailer([(a, "combined.txt"), (b, "combined.txt")]); receiver = self.receiver()
        try:
            tailer.capture(); tailer.deliver(receiver)
            self.assertEqual((self.merged / "combined.txt").read_text(), "from-a\nfrom-b\n")
        finally: tailer.close()

    def test_receiver_recovers_partial_durable_append_before_new_batch(self):
        receiver = self.receiver(); path = self.merged / "result.txt"
        data = b"recover-this\n"; path.write_bytes(b"recover-"); info = path.stat()
        with receiver.db:
            receiver.db.execute("INSERT INTO receipts VALUES(?,?,?,?,?,?,0,?)", ("old", "result.txt", data, 0, info.st_dev, info.st_ino, hashlib.sha256(data).digest()))
        receiver.apply(self.packet("new", b"after\n"))
        self.assertTrue(receiver.apply(self.packet("old", data))["replayed"])
        self.assertEqual(path.read_bytes(), b"recover-this\nafter\n")

    def test_receiver_preserves_existing_file_and_rejects_unsafe_names(self):
        receiver = self.receiver(); path = self.merged / "result.txt"; path.write_bytes(b"existing")
        receiver.apply(self.packet("first"))
        self.assertEqual(path.read_bytes(), b"existing\nhello\n")
        for name in ["../outside.txt", "/outside.txt", ".hidden.txt", "not-text.bin"]:
            with self.assertRaises(ValueError): receiver.apply(self.packet("bad", name=name))
        with self.assertRaises(RuntimeError): receiver.apply(self.packet("first", b"different\n"))

    def test_stdio_protocol_used_by_ssh_acknowledges_replay(self):
        command = [sys.executable, str(Path(w.__file__)), "--receive", "--merged-dir", str(self.merged)]
        transport = w.SSHReceiver("fixture", self.merged, command=command)
        packet = self.packet("protocol", "unicode ✓\n".encode())
        try:
            self.assertTrue(transport.apply(packet)["ok"])
            transport.close()
            self.assertTrue(transport.apply(packet)["replayed"])
        finally: transport.close()
        self.assertEqual((self.merged / "result.txt").read_text(), "unicode ✓\n")

    def test_daemon_sends_new_line_while_file_remains_open(self):
        source = self.root / "a.txt"; source.touch()
        command = [sys.executable, str(Path(w.__file__)), "--file", str(source)+"=live.txt", "--merged-dir", str(self.merged), "--state", str(self.state), "--interval", "0.05"]
        process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        try:
            with source.open("a") as writer:
                writer.write("live-without-close\n"); writer.flush()
                deadline = time.monotonic()+3
                result = self.merged / "live.txt"
                while not result.exists() or result.read_text() != "live-without-close\n":
                    if process.poll() is not None or time.monotonic() > deadline:
                        self.fail("Live watcher did not deliver while source was open")
                    time.sleep(0.02)
        finally:
            process.terminate(); _, error = process.communicate(timeout=5)
        self.assertEqual(process.returncode, 0, error.decode())


if __name__ == "__main__": unittest.main()
