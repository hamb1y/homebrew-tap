"""Update the pinned Foxhand formula from a stable tag and its release checksums."""

import re
import sys
from pathlib import Path


def version_tuple(version):
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}", version):
        raise ValueError(f"Not a stable release version: {version}")
    return tuple(map(int, version.split(".")))


tag, sums = sys.argv[1:]
if not tag.startswith("v"):
    raise ValueError(f"Expected a v-prefixed release tag, got {tag}")
version = tag[1:]
new = version_tuple(version)
formula = Path(__file__).resolve().parents[1] / "Formula/foxhand-helper.rb"
original = formula.read_text()
version_field = re.compile(r'^  version "([^"]+)"$', re.M)
versions = version_field.findall(original)
if len(versions) != 1:
    raise ValueError("Expected exactly one version in the formula")
if new <= version_tuple(versions[0]):
    print(f"Foxhand {versions[0]} is already current or newer than {version}.")
    sys.exit(0)

checksums = Path(sums).read_text().splitlines()
updated = version_field.sub(f'  version "{version}"', original)
for target in ("darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"):
    asset = f"foxhand-host-{target}"
    hashes = [
        match[1]
        for line in checksums
        if (match := re.fullmatch(r"([0-9a-f]{64}) [ *]" + re.escape(asset), line))
    ]
    if len(hashes) != 1:
        raise ValueError(f"Expected exactly one checksum for {asset}")
    field = r'(-' + re.escape(target) + r'"\n      sha256 ")[0-9a-f]{64}(")'
    updated, count = re.subn(field, lambda m: m[1] + hashes[0] + m[2], updated)
    if count != 1:
        raise ValueError(f"Expected exactly one formula entry for {asset}")

formula.write_text(updated)
