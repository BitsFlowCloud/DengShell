#!/usr/bin/env bash
set -euo pipefail
dengshell_test_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
python3 - "$dengshell_test_root" <<'PY'
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest

SOURCE = (Path(sys.argv[1]) / 'start.sh').read_text()
CACHE = Path(os.environ.get('XDG_CACHE_HOME', str(Path.home() / '.cache'))) / 'dengshell-launcher-tests'
CACHE.mkdir(parents=True, exist_ok=True)
READY = b'DengShell runtime ready v1\n'

class LauncherTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='launcher-', dir=CACHE)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.release = self.root / 'os-release'
        self.release.write_text('ID=ubuntu\nVERSION_ID="26.04"\nID_LIKE=debian\n')
        source = SOURCE.replace('dengshell_os_release=/etc/os-release', 'dengshell_os_release=' + shlex.quote(str(self.release)))
        (self.root / 'start.sh').write_text(source)
        for tool in ['bash', 'dirname', 'cmp', 'chmod']:
            (self.bin / tool).symlink_to(shutil.which(tool))
        self.tool('uname', 'printf "%s\\n" "${DENG_TEST_ARCH:-x86_64}"')
        self.tool('ldd', 'printf "called\\n" >> "$DENG_TEST_LDD_LOG"\nprintf "%s\\n" "$DENG_TEST_LDD"\nexit "${DENG_TEST_LDD_STATUS:-0}"')
        self.tool('ping', 'exit 0')
        self.tool('mtr', 'exit 0')
        self.tool('zenity', 'printf "%s\\n" "$@" > "$DENG_TEST_DIALOG"')
        self.program = self.root / 'dengshell'
        self.program.write_text('#!/bin/bash\nprintf "%s\\0" "$@" > "$DENG_TEST_RUN"\n')
        self.program.chmod(0o755)
        self.build_log = self.root / 'build.log'
        (self.root / 'build.sh').write_text('#!/bin/bash\nprintf "unexpected build\\n" > "' + str(self.build_log) + '"\nexit 79\n')
        (self.root / 'build.sh').chmod(0o755)
        self.env = os.environ.copy()
        self.env.update(PATH=str(self.bin), XDG_CONFIG_HOME=str(self.root / 'xdg'), DISPLAY=':test',
                        DENG_TEST_RUN=str(self.root / 'run.log'), DENG_TEST_DIALOG=str(self.root / 'dialog.log'),
                        DENG_TEST_LDD_LOG=str(self.root / 'ldd.log'),
                        DENG_TEST_LDD='libgtk-3.so.0 => /lib/libgtk-3.so.0 (0x1234)')

    def tool(self, name, body):
        path = self.bin / name
        path.write_text('#!/bin/bash\n' + body + '\n')
        path.chmod(0o755)

    def run_launcher(self, args=(), **updates):
        return subprocess.run(['/bin/bash', str(self.root / 'start.sh'), *args], env={**self.env, **updates},
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=5)

    def marker(self, config=None, arch='amd64'):
        directory = Path(config) if config else self.root / 'data'
        if not config: directory.mkdir(parents=True, exist_ok=True)
        return directory / ('runtime-success-v1-linux-' + arch)

    def test_success_preserves_arguments_without_go_or_writing_marker(self):
        args = ['--config', str(self.root / 'config with spaces'), '-addr=127.0.0.1:0', '--dev']
        result = self.run_launcher(args)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / 'run.log').read_bytes().split(b'\0')[:-1], [a.encode() for a in args])
        self.assertFalse(self.marker(args[1]).exists())
        self.assertFalse(self.build_log.exists())

    def test_exact_success_marker_skips_all_checks_for_config_variants(self):
        for style in ['-config', '--config', '-config=', '--config=']:
            with self.subTest(style=style):
                config = self.root / ('config ' + style.strip('-='))
                marker = self.marker(config)
                marker.parent.mkdir(exist_ok=True)
                marker.write_bytes(READY)
                args = [style + str(config)] if style.endswith('=') else [style, str(config)]
                result = self.run_launcher(args, DENG_TEST_LDD='libgtk-3.so.0 => not found')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertFalse((self.root / 'ldd.log').exists())
        for tool in ['ldd', 'ping', 'mtr']:
            (self.bin / tool).unlink()
        marker = self.marker()
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_bytes(READY)
        self.assertEqual(self.run_launcher().returncode, 0)

    def test_invalid_marker_rechecks_and_never_launches_on_missing_tools(self):
        marker = self.marker()
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_bytes(READY + b'\n')
        (self.bin / 'ping').unlink()
        (self.bin / 'mtr').unlink()
        result = self.run_launcher()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('iputils-ping', result.stderr)
        self.assertNotIn('mtr-tiny', result.stderr)
        self.assertFalse((self.root / 'run.log').exists())
        self.assertEqual(marker.read_bytes(), READY + b'\n')
        self.assertTrue((self.root / 'dialog.log').exists())

    def test_missing_mtr_is_optional_until_diagnostics_is_requested(self):
        (self.bin / 'mtr').unlink()
        result = self.run_launcher()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / 'run.log').exists())
        self.assertFalse((self.root / 'dialog.log').exists())
        self.assertFalse(self.marker().exists())

    def test_missing_library_and_browser_still_check_desktop_elf(self):
        for args in [[], ['--browser'], ['-browser=true']]:
            with self.subTest(args=args):
                result = self.run_launcher(args, DENG_TEST_LDD='libwebkit2gtk-4.1.so.0 => not found')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('libgtk-3-0t64 libwebkit2gtk-4.1-0', result.stderr)
                self.assertFalse((self.root / 'run.log').exists())
                self.assertFalse(self.marker().exists())

    def test_library_dialog_only_contains_actionable_failures_and_is_bounded(self):
        libraries = '\n'.join(f'libavailable-{i}.so => /lib/libavailable-{i}.so (0x123)' for i in range(120))
        failures = '\n'.join(f'libmissing-{i}.so => not found' for i in range(25))
        result = self.run_launcher(DENG_TEST_LDD=libraries + '\n' + failures)
        self.assertNotEqual(result.returncode, 0)
        dialog = (self.root / 'dialog.log').read_text()
        self.assertNotIn('libavailable-', dialog)
        self.assertIn('libmissing-0.so', dialog)
        self.assertIn('libmissing-19.so', dialog)
        self.assertNotIn('libmissing-20.so', dialog)
        self.assertIn('ldd ./dengshell', dialog)
        result = self.run_launcher(DENG_TEST_LDD=libraries + "\n./dengshell: version `GLIBC_2.43' not found", DENG_TEST_LDD_STATUS='1')
        self.assertNotEqual(result.returncode, 0)
        dialog = (self.root / 'dialog.log').read_text()
        self.assertNotIn('libavailable-', dialog)
        self.assertIn('GLIBC_2.43', dialog)

    def test_glibc_and_glibcxx_incompatibility_need_matching_build(self):
        for version in ['GLIBC_2.43', 'GLIBCXX_3.4.33']:
            with self.subTest(version=version):
                result = self.run_launcher(DENG_TEST_LDD=f"./dengshell: /lib/libc.so.6: version `{version}' not found (required by ./dengshell)", DENG_TEST_LDD_STATUS='1')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('不兼容', result.stderr)
                self.assertIn('重新构建', result.stderr)
                self.assertNotIn('sudo apt install', result.stderr)
                self.assertFalse((self.root / 'run.log').exists())

    def test_distro_specific_package_guidance_and_unknown_fallback(self):
        for release, expected, forbidden in [
            ('ID=ubuntu\nVERSION_ID=22.04\n', 'sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0', 'libgtk-3-0t64'),
            ('ID=debian\nVERSION_ID=12\n', 'sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0', 'libgtk-3-0t64'),
            ('ID=debian\nVERSION_ID=13\n', 'sudo apt install libgtk-3-0t64 libwebkit2gtk-4.1-0', 'sudo dnf'),
            ('ID=fedora\nVERSION_ID=44\n', 'sudo dnf install gtk3 webkit2gtk4.1 iputils', 'sudo apt'),
            ('ID=endeavouros\nID_LIKE="arch"\n', 'sudo pacman -S --needed gtk3 webkit2gtk-4.1 iputils', 'sudo apt'),
            ('ID=opensuse-tumbleweed\nID_LIKE="opensuse suse"\n', 'sudo zypper install libgtk-3-0 libwebkit2gtk-4_1-0 iputils', 'sudo apt'),
            ('ID=rocky\nID_LIKE="rhel centos fedora"\nVERSION_ID=9\n', '默认仓库缺少', 'sudo dnf'),
            ('ID=alpine\nVERSION_ID=3.22\n', '不支持 Alpine 的 musl', 'sudo apt'),
        ]:
            with self.subTest(release=release):
                self.release.write_text(release)
                for tool in ['ping', 'mtr']:
                    (self.bin / tool).unlink(missing_ok=True)
                result = self.run_launcher(DENG_TEST_LDD='libgtk-3.so.0 => not found')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(expected, result.stderr)
                self.assertNotIn(forbidden, result.stderr)

    def test_installed_layout_defaults_to_user_config_and_preserves_explicit_override(self):
        support = self.root / 'data/support'
        support.mkdir(parents=True)
        (support / 'installed-layout').write_text('installed\n')
        result = self.run_launcher(['--dev'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / 'run.log').read_bytes().split(b'\0')[:-1],
                         [b'--config', str(self.root / 'xdg/dengshell').encode(), b'--dev'])
        result = self.run_launcher(['--config', 'custom', '--dev'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / 'run.log').read_bytes().split(b'\0')[:-1],
                         [b'--config', b'custom', b'--dev'])

    def test_relative_config_arm64_and_missing_execute_bit(self):
        config = self.root / 'relative config'
        marker = self.marker(config, 'arm64')
        marker.parent.mkdir()
        marker.write_bytes(READY)
        self.program.chmod(0o644)
        result = self.run_launcher(['--config=relative config'], DENG_TEST_ARCH='aarch64', DENG_TEST_LDD='bad runtime')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.build_log.exists())
        self.assertFalse((self.root / 'ldd.log').exists())

    def test_dialog_fallback_and_no_display_stderr(self):
        (self.bin / 'zenity').unlink()
        self.tool('kdialog', 'printf "%s\\n" "$@" > "$DENG_TEST_DIALOG"')
        result = self.run_launcher(DENG_TEST_LDD='libgtk-3.so.0 => not found')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('--error', (self.root / 'dialog.log').read_text())
        (self.root / 'dialog.log').unlink()
        result = self.run_launcher(DENG_TEST_LDD='libgtk-3.so.0 => not found', DISPLAY='', WAYLAND_DISPLAY='')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('libgtk-3.so.0', result.stderr)
        self.assertFalse((self.root / 'dialog.log').exists())

unittest.main(argv=[sys.argv[0]], verbosity=2)
PY
