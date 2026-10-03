#!/usr/bin/env python3
"""Run the packaged AI child in real macOS WebKit against a fake local owner.

No model, SSH, user configuration, or user credential is used. The fixture boot
script observes the packaged frontend DOM and exercises the native pin bridge.
It does not claim end-to-end validation of a real AI provider or SSH session.
"""
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import platform
import plistlib
import secrets
import subprocess
import tempfile
import threading
import time
from urllib.parse import parse_qs, urlsplit


OBSERVER = r"""
;(async () => {
  const until = Date.now() + 45000;
  const sleep = () => new Promise(resolve => setTimeout(resolve, 50));
  const check = (value, message) => {if (!value) throw new Error(message);};
  let result;
  try {
    while (!window.DengAIWindowTitlebar?.ready || !document.querySelector('.ai-message-assistant table')) {
      if (Date.now() > until) throw new Error('Packaged AI WebKit frontend did not become ready');
      await sleep();
    }
    const row = document.querySelector('.ai-message-assistant');
    check(window.DENG_AI_CHILD === true, 'Child mode not active');
    check(location.protocol === 'wails:' || location.hostname === 'wails.localhost', 'Not real Wails/WebKit asset origin');
    check(!document.querySelector('#workspace'), 'SSH workspace was loaded in AI child');
    check(row.querySelector('.ai-message-caption').textContent === 'Native Smoke Model', 'Model caption missing');
    check(row.querySelector('tbody strong')?.textContent === '节点-001', 'Markdown table/strong not rendered');
    check(row.querySelector('pre code')?.textContent === 'printf "AI smoke"\n', 'Code content changed');
    check(!row.querySelector('img, iframe, script, svg'), 'Unsafe Markdown node created');
    check(row.textContent.includes('<img src=x onerror=window.__unsafe=1>'), 'Raw HTML was not preserved as text');
    check(!window.__unsafe, 'Raw HTML executed');
    check(row.querySelector('.ai-message-source-toggle') && row.querySelector('.ai-message-copy'), 'Original/copy controls missing');
    check(document.querySelector('#ai-window-titlebar')?.hidden === false, 'Native titlebar hidden');
    check(document.querySelector('#ai-pin') && document.querySelector('#ai-window-close'), 'Native window controls missing');
    const desktop = window.go.main.Desktop;
    const state = await desktop.WindowState();
    check(state.platform === 'darwin' && state.frameless === true, 'Unexpected native window state');
    const pinned = await desktop.SetAIWindowAlwaysOnTop(true);
    const unpinned = await desktop.SetAIWindowAlwaysOnTop(false);
    check(pinned === true && unpinned === false, 'Native pin/unpin bridge failed');
    result = {passed: true, originProtocol: location.protocol, userAgent: navigator.userAgent,
      packagedChildFrontend: true, nativeTitlebarReady: true, markdownTableAndCode: true,
      rawHTMLInert: true, originalAndCopyControls: true, pinBridgeReturnedExpectedState: true,
      windowState: state, fixtureBackend: true};
  } catch (error) { result = {passed: false, error: error.stack || String(error), userAgent: navigator.userAgent}; }
  while (!window.go?.main?.Desktop?.Request && Date.now() < until) await sleep();
  if (window.go?.main?.Desktop?.Request)
    await window.go.main.Desktop.Request('POST', '/api/ai-smoke/result', JSON.stringify(result));
})();
"""


def digest_tree(root):
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob('*') if path.is_file()}


