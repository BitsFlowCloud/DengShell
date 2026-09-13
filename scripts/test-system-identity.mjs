import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const project = new URL('../', import.meta.url);
const elements = [];
function element(tagName) {
  const classes = new Set();
  const node = {
    tagName, dataset: {}, children: [], textContent: '',
    classList: { add: (...items) => items.forEach(item => classes.add(item)), toggle: (item, on) => on ? classes.add(item) : classes.delete(item), contains: item => classes.has(item) },
    setAttribute(name, value) { this[name] = value; },
    querySelector(selector) { return this.children.find(child => child.className === selector.slice(1)) || null; },
    replaceChildren(...children) { this.children = children; },
    set innerHTML(value) { throw new Error('Remote OS names must not be interpreted as HTML: ' + value); },
  };
  elements.push(node);
  return node;
}
const host = element('span');
host.parentElement = element('h2');
const context = { window: {}, document: { getElementById: id => id === 'system-os' ? host : null, createElement: element } };
vm.runInNewContext(fs.readFileSync(new URL('web/system-identity.js', project), 'utf8'), context);
const api = context.window.DengSystemIdentity;
const cases = [
  ['AlmaLinux 10.2 (Lavender Lion)', 'almalinux'],
  ['Ubuntu 26.04 LTS', 'ubuntu'],
  ['Debian GNU/Linux 13 (trixie)', 'debian'],
  ['Rocky Linux 10.0 (Red Quartz)', 'rockylinux'],
  ['CentOS Stream 10 (Coughlan)', 'centos'],
  ['Red Hat Enterprise Linux 10.0 (Coughlan)', 'redhat'],
  ['RHEL 9', 'redhat'],
  ['Fedora Linux 43 (Server Edition)', 'fedora'],
  ['Alpine Linux v3.22', 'alpinelinux'],
  ['Arch Linux', 'archlinux'],
  ['openSUSE Tumbleweed', 'opensuse'],
  ['SUSE Linux Enterprise Server 15 SP7', 'suse'],
  ['Linux Mint 22.2', 'linuxmint'],
  ['Kali GNU/Linux Rolling', 'kalilinux'],
  ['Oracle Linux Server 9.6', 'linux'],
  ['Research Linux (not Arch)', 'linux'],
  ['Linux 6.15.0-100-generic x86_64', 'linux'],
  ['自定义 Linux 分发版本', 'linux'],
];
for (const [name, logo] of cases) {
  assert.equal(api.identify(name).logo, logo, name);
  api.render(name);
  assert.equal(host.children[1].textContent, name);
  assert.equal(host.children[0].src, `assets/system-logos/${logo}.svg`);
  assert.ok(fs.existsSync(new URL(`web/assets/system-logos/${logo}.svg`, project)));
  assert.equal(host.title, name);
}
for (const empty of [null, undefined, '', '  ', '—', 12, {}]) {
  api.render(empty);
  assert.equal(host.children[1].textContent, '—');
  assert.equal(host.children[0].hidden, true);
}
const injected = '<img src=x onerror="alert(1)"> very long remote name '.repeat(30);
api.render(injected);
assert.equal(host.children[1].textContent, injected.trim());
assert.equal(host.children.length, 2);
assert.equal(host.children[0].src, 'assets/system-logos/linux.svg');
const previousNodes = [...host.children];
api.render(injected);
assert.deepEqual(host.children, previousNodes);
assert.ok(host.classList.contains('system-identity'));
assert.ok(host.parentElement.classList.contains('system-identity-heading'));
console.log(`System identity: ${cases.length} distro/name mappings, empty states, static assets, node reuse and untrusted-name rendering passed.`);
