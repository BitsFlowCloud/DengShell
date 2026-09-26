/* Distro identity uses the OS name already received by the monitor. No extra probes. */
(() => {
  'use strict';
  const distributions = [
    ['almalinux', /^alma\s*linux\b/i],
    ['rockylinux', /^rocky(?:\s+linux)?\b/i],
    ['linuxmint', /^linux\s+mint\b/i],
    ['kalilinux', /^kali(?:\s+gnu\/linux|\s+linux)?\b/i],
    ['ubuntu', /^ubuntu\b/i],
    ['debian', /^debian\b/i],
    ['centos', /^centos\b/i],
    ['redhat', /^red\s*hat\b|^rhel\b/i],
    ['fedora', /^fedora\b/i],
    ['alpinelinux', /^alpine\b/i],
    ['archlinux', /^arch(?:\s+linux)?\b/i],
    ['opensuse', /^opensuse\b/i],
    ['suse', /^suse\b|^sles\b/i],
  ];
  function identify(value) {
    const name = typeof value === 'string' ? value.trim() : '';
    return { name: name || '—', logo: name && name !== '—' ? (distributions.find(([, match]) => match.test(name))?.[0] || 'linux') : null };
  }
  function render(value) {
    const host = document.getElementById('system-os');
    if (!host) return;
    const { name, logo } = identify(value);
    host.classList.add('system-identity');
    host.parentElement?.classList.add('system-identity-heading');
    host.classList.toggle('is-empty', !logo);
    host.title = logo ? name : '';
    host.dataset.distribution = logo || '';
    // Monitor events arrive frequently; keep the same image and text nodes when possible.
    let image = host.querySelector('.system-identity-logo');
    let label = host.querySelector('.system-identity-name');
    if (!image || !label) {
      image = document.createElement('img');
      image.className = 'system-identity-logo';
      image.alt = '';
      image.setAttribute('aria-hidden', 'true');
      image.draggable = false;
      image.width = 16;
      image.height = 16;
      image.onerror = () => {
        // A damaged/missing distro asset must not obscure the actual OS name.
        if (image.dataset.logo !== 'linux') {
          image.dataset.logo = 'linux';
          image.src = 'assets/system-logos/linux.svg';
        } else image.hidden = true;
      };
      label = document.createElement('span');
      label.className = 'system-identity-name';
      host.replaceChildren(image, label);
    }
    image.hidden = !logo;
    if (logo && image.dataset.logo !== logo) {
      image.dataset.logo = logo;
      image.src = `assets/system-logos/${logo}.svg`;
    }
    if (label.textContent !== name) label.textContent = name;
  }
  window.DengSystemIdentity = Object.freeze({ identify, render });
})();