def verify(bundle, output):
    if platform.system() != 'Darwin':
        raise SystemExit('This test requires macOS Cocoa/WebKit; it cannot run on Linux.')
    bundle, output = bundle.resolve(), output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    executable = bundle / 'Contents/MacOS/DengShell'
    plist = plistlib.loads((bundle / 'Contents/Info.plist').read_bytes())
    token, room = secrets.token_hex(24), secrets.token_hex(24)
    room_path = '/api/ai/windows/' + room
    before = digest_tree(bundle)
    lock = threading.Lock()
    received = threading.Event()
    state = {'events': [], 'seq': 0, 'result': None, 'requests': [], 'closed': False}
    provider = {'id': 'native-smoke-fixture', 'name': 'Local fixture', 'format': 'openai',
                'baseURL': 'https://invalid.example.test/v1', 'model': 'Native Smoke Model',
                'hasKey': False, 'proxy': {'type': 'direct'}}
    settings = {'provider': provider['id'], 'timeout': 120, 'providers': [provider]}
    text = '# 原生 AI 窗口\n\n| ID | 状态 |\n|---|---|\n| **节点-001** | 正常 |\n\n```sh\nprintf "AI smoke"\n```\n\n<img src=x onerror=window.__unsafe=1>\n'
    snapshot = {'type': 'state', 'closed': False, 'enabled': False, 'busy': False,
                'settings': settings, 'settingsBusy': False, 'theme': 'light',
                'target': '目标：原生烟测模拟工作区', 'logRevision': 1,
                'events': [{'role': 'assistant', 'value': text, 'model': provider['model'],
                            'sourceLength': len(text), 'truncated': False, 'sourceAvailable': True}]}

    def enqueue(payload):
        state['seq'] += 1
        state['events'].append({'seq': state['seq'], 'payload': payload})

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def respond(self, value, status=200, content_type='application/json'):
            data = value.encode() if isinstance(value, str) else json.dumps(value, ensure_ascii=False).encode()
            self.send_response(status)
            self.send_header('Content-Type', content_type)
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def handle_request(self):
            if self.headers.get('X-CloudShell-Token') != token:
                self.respond({'error': 'fixture authentication failed'}, 403)
                return
            parsed = urlsplit(self.path)
            with lock:
                state['requests'].append({'method': self.command, 'path': parsed.path})
            if parsed.path == '/boot.js' and self.command == 'GET':
                boot = {'token': token, 'base': '', 'theme': 'light', 'startupAnimation': False,
                        'securityLock': {'enabled': False, 'locked': False}}
                self.respond('window.CLOUDSHELL = ' + json.dumps(boot) + ';\n' + OBSERVER,
                             content_type='application/javascript; charset=utf-8')
                return
            if parsed.path in ['/api/security-lock/access', '/api/windows/alive', '/api/security-lock/activity']:
                self.respond({'enabled': False, 'locked': False})
                return
            if parsed.path == '/api/config':
                self.respond({'appearance': {}})
                return
            if parsed.path == room_path and self.command == 'GET':
                query = parse_qs(parsed.query)
                after = int(query.get('after', ['0'])[0])
                with lock:
                    events = [] if query.get('probe') == ['1'] else [e for e in state['events'] if e['seq'] > after]
                    value = {'closed': state['closed'], 'events': events}
                self.respond(value)
                return
            if parsed.path == room_path and self.command == 'DELETE':
                with lock:
                    state['closed'] = True
                self.respond({'ok': True})
                return
            if self.command != 'POST':
                self.respond({'error': 'unsupported fixture request'}, 404)
                return
            size = int(self.headers.get('Content-Length', '0'))
            if size < 0 or size > 1_000_000:
                self.respond({'error': 'fixture request too large'}, 413)
                return
            body = json.loads(self.rfile.read(size))
            if parsed.path == '/api/ai-smoke/result':
                with lock:
                    state['result'] = body
                received.set()
                self.respond({'ok': True})
                return
            if parsed.path == room_path + '/messages':
                payload = body['payload']
                assert body['side'] == 'assistant' and payload['type'] == 'request'
                with lock:
                    action = payload['action']
                    if action == 'hello':
                        enqueue(snapshot)
                        enqueue({'type': 'result', 'id': payload['id'], 'value': {'ok': True}})
                    elif action == 'get_settings':
                        enqueue({'type': 'result', 'id': payload['id'], 'value': settings})
                    elif action == 'stop':
                        enqueue({'type': 'result', 'id': payload['id'], 'value': {'ok': True}})
                    else:
                        enqueue({'type': 'result', 'id': payload['id'], 'error': 'Action not allowed by native smoke fixture'})
                self.respond({'ok': True})
                return
            self.respond({'error': 'unsupported fixture request'}, 404)

        do_GET = handle_request
        do_POST = handle_request
        do_DELETE = handle_request

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    result = {'passed': False, 'build': plist['DengShellBuild'],
              'architecture': 'arm64' if platform.machine() == 'arm64' else 'amd64',
              'macOS': platform.mac_ver()[0], 'platform': platform.platform(),
              'scope': 'Real packaged Cocoa/WebKit AI child; fake local owner relay; no AI provider or SSH connection'}
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix='dengshell-macos-ai-smoke-') as temporary:
            root = Path(temporary)
            config = root / 'isolated config'
            config.mkdir()
            with output.with_suffix('.log').open('wb') as log:
                process = subprocess.Popen([str(executable), '--ai-window'], stdin=subprocess.PIPE,
                                           stdout=log, stderr=log, cwd=executable.parent)
                launch = {'base': f'http://127.0.0.1:{server.server_port}', 'token': token,
                          'configDir': str(config), 'aiWindowId': room}
                process.stdin.write((json.dumps(launch) + '\n').encode())
                process.stdin.flush()
                deadline = time.monotonic() + 65
                while not received.wait(0.1):
                    if process.poll() is not None:
                        raise RuntimeError(f'AI native process exited before frontend ready: {process.returncode}')
                    if time.monotonic() > deadline:
                        raise RuntimeError('No AI native WebKit observer result within 65 seconds')
                with lock:
                    observed = state['result']
                result['frontend'] = observed
                if not observed.get('passed'):
                    raise RuntimeError('AI WebKit check failed: ' + str(observed.get('error')))
                process.stdin.write(b'{"action":"focus"}\n')
                process.stdin.flush()
                time.sleep(0.3)
                if process.poll() is not None:
                    raise RuntimeError('AI native process exited unexpectedly after readiness')
                # EOF is the real private-pipe owner-exit contract.
                process.stdin.close()
                code = process.wait(timeout=12)
                if code != 0:
                    raise RuntimeError(f'AI native process failed during owner-exit shutdown: {code}')
                result['privatePipeFocusAndOwnerExit'] = True
                result['bundleUnchanged'] = digest_tree(bundle) == before
                if not result['bundleUnchanged']:
                    raise RuntimeError('AI child wrote into its signed application bundle')
                result['isolatedConfiguration'] = True
                result['passed'] = True
    except Exception as error:
        result['error'] = str(error)
        raise
    finally:
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)
        # Paths are sufficient evidence; never serialize fixture capability values.
        result['observedRequests'] = state['requests']
        output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
        print(output.read_text())


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bundle', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    options = parser.parse_args()
    verify(options.bundle, options.output)
