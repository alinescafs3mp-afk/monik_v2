#!/usr/bin/env python3
"""Read-only source/runtime hash checks. Importing this module performs no I/O."""
import argparse
import hashlib
import json
import os
from pathlib import Path


def digest(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def check_source(root, state, package):
    manifest = json.loads((package / "PATCH_MANIFEST.json").read_text())
    require(digest(package / "nemotron-exact-tekken.patch") == manifest["patch_sha256"],
            "package patch hash mismatch")
    for name, hashes in manifest["source_files"].items():
        require(digest(root / name) == hashes[state + "_sha256"], name + " hash mismatch")
    for name, expected in manifest["new_files"].items():
        if state == "before":
            require(not os.path.lexists(root / name), name + " already exists")
        else:
            require(digest(root / name) == expected, name + " hash mismatch")
    return {"status": "PASS", "kind": "source", "state": state,
            "note": "Checks patch inputs/outputs; clean full checkout and HEAD are separate checks."}


def check_runtime(root, state, package):
    expected = json.loads((package / "runtime-original.json").read_text())
    actual = {}
    for path in sorted(root.rglob("*")):
        name = str(path.relative_to(root))
        if path.is_symlink():
            actual[name] = {"symlink": os.readlink(path)}
        elif path.is_file():
            actual[name] = {"size": path.stat().st_size, "sha256": digest(path)}
    require(set(actual) == set(expected), "runtime inventory entries differ")
    for name, entry in expected.items():
        if state == "rebuilt" and name == "libllama.so.0.6.0":
            require("sha256" in actual[name], "rebuilt core is not a regular file")
        else:
            require(actual[name] == entry, name + " changed")
    core = actual["libllama.so.0.6.0"]["sha256"]
    if state == "rebuilt":
        require(core != expected["libllama.so.0.6.0"]["sha256"], "rebuilt core still has original bytes")
    return {"status": "PASS", "kind": "runtime", "state": state,
            "core_sha256": core,
            "note": "Rebuilt state permits only new core bytes; ABI/native acceptance remain separate."}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="kind", required=True)
    source = commands.add_parser("source")
    source.add_argument("--root", type=Path, required=True)
    source.add_argument("--state", choices=["before", "after"], required=True)
    runtime = commands.add_parser("runtime")
    runtime.add_argument("--root", type=Path, required=True)
    runtime.add_argument("--state", choices=["original", "rebuilt"], required=True)
    args = parser.parse_args()
    package = Path(__file__).resolve().parent
    check = check_source if args.kind == "source" else check_runtime
    print(json.dumps(check(args.root, args.state, package), indent=2))


if __name__ == "__main__":
    main()
