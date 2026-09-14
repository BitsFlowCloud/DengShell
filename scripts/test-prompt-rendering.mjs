import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import xterm from '@xterm/xterm';
const { Terminal } = xterm;

// Expand actual Bash/Zsh prompts and feed their bytes into the application's
// xterm parser. No user shell startup files, SSH servers or GUI are involved.
const fixtures = [
  { name: 'Ubuntu Bash title', shell: 'bash', prompt: String.raw`\[\e]0;\u@\h: \w\a\]\u@\h:\w\$ ` },
  { name: 'Ubuntu Bash colored prompt', shell: 'bash', prompt: String.raw`\[\e]0;\u@\h: \w\a\]\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ ` },
  { name: 'Bash ST title', shell: 'bash', prompt: String.raw`\[\033]2;\u@\H: \w\033\\\]\u@\H:\w\$ ` },
  { name: 'Zsh title', shell: 'zsh', prompt: '%{\x1b]0;%n@%m: %~\x07%}%n@%m:%~%# ' },
  { name: 'Zsh ST title', shell: 'zsh', prompt: '%{\x1b]2;%n@%M: %~\x1b\\%}%n@%M:%~%# ' },
];
const hookDir = process.env.DENGSHELL_PROMPT_HOOK_DIR || fileURLToPath(new URL('../internal/app/shell_integration/', import.meta.url));
const directory = mkdtempSync(path.join(tmpdir(), 'dengshell-prompt-'));

async function render(data) {
  const term = new Terminal({ cols: 240, rows: 4, allowProposedApi: true });
  const titles = [];
  term.onTitleChange(title => titles.push(title));
  try {
    await new Promise(resolve => term.write(data, resolve));
    const line = term.buffer.active.getLine(0);
    return { text: line.translateToString(true), titles, colors: Array.from({ length: line.length }, (_, i) => {
      const cell = line.getCell(i);
      return cell.isFgRGB() ? cell.getFgColor() : null;
    }) };
  } finally { term.dispose(); }
}

try {
  for (const fixture of fixtures) {
    const style = path.join(directory, 'style'), hook = path.join(directory, 'hook');
    writeFileSync(style, '#112233\n#abcdef\n', { mode: 0o600 });
    writeFileSync(hook, readFileSync(path.join(hookDir, `prompt-${fixture.shell}.sh`), 'utf8').replaceAll('@DENGSHELL_STYLE_FILE@', style), { mode: 0o600 });
    const print = fixture.shell === 'bash' ? 'printf "%s\\0" "${PS1@P}"' : 'print -nrP -- "$PS1"; printf "\\0"';
    const script = `PS1=$DENGSHELL_TEST_PROMPT
. "$DENGSHELL_TEST_HOOK"
${print}
__dengshell_apply_prompt_style
__dengshell_apply_prompt_style
${print}
printf '\\n#445566\\n' > "$DENGSHELL_TEST_STYLE"
__dengshell_apply_prompt_style
${print}
printf '\\n\\n' > "$DENGSHELL_TEST_STYLE"
__dengshell_apply_prompt_style
${print}`;
    const flags = fixture.shell === 'bash' ? ['--noprofile', '--norc'] : ['-f'];
    const result = spawnSync(process.env[`DENGSHELL_TEST_${fixture.shell.toUpperCase()}`] || fixture.shell, [...flags, '-c', script], {
      cwd: directory, encoding: 'utf8', timeout: 10000,
      env: { ...process.env, HOME: directory, ZDOTDIR: directory, TERM: 'xterm-256color', DENGSHELL_TEST_PROMPT: fixture.prompt, DENGSHELL_TEST_HOOK: hook, DENGSHELL_TEST_STYLE: style },
    });
    assert.equal(result.status, 0, `${fixture.name}: ${result.error || result.stderr}`);
    const pieces = result.stdout.split('\0');
    assert.equal(pieces.length, 5);
    const [baseline, colored, changed, restored] = await Promise.all(pieces.slice(0, 4).map(render));
    assert.equal((baseline.text.match(/@/g) || []).length, 1, `${fixture.name}: ${JSON.stringify({ text: baseline.text, raw: pieces[0] })}`);
    assert.equal(baseline.titles.length, 1);
    for (const rendered of [colored, changed, restored]) {
      assert.equal(rendered.text, baseline.text, `${fixture.name}: hidden title leaked into visible prompt`);
      assert.deepEqual(rendered.titles, baseline.titles, `${fixture.name}: window title changed`);
    }
    const hostColumn = baseline.text.indexOf('@') + 1;
    assert.equal(colored.colors[0], 0x112233);
    assert.equal(colored.colors[hostColumn], 0xabcdef);
    assert.equal(changed.colors[hostColumn], 0x445566);
    assert.deepEqual(restored.colors, baseline.colors);
    console.log(`PASS ${fixture.name}: one visible identity, intact title, live colors and exact reset`);
  }
} finally { rmSync(directory, { recursive: true, force: true }); }
