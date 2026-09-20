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
  if (!state?.connected || !state.ready || state.closed || state.detaching || state.restoring || state.ownershipUncertain || typeof text !== 'string') return false;
  state.term.clearSelection();
  // xterm consults the remote program's current bracketed-paste mode, keeping
  // pasted newlines distinct from an explicit command submission.
  const bracketed = state.term.modes.bracketedPasteMode && !state.term.options.ignoreBracketedPasteMode;
  state.term.paste(text);
  // Bracketed paste always needs a CR outside the closing marker. In plain
  // mode a trailing newline already submits the last line; do not submit twice.
  if (execute && (bracketed || !/[\r\n]$/.test(text))) sendInput(state, '\r');
  state.term.focus();
  return true;
}

function pasteTerminalClipboard(state, text) {
  if (!state?.term || typeof text !== 'string') return false;
  const multiline = text.replace(/\r\n?/g, '\n').trim().includes('\n');
  // Do not add a command submission to a full-screen editor or to input for
  // a running program. Shell integration reports the prompt authoritatively;
  // shells without integration retain the normal-screen paste fallback.
  const atPrompt = state.term.buffer.active.type === 'normal' && (!state.shellIntegration?.ready || state.shellIntegration.atPrompt);
  return pasteTerminalText(state, text, { execute: multiline && atPrompt });
}
