#!/usr/bin/env python3
"""Write a rendered spec ConfigMap to disk the way kubelet projects it.

Reads the `items` list off the Deployment's `spec` volume rather than dumping
every ConfigMap key, because that mapping is the thing under test: a widget key
is flattened in the ConfigMap and has to land back at its nested path.
"""

import base64
import pathlib
import sys

try:
    import yaml
except ModuleNotFoundError:  # Fail loud: a skipped mount check reads as a passing one.
    sys.exit("materialize-spec-mount needs PyYAML - `uv pip install pyyaml` or run this in dev-base")


def main() -> int:
    rendered, dest = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
    docs = [d for d in yaml.safe_load_all(rendered.read_text()) if d]
    config = next(d for d in docs if d["kind"] == "ConfigMap")
    deployment = next(d for d in docs if d["kind"] == "Deployment")

    volumes = deployment["spec"]["template"]["spec"]["volumes"]
    volume = next(v for v in volumes if v["name"] == "spec")
    items = volume["configMap"].get("items")
    if not items:
        print("spec volume projects the whole ConfigMap, so no item mapping is under test", file=sys.stderr)
        return 1

    text = config.get("data") or {}
    binary = config.get("binaryData") or {}
    for item in items:
        key, target = item["key"], dest / item["path"]
        target.parent.mkdir(parents=True, exist_ok=True)
        if key in text:
            target.write_text(text[key])
        elif key in binary:
            target.write_bytes(base64.b64decode(binary[key]))
        else:
            print(f"volume projects key {key!r}, which the ConfigMap does not hold", file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
