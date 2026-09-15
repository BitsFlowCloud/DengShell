#!/usr/bin/env python3
"""Build DengShell.app and ZIP/DMG packages on macOS using the system SDK."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
MINIMUM_MACOS = "13.0"


def run(*args, **kwargs):
    return subprocess.run([str(arg) for arg in args], check=True, **kwargs)


def build(args):
    if platform.system() != "Darwin":
        raise SystemExit("macOS + Xcode Command Line Tools are required. Run this script on a Mac or the macOS CI workflow.")
    for tool in ["go", "xcrun", "sips", "iconutil", "codesign", "ditto", "hdiutil"]:
        if not shutil.which(tool):
            raise SystemExit(f"Missing build tool: {tool}. Install Go and run xcode-select --install.")
    sdk = run("xcrun", "--sdk", "macosx", "--show-sdk-path", capture_output=True, text=True).stdout.strip()
    run("go", "mod", "verify", cwd=ROOT)
    for name in ["xterm.js", "xterm.css", "addon-fit.js", "addon-serialize.js"]:
        if not (ROOT / "web/vendor" / name).is_file():
            raise SystemExit("Frontend dependencies are missing. Run npm ci && npm run vendor first.")
    source = (ROOT / "internal/app/updates.go").read_text()
    build_number = int(re.search(r"const ApplicationBuild uint64 = (\d+)", source)[1])
    release = int(re.search(r"const ApplicationRelease = (\d+)", source)[1])
    archs = ["arm64", "amd64"] if args.arch == "universal" else [args.arch]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    bundle = output / "DengShell.app"
    if bundle.exists():
        raise SystemExit(f"Output already exists: {bundle}. Choose a new --output directory or move the old build.")
    with tempfile.TemporaryDirectory(prefix="dengshell-macos-build-") as temporary:
        stage = Path(temporary)
        app = stage / "DengShell.app"
        macos = app / "Contents/MacOS"
        resources = app / "Contents/Resources"
        macos.mkdir(parents=True)
        resources.mkdir()
        binaries = []
        for arch in archs:
            binary = stage / f"DengShell-{arch}"
            env = dict(os.environ, GOOS="darwin", GOARCH=arch, CGO_ENABLED="1", SDKROOT=sdk,
                       MACOSX_DEPLOYMENT_TARGET=MINIMUM_MACOS)
            # A fixed minimum keeps the Intel and Apple Silicon slices consistent.
            for flag in ["CGO_CFLAGS", "CGO_LDFLAGS"]:
                env[flag] = f"{env.get(flag, '')} -mmacosx-version-min={MINIMUM_MACOS}".strip()
            run("go", "build", "-buildvcs=false", "-trimpath", "-tags", "desktop,production",
                "-ldflags", "-s -w", "-o", binary, ".", cwd=ROOT, env=env)
            binaries.append(binary)
        executable = macos / "DengShell"
        if len(binaries) > 1:
            run("xcrun", "lipo", "-create", *binaries, "-output", executable)
        else:
            shutil.copy2(binaries[0], executable)
        executable.chmod(0o755)
        run("xcrun", "lipo", "-verify_arch", *["x86_64" if a == "amd64" else a for a in archs], executable)

        iconset = stage / "DengShell.iconset"
        iconset.mkdir()
        for size in [16, 32, 128, 256, 512]:
            for scale in [1, 2]:
                suffix = "@2x" if scale == 2 else ""
                run("sips", "-z", size * scale, size * scale, ROOT / "build/dengshell.png",
                    "--out", iconset / f"icon_{size}x{size}{suffix}.png", stdout=subprocess.DEVNULL)
        run("iconutil", "-c", "icns", iconset, "-o", resources / "DengShell.icns")
        info = {
            "CFBundleName": "DengShell", "CFBundleDisplayName": "DengShell",
            "CFBundleIdentifier": "cloud.bitsflow.dengshell", "CFBundleExecutable": "DengShell",
            "CFBundlePackageType": "APPL", "CFBundleInfoDictionaryVersion": "6.0",
            "CFBundleShortVersionString": "0.1.0",
            "CFBundleVersion": f"{release}.{build_number % 1000 // 100}.{build_number % 100}",
            "DengShellBuild": str(build_number), "CFBundleIconFile": "DengShell.icns",
            "LSMinimumSystemVersion": MINIMUM_MACOS, "NSHighResolutionCapable": True,
            "NSPrincipalClass": "NSApplication", "LSApplicationCategoryType": "public.app-category.developer-tools",
            "NSHumanReadableCopyright": "Copyright © BitsFlowCloud. MIT License.",
            "NSLocalNetworkUsageDescription": "DengShell 使用 SSH、SFTP 和网络诊断连接你选择的局域网服务器。",
        }
        with (app / "Contents/Info.plist").open("wb") as stream:
            plistlib.dump(info, stream)
        (app / "Contents/PkgInfo").write_bytes(b"APPL????")
        shutil.copy2(ROOT / "LICENSE", resources / "LICENSE.txt")
        shutil.copy2(ROOT / "docs/MACOS.md", resources / "macOS-README.md")
        license_dirs = ["build/go-licenses", "build/font-licenses", "web/assets/fonts/licenses",
                        "web/assets/ui-fonts", "web/assets/group-emoji", "web/assets/system-logos", "web/vendor"]
        for folder in license_dirs:
            for path in (ROOT / folder).rglob("*"):
                if path.is_file() and ("license" in path.name.lower() or "ofl" in path.name.lower() or path.name == "ATTRIBUTION.txt"):
                    target = resources / "licenses" / path.relative_to(ROOT)
                    target.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(path, target)
        sign_args = ["--timestamp=none"] if args.sign_identity == "-" else ["--options", "runtime", "--timestamp"]
        run("codesign", "--force", "--sign", args.sign_identity, *sign_args, app)
        run("codesign", "--verify", "--deep", "--strict", "--verbose=2", app)
        shutil.copytree(app, bundle)
        zip_path = output / f"DengShell-macos-{args.arch}.zip"
        run("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", bundle, zip_path)
        disk = stage / "disk"
        disk.mkdir()
        shutil.copytree(app, disk / app.name)
        (disk / "Applications").symlink_to("/Applications")
        shutil.copy2(ROOT / "docs/MACOS.md", disk / "使用说明.md")
        dmg_path = output / f"DengShell-macos-{args.arch}.dmg"
        run("hdiutil", "create", "-volname", "DengShell", "-srcfolder", disk, "-format", "UDZO", dmg_path)
        run("hdiutil", "verify", dmg_path)
        files = [zip_path, dmg_path]
        checksums = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in files}
        (output / "SHA256SUMS.txt").write_text("".join(f"{digest}  {name}\n" for name, digest in checksums.items()))
        metadata = {"build": build_number, "release": release, "architectures": archs,
                    "minimumMacOS": MINIMUM_MACOS, "signature": "ad-hoc" if args.sign_identity == "-" else "Developer ID",
                    "notarized": False, "go": run("go", "version", capture_output=True, text=True).stdout.strip(),
                    "sha256": checksums}
        (output / "build-info.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print(f"Built: {bundle}\nZIP: {zip_path}\nDMG: {dmg_path}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", choices=["universal", "arm64", "amd64"], default="universal")
    parser.add_argument("--output", type=Path, default=ROOT / "build/macos")
    parser.add_argument("--sign-identity", default="-", help="Developer ID identity; default: local ad-hoc signature")
    build(parser.parse_args())
