#!/usr/bin/env python3
"""Read-only check of DengShell development, desktop and packaging tools."""

import argparse
import importlib.util
import json
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent


def probe(command):
    try:
        result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, timeout=20)
        return {"ok": result.returncode == 0, "detail": (result.stdout or result.stderr).strip()[:2000]}
    except (OSError, subprocess.TimeoutExpired) as error:
        return {"ok": False, "detail": str(error)}


def inspect():
    system = platform.system()
    checks = {}
    required_go = re.search(r"^go (\S+)", (ROOT / "go.mod").read_text(), re.M)[1]
    for name, command in {"go": ["go", "version"], "node": ["node", "--version"],
                          "npm": ["npm", "--version"], "node_modules": ["npm", "ls", "--depth=0"]}.items():
        checks[name] = probe(command)
    go_version = re.search(r"go(\d+)\.(\d+)(?:\.(\d+))?", checks["go"]["detail"])
    checks["go"]["ok"] = checks["go"]["ok"] and bool(go_version) and tuple(int(x or 0) for x in go_version.groups()) >= tuple(map(int, required_go.split(".")))
    node_version = re.match(r"v(\d+)", checks["node"]["detail"])
    checks["node"]["ok"] = checks["node"]["ok"] and bool(node_version) and int(node_version[1]) >= 22
    checks["frontend_assets"] = {"ok": all((ROOT / "web/vendor" / name).is_file() for name in
                                               ["xterm.js", "xterm.css", "addon-fit.js", "addon-serialize.js"]),
                                  "detail": "Embedded xterm.js and addons"}
    if system == "Linux":
        for package in ["gtk+-3.0", "webkit2gtk-4.1"]:
            checks[package] = probe(["pkg-config", "--modversion", package])
        checks["c_compiler"] = probe(["cc", "--version"])
    elif system == "Darwin":
        checks["apple_sdk"] = probe(["xcrun", "--sdk", "macosx", "--show-sdk-path"])
        checks["apple_clang"] = probe(["xcrun", "clang", "--version"])
        checks["apple_lipo"] = probe(["xcrun", "--find", "lipo"])
        for tool in ["sips", "iconutil", "codesign", "ditto", "hdiutil"]:
            checks[tool] = {"ok": bool(shutil.which(tool)), "detail": shutil.which(tool) or "missing"}
    optional = {tool: shutil.which(tool) for tool in
                ["ping", "mtr", "ssh", "sshd", "bash", "zsh", "fish", "google-chrome", "xvfb-run", "xdotool",
                 "wmctrl", "docker", "makensis", "rpmbuild", "bsdtar", "zstd", "gh", "actionlint", "shellcheck"]}
    optional.update({"python:" + module: importlib.util.find_spec(module) is not None
                     for module in ["PIL", "fontTools", "gi", "yaml"]})
    return {"system": platform.platform(), "architecture": platform.machine(), "python": sys.version.split()[0],
            "requiredGo": required_go, "checks": checks, "optional": optional,
            "developmentReady": all(result["ok"] for result in checks.values()),
            "macOSNativeBuildAvailable": system == "Darwin" and checks.get("apple_sdk", {}).get("ok", False)}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--json", type=Path, help="Save the complete report")
    args = parser.parse_args()
    report = inspect()
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(report["system"])
    for name, result in report["checks"].items():
        print(f"{'OK' if result['ok'] else 'MISSING'} {name}: {result['detail'].splitlines()[0] if result['detail'] else ''}")
    print("macOS native build:", "available" if report["macOSNativeBuildAvailable"] else "requires a Mac with Apple SDK")
    raise SystemExit(0 if report["developmentReady"] else 1)
