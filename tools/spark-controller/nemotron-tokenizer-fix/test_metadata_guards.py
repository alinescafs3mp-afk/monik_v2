#!/usr/bin/env python3
"""Small synthetic copy-safety controls; no model/tokenizer/corpus inputs required."""
import hashlib
import importlib.util
import os
from pathlib import Path
import struct
import tempfile
import unittest
from unittest import mock


def load_helper():
    path = Path(__file__).with_name("repair_gguf_metadata.py")
    spec = importlib.util.spec_from_file_location("metadata_repair", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class CopyGuards(unittest.TestCase):
    def setUp(self):
        self.helper = load_helper()
        self.tmp = tempfile.TemporaryDirectory(prefix="nemo-copy-guards-")
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name)
        self.source = self.directory / "source.gguf"
        self.destination = self.directory / "new.gguf"
        self.expected = ["<synthetic-%d>" % n for n in range(12)]
        self.types = [3] * 12
        self.payload = bytes(range(256)) * 4
        self.source.write_bytes(self.fixture([""] * 12))
        self.original = self.source.read_bytes()
        # Only the synthetic copy guard tests replace the official-tokenizer reader.
        # Production CLI keeps the original full SHA/defect-set/tokenizer checks.
        patcher = mock.patch.object(self.helper, "official",
                                    return_value=(self.expected, self.types, ["a b"]))
        patcher.start()
        self.addCleanup(patcher.stop)

    def fixture(self, tokens):
        enc = self.helper.encstr
        def string(value):
            return 8, enc(value)
        def strings(values):
            return 9, struct.pack("<IQ", 8, len(values)) + b"".join(map(enc, values))
        values = {
            "general.architecture": string("nemotron_h_moe"),
            "tokenizer.ggml.pre": string("pixtral"),
            "tokenizer.ggml.tokens": strings(tokens),
            "tokenizer.ggml.merges": strings(["a b"]),
            "tokenizer.ggml.token_type": (9, struct.pack("<IQ", 5, 12) + struct.pack("<12i", *self.types)),
            "tokenizer.ggml.bos_token_id": (4, struct.pack("<I", 1)),
            "tokenizer.ggml.eos_token_id": (4, struct.pack("<I", 11)),
            "tokenizer.ggml.unknown_token_id": (4, struct.pack("<I", 0)),
            "tokenizer.ggml.padding_token_id": (4, struct.pack("<I", 11)),
            "synthetic.preserve": string("unaltered metadata"),
        }
        rows = [enc(name) + struct.pack("<I", ty) + data
                for name, (ty, data) in values.items()]
        tensor = enc("synthetic.weight") + struct.pack("<IQIQ", 1, 256, 0, 0)
        prefix = b"GGUF" + struct.pack("<IQQ", 3, 1, len(rows)) + b"".join(rows) + tensor
        return prefix + b"\0" * (-len(prefix) % 32) + self.payload

    def copy(self, **overrides):
        args = dict(expected_source_sha=hashlib.sha256(self.original).hexdigest(),
                    min_free_after=0, strict_snapshot=False)
        args.update(overrides)
        return self.helper.copy_candidate(self.source, self.destination, "unused", "unused", **args)

    def assert_clean_failure(self):
        self.assertFalse(os.path.lexists(self.destination))
        self.assertEqual(list(self.directory.glob("new.gguf.partial-*")), [])
        self.assertEqual(self.source.read_bytes(), self.original)

    def test_new_copy_preserves_source_payload_and_tensor_directory(self):
        result = self.copy()
        old = self.helper.scan_header(self.source)
        new = self.helper.scan_header(self.destination)
        self.assertEqual(self.source.read_bytes(), self.original)
        self.assertEqual(self.destination.read_bytes()[new["payload_offset"]:], self.payload)
        self.assertEqual(new["tensor_raw"], old["tensor_raw"])
        self.assertEqual(new["meta"]["synthetic.preserve"], "unaltered metadata")
        self.assertEqual(new["meta"]["tokenizer.ggml.tokens"], self.expected)
        self.assertFalse(new["meta"]["tokenizer.ggml.add_bos_token"])
        self.assertTrue(result["payload_verified_by_reread"])

    def test_existing_destination_is_preserved(self):
        self.destination.write_bytes(b"KEEP")
        with self.assertRaisesRegex(ValueError, "destination exists"):
            self.copy()
        self.assertEqual(self.destination.read_bytes(), b"KEEP")
        self.assertEqual(self.source.read_bytes(), self.original)

    def test_wrong_source_hash_cleans_partial(self):
        with self.assertRaisesRegex(ValueError, "source full SHA256 mismatch"):
            self.copy(expected_source_sha="0" * 64)
        self.assert_clean_failure()

    def test_nonempty_token_drift_is_rejected(self):
        tokens = [""] * 12
        tokens[0] = "unapproved"
        self.source.write_bytes(self.fixture(tokens))
        self.original = self.source.read_bytes()
        with self.assertRaisesRegex(ValueError, "nonempty token drift"):
            self.copy()
        self.assert_clean_failure()

    def test_disk_payload_corruption_is_detected_and_cleaned(self):
        real_fsync = self.helper.os.fsync
        corrupted = False
        def corrupt_once(fd):
            nonlocal corrupted
            real_fsync(fd)
            if not corrupted:
                corrupted = True
                offset = len(self.helper.plan(self.source, "unused", "unused", False)[1])
                os.pwrite(fd, b"\xff", offset)
                real_fsync(fd)
        with mock.patch.object(self.helper.os, "fsync", side_effect=corrupt_once):
            with self.assertRaisesRegex(ValueError, "written payload differs"):
                self.copy()
        self.assert_clean_failure()

    def test_real_official_reader_rejects_unpinned_tokenizer(self):
        bad = self.directory / "unpinned.json"
        bad.write_text("{}")
        # Retrieve the unmocked implementation from a separate inert module import.
        helper = load_helper()
        with self.assertRaisesRegex(ValueError, "tokenizer hash mismatch"):
            helper.official(bad, "must-not-be-read")


if __name__ == "__main__":
    unittest.main()
