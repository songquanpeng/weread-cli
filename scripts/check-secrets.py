#!/usr/bin/env python3
"""Small dependency-free guard for accidental secrets/state in public source.

Not a replacement for GitHub secret scanning. It never prints matched values.
"""
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(__file__).resolve().parents[1]
files = subprocess.check_output(
    ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=root
).decode().split("\0")
patterns = [
    re.compile(rb"wrk-[A-Za-z0-9_-]{16,}"),
    re.compile(rb"(?:ghp|gho|ghu|ghs|github_pat)_[A-Za-z0-9_]{20,}"),
    re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    re.compile(rb"AKIA[0-9A-Z]{16}"),
]
blocked = {"auth.json", "credentials.json", ".zshrc", ".bashrc", ".env"}
bad = set()
for name in set(files) - {""}:
    p = pathlib.Path(name)
    if p.name in blocked or re.search(r"\.(?:db|sqlite)(?:3)?(?:-(?:wal|shm))?$", name):
        bad.add(name)
        continue
    path = root / p
    blobs = []
    if path.is_file():
        blobs.append(path.read_bytes())
    staged = subprocess.run(
        ["git", "show", ":" + name], cwd=root, capture_output=True
    )
    if staged.returncode == 0:
        blobs.append(staged.stdout)
    for data in blobs:
        if any(pattern.search(data) for pattern in patterns):
            bad.add(name)
if bad:
    print("Possible sensitive files (values withheld):", file=sys.stderr)
    for name in sorted(bad):
        print(name, file=sys.stderr)
    sys.exit(1)
print("Secret/state guard passed.")
