'use strict';

// Shell-confirmed commands only. Never infer history from keys, echoed screen
// contents, password prompts, pasted input, or a terminal application's input.
function bindShellIntegration(state) {
  state.shellIntegration = { shell: '', nonce: '', ready: false, atPrompt: false };
  state.term.parser.registerOscHandler(777, data => {
    const parts = data.split(';');
    if (parts[0] !== 'DengShell') return false;
    const integration = state.shellIntegration;
    if (!integration.nonce || parts[2] !== integration.nonce) return true;
    switch (parts[1]) {
      case 'ready': integration.ready = true; integration.shell = parts[3] || integration.shell; break;
      case 'prompt': integration.atPrompt = true; break;
      case 'busy': integration.atPrompt = false; break;
      case 'command':
      case 'cwd':
        if (parts.length !== 4 || parts[3].length > 90000 || !/^[A-Za-z0-9+/]*={0,2}$/.test(parts[3])) return true;
        try {
          const bytes = Uint8Array.from(atob(parts[3]), character => character.charCodeAt(0));
          const command = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
          if (command.length <= 16384 && !command.includes('\0')) {
            if (parts[1] === 'command') recordCommand(state, command);
            else if (command.startsWith('/')) {
              state.terminalDirectory = command;
              if (state.follow && command !== state.cwd) navigate(command, state).catch(error => toast(error?.message || String(error)));
            }
          }
        } catch {}
        break;
    }
    return true;
  });
}

function acceptShellIntegration(state, metadata) {
  if (!state.shellIntegration || !metadata || !/^[a-f0-9]{48}$/.test(metadata.nonce || '')) return;
  state.shellIntegration.nonce = metadata.nonce;
  state.shellIntegration.shell = metadata.shell;
  state.promptUsername = metadata.promptUsername || state.promptUsername;
  state.promptHostname = metadata.promptHostname || state.promptHostname;
  if (typeof reflectPromptPreview === 'function') reflectPromptPreview();
}

function pasteTerminalText(state, text, { execute = false } = {}) {
  if (!state?.ready || typeof text !== 'string') return false;
  state.term.clearSelection();
  // xterm consults the remote program's current bracketed-paste mode, keeping
  // pasted newlines distinct from an explicit command submission.
  state.term.paste(text);
  if (execute) sendInput(state, '\r');
  state.term.focus();
  return true;
}
