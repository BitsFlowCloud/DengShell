// Android layout adapter. Existing DengShell screens and API handlers remain the source of truth.
(() => {
  const body = document.body;
  body.classList.add('dengshell-android');
  const agentAuth = document.querySelector('#connection-form select[name="auth"] option[value="agent"]');
  if (agentAuth) { agentAuth.disabled = true; agentAuth.textContent = 'SSH Agent（Android 暂不支持）'; }
  const welcomeAuth = document.querySelector('#welcome-state small');
  if (welcomeAuth) welcomeAuth.textContent = '支持密码与私钥登录';
  // Android WebView does not provide a reliable directory tree for
  // webkitdirectory. Keep the working document picker for one or more files.
  document.getElementById('upload-folder-option').hidden = true;
  const uploadButton = document.getElementById('choose-files');
  uploadButton.innerHTML = '<svg aria-hidden="true"><use href="#i-upload"/></svg>上传文件';
  uploadButton.onclick = () => document.getElementById('file-picker').click();
  const keyImportFields = document.getElementById('key-import-fields');
  keyImportFields.querySelector('label').hidden = true; // A local filesystem path is not accessible to WebView.
  const keyFileRow = document.createElement('div');
  keyFileRow.className = 'mobile-key-file-picker';
  keyFileRow.innerHTML = '<button type="button" class="upload-button">从设备选择私钥文件</button><input type="file" accept="*/*" hidden><span role="status"></span>';
  keyImportFields.prepend(keyFileRow);
  const keyFileInput = keyFileRow.querySelector('input');
  keyFileRow.querySelector('button').addEventListener('click', () => keyFileInput.click());
  keyFileInput.addEventListener('change', () => {
    const file = keyFileInput.files?.[0];
    if (!file) return;
    if (file.size > 1024 * 1024) {
      keyFileRow.querySelector('span').textContent = '私钥文件过大';
      keyFileInput.value = '';
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      document.getElementById('key-editor-form').elements.privateKey.value = String(reader.result || '');
      const name = document.getElementById('key-editor-form').elements.name;
      if (!name.value) name.value = file.name;
      keyFileRow.querySelector('span').textContent = `已载入 ${file.name}`;
      keyFileInput.value = '';
    };
    reader.onerror = () => { keyFileRow.querySelector('span').textContent = '读取私钥失败，请重试'; keyFileInput.value = ''; };
    reader.readAsText(file);
  });
  document.getElementById('diagnostics-local').hidden = true;
  document.querySelector('.diagnostics-note').textContent = 'Android 不支持本机 MTR。远程诊断在已连接的服务器上运行，并可能需要该服务器安装 mtr。';
  document.getElementById('network-chart').setAttribute('aria-label', '点击打开远程 MTR 网络诊断');
  document.getElementById('network-chart').setAttribute('title', '点击打开远程 MTR 网络诊断');
  const navItems = [
    ['servers', '服务器', 'i-server'],
    ['terminal', '终端', 'i-terminal'],
    ['files', '文件', 'i-folder'],
    ['monitor', '监控', 'i-cpu'],
    ['more', '更多', 'i-layout'],
  ];
  const svg = id => `<svg aria-hidden="true"><use href="#${id}"/></svg>`;
  const nav = document.createElement('nav');
  nav.id = 'mobile-navigation';
  nav.setAttribute('aria-label', 'DengShell 主导航');
  for (const [id, label, icon] of navItems) {
    const button = document.createElement('button');
    button.type = 'button';
    button.dataset.mobileTarget = id;
    button.innerHTML = `${svg(icon)}<span>${label}</span>`;
    button.setAttribute('aria-label', label);
    nav.append(button);
  }
  body.append(nav);

  const brand = document.querySelector('.titlebar .brand');
  const brandTitle = document.createElement('div');
  brandTitle.className = 'mobile-brand-title';
  brandTitle.innerHTML = '<strong>DengShell</strong><small id="mobile-connection-status">服务器工作台</small>';
  brand.querySelector('#settings-button').after(brandTitle);
  const searchButton = document.createElement('button');
  searchButton.className = 'mobile-header-button';
  searchButton.type = 'button';
  searchButton.setAttribute('aria-label', '搜索服务器');
  searchButton.innerHTML = svg('i-search');
  brand.append(searchButton);
  searchButton.addEventListener('click', () => {
    if (document.getElementById('connections-drawer').hidden) document.getElementById('connection-button').click();
    document.getElementById('connection-search').focus();
  });

  const context = document.createElement('aside');
  context.id = 'mobile-context';
  context.setAttribute('aria-label', '服务器和会话');
  body.append(context);
  function renderContext() {
    context.replaceChildren();
    const heading = document.createElement('div');
    heading.className = 'mobile-context-heading';
    heading.textContent = '当前会话';
    context.append(heading);
    const tabs = [...document.querySelectorAll('#session-tabs .session-tab')];
    if (!tabs.length) {
      const empty = document.createElement('p');
      empty.className = 'mobile-context-empty';
      empty.textContent = '尚未连接服务器';
      context.append(empty);
    }
    for (const tab of tabs) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'mobile-context-row';
      button.innerHTML = `${svg('i-terminal')}<span></span>`;
      button.querySelector('span').textContent = tab.querySelector('button')?.textContent?.trim() || tab.textContent.trim();
      if (tab.classList.contains('active')) button.setAttribute('aria-current', 'true');
      button.addEventListener('click', () => {
        tab.querySelector('button')?.click();
        selectView('terminal');
      });
      context.append(button);
    }
    const serverHeading = document.createElement('div');
    serverHeading.className = 'mobile-context-heading';
    serverHeading.textContent = '已保存服务器';
    context.append(serverHeading);
    for (const row of document.querySelectorAll('#connection-groups [data-profile-id]')) {
      const label = row.querySelector('.connection-card-text strong')?.textContent?.trim();
      if (!label) continue;
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'mobile-context-row';
      button.innerHTML = `${svg('i-server')}<span></span>`;
      button.querySelector('span').textContent = label;
      button.addEventListener('click', () => {
        document.getElementById('connection-button').click();
        const actual = [...document.querySelectorAll('#connection-groups [data-profile-id]')]
          .find(item => item.dataset.profileId === row.dataset.profileId);
        actual?.scrollIntoView({block: 'center'});
        actual?.querySelector('.connection-card')?.focus();
      });
      context.append(button);
    }
    const add = document.createElement('button');
    add.type = 'button';
    add.className = 'mobile-context-row mobile-context-add';
    add.innerHTML = `${svg('i-plus')}<span>添加服务器</span>`;
    add.addEventListener('click', () => {
      document.getElementById('connection-button').click();
      document.getElementById('new-connection').click();
    });
    context.append(add);
  }
  const contextObserver = new MutationObserver(() => queueMicrotask(renderContext));
  contextObserver.observe(document.getElementById('session-tabs'), {childList:true, subtree:true, attributes:true, attributeFilter:['class']});
  contextObserver.observe(document.getElementById('connection-groups'), {childList:true, subtree:true});
  renderContext();

  const more = document.createElement('section');
  more.id = 'mobile-more-panel';
  more.innerHTML = `<h1>更多工具</h1><p>常用操作与工作台设置</p><div class="mobile-tool-grid"></div>`;
  document.getElementById('app-shell').append(more);
  const tools = [
    ['快捷命令', 'i-code', () => selectView('files', 'commands')],
    ['传输任务', 'i-upload', () => selectView('files', 'transfers')],
    ['常用应用', 'i-layout', () => selectView('files', 'common-apps')],
    ['历史命令', 'i-history', () => document.getElementById('command-history').click()],
    ['远程网络诊断', 'i-network', () => openDiagnostics()],
    ['密钥管理', 'i-key', () => document.getElementById('manage-keys').click()],
    ['加密同步', 'i-refresh', () => document.getElementById('sync-button').click()],
    ['安全锁定', 'i-lock', () => document.getElementById('manage-security-lock').click()],
    ['设置与外观', 'i-settings', () => document.getElementById('settings-button').click()],
    ['使用帮助', 'i-help-book', () => document.getElementById('help-button')?.click()],
  ];
  for (const [label, icon, action] of tools) {
    const button = document.createElement('button');
    button.type = 'button';
    button.innerHTML = `${svg(icon)}<span>${label}</span>${svg('i-chevron')}`;
    button.addEventListener('click', action);
    more.querySelector('.mobile-tool-grid').append(button);
  }

  const terminal = document.querySelector('.terminal-panel');
  const keybar = document.createElement('div');
  keybar.id = 'mobile-keybar';
  keybar.setAttribute('aria-label', '终端快捷按键');
  const keyMap = [
    ['Esc', '\x1b'], ['Tab', '\t'], ['Ctrl+C', '\x03'], ['Ctrl+D', '\x04'],
    ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['←', '\x1b[D'], ['→', '\x1b[C'],
  ];
  for (const [label, value] of keyMap) {
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = label;
    button.addEventListener('click', () => {
      const session = current();
      if (session?.ready) sendInput(session, value);
      else document.getElementById('command-input').focus();
    });
    keybar.append(button);
  }
  terminal.querySelector('.terminal-command-bar').before(keybar);
  const submit = document.createElement('button');
  submit.id = 'mobile-command-submit';
  submit.type = 'button';
  submit.setAttribute('aria-label', '执行命令');
  submit.innerHTML = svg('i-up');
  submit.addEventListener('click', () => document.getElementById('command-form').requestSubmit());
  terminal.querySelector('.terminal-command-bar').append(submit);

  let previousView = 'terminal';
  let activeView = 'terminal';
  const drawer = document.getElementById('connections-drawer');
  function highlightNav(view) {
    for (const button of nav.querySelectorAll('button')) {
      if (button.dataset.mobileTarget === view) button.setAttribute('aria-current', 'page');
      else button.removeAttribute('aria-current');
    }
  }
  function selectView(view, pane = 'files') {
    if (view === 'servers') {
      document.getElementById('connection-button').click();
      return;
    }
    if (!drawer.hidden) setDrawer(false);
    activeView = view;
    if (view !== 'more') previousView = view;
    body.dataset.mobileView = view;
    highlightNav(view);
    if (view === 'files') document.querySelector(`.file-tab[data-pane="${pane}"]`)?.click();
    window.dispatchEvent(new Event('resize'));
  }
  nav.addEventListener('click', event => {
    const button = event.target.closest('[data-mobile-target]');
    if (button) selectView(button.dataset.mobileTarget);
  });
  new MutationObserver(() => highlightNav(drawer.hidden ? activeView : 'servers'))
    .observe(drawer, { attributes:true, attributeFilter:['hidden'] });
  const statusText = document.getElementById('mobile-connection-status');
  new MutationObserver(() => {
    const host = document.getElementById('terminal-meta-host').textContent.trim();
    statusText.textContent = host && host !== 'SSH 终端' ? host : '服务器工作台';
  }).observe(document.getElementById('terminal-meta-host'), {childList:true, subtree:true, characterData:true});
  window.DengShellMobile = {
    back() {
      if (activeView === 'terminal') return false;
      selectView(activeView === 'more' ? previousView : 'terminal');
      return true;
    },
    selectView,
  };
  selectView('terminal');
})();
