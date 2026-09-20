#!/usr/bin/env python3
"""Launch the packaged native app with a disposable config and verify readiness."""

import argparse
import hashlib
import json
from pathlib import Path
import platform
import plistlib
import subprocess
import tempfile
import time


def tree_digest(root):
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file()}


def verify(bundle, output):
    if platform.system() != "Darwin":
        raise SystemExit("This native application test requires macOS.")
    bundle = bundle.resolve()
    executable = bundle / "Contents/MacOS/DengShell"
    info = plistlib.loads((bundle / "Contents/Info.plist").read_bytes())
    subprocess.run(["codesign", "--verify", "--deep", "--strict", str(bundle)], check=True)
    arch = "arm64" if platform.machine() == "arm64" else "amd64"
    before = tree_digest(bundle)
    with tempfile.TemporaryDirectory(prefix="dengshell-macos-qa-") as temporary:
        root = Path(temporary)
        config = root / "isolated config"
        config.mkdir()
        log = root / "native.log"
        with log.open("wb") as stream:
            process = subprocess.Popen([str(executable), "--config", str(config)], stdout=stream, stderr=stream)
            try:
                marker = config / f"runtime-success-v1-darwin-{arch}"
                deadline = time.monotonic() + 60
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError(f"Native app exited ({process.returncode}):\n{log.read_text(errors='replace')}")
                    if marker.exists() and marker.read_text() == "DengShell runtime ready v1\n":
                        break
                    time.sleep(0.25)
                else:
                    raise RuntimeError(f"Native frontend did not become ready:\n{log.read_text(errors='replace')}")
                time.sleep(2)
                if process.poll() is not None:
                    raise RuntimeError("Native app exited immediately after readiness")
                if tree_digest(bundle) != before:
                    raise RuntimeError("Application wrote into its signed bundle")
                report = {"nativeReady": True, "platform": platform.platform(), "architecture": arch,
                          "build": info["DengShellBuild"], "bundleUnchanged": True,
                          "isolatedConfiguration": True, "macOS": platform.mac_ver()[0]}
                output.parent.mkdir(parents=True, exist_ok=True)
                output.write_text(json.dumps(report, indent=2) + "\n")
                print(output.read_text())
            finally:
                process.terminate()
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
                output.with_suffix(".log").parent.mkdir(parents=True, exist_ok=True)
                output.with_suffix(".log").write_text(log.read_text(errors="replace"))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("bundle", type=Path)
    parser.add_argument("--output", type=Path, default=Path("build/macos/native-validation.json"))
    args = parser.parse_args()
    verify(args.bundle, args.output)
