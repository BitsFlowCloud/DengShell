const { chromium } = require('/home/bitsflow/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');

const address = process.argv[2];
if (!address) throw new Error('Pass the local smoke URL');
const report = { checks: [], errors: [], geometry: {} };
const check = (name, ok, detail = '') => report.checks.push({ name, ok: !!ok, detail });
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));

async function inspect(page, label) {
  report.geometry[label] = await page.evaluate(() => {
    const rect = selector => {
      const node = document.querySelector(selector);
      if (!node) return null;
      const r = node.getBoundingClientRect();
      return { x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height), visible: getComputedStyle(node).display !== 'none' && r.width > 0 && r.height > 0 };
    };
    return {
      viewport: { width: innerWidth, height: innerHeight },
      documentWidth: document.documentElement.scrollWidth,
      view: document.body.dataset.mobileView,
      header: rect('.titlebar'), navigation: rect('#mobile-navigation'),
      workspace: rect('.app-shell'), drawer: rect('#connections-drawer'),
      dialog: rect('#connection-dialog'), fileToolbar: rect('.file-toolbar'),
    };
  });
}

async function testOrientation(browser, size, label) {
  const context = await browser.newContext({ viewport: size, screen: size, isMobile: true, hasTouch: true, deviceScaleFactor: 1 });
  const page = await context.newPage();
  page.on('pageerror', error => report.errors.push(`${label}: page ${error.message}`));
  page.on('console', message => { if (message.type() === 'error') report.errors.push(`${label}: console ${message.text()}`); });
  await page.goto(address, { waitUntil: 'domcontentloaded' });
  await page.locator('body.dengshell-android').waitFor();
  await pause(350);
  await inspect(page, `${label}-initial`);
  check(`${label} no horizontal overflow`, report.geometry[`${label}-initial`].documentWidth <= size.width, JSON.stringify(report.geometry[`${label}-initial`].viewport));

  for (const [tab, view] of [['文件','files'], ['监控','monitor'], ['更多','more'], ['终端','terminal']]) {
    await page.locator(`#mobile-navigation [aria-label="${tab}"]`).click();
    check(`${label} ${tab} navigation`, await page.locator('body').getAttribute('data-mobile-view') === view);
    await inspect(page, `${label}-${view}`);
    check(`${label} ${tab} overflow`, report.geometry[`${label}-${view}`].documentWidth <= size.width);
  }
  await page.locator('#mobile-navigation [aria-label="更多"]').click();
  const moreScroll = await page.locator('#mobile-more-panel').evaluate(el => { const old = el.scrollTop; el.scrollTop = 10000; return { before: old, after: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight }; });
  check(`${label} tools reachable by scrolling`, moreScroll.scrollHeight <= moreScroll.clientHeight || moreScroll.after > 0, JSON.stringify(moreScroll));
  await page.locator('#mobile-navigation [aria-label="服务器"]').click();
  await page.locator('#connections-drawer:not([hidden])').waitFor();
  await pause(350);
  await inspect(page, `${label}-drawer`);
  const drawer = report.geometry[`${label}-drawer`].drawer;
  check(`${label} drawer fits viewport`, drawer.visible && drawer.x >= 0 && drawer.x + drawer.width <= size.width && drawer.width >= Math.min(size.width * .75, 320), JSON.stringify(drawer));
  check(`${label} server tab active`, await page.locator('#mobile-navigation [aria-label="服务器"]').getAttribute('aria-current') === 'page');
  await page.screenshot({ path: `previews/regression-${label}-drawer.png` });
  await page.locator('.mobile-header-button').click();
  check(`${label} search keeps drawer open`, !(await page.locator('#connections-drawer').evaluate(el => el.hidden)) && await page.locator('#connection-search').evaluate(el => document.activeElement === el));
  await page.locator('#connection-search').fill('No server matching 2026');
  await page.locator('#connection-search').fill('');
  await page.locator('#new-connection').click();
  await page.locator('#connection-dialog[open]').waitFor();
  await inspect(page, `${label}-dialog`);
  const dialogScroll = await page.locator('#connection-dialog').evaluate(el => { el.scrollTop = 10000; return { top: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight }; });
  check(`${label} form scrolls to save`, dialogScroll.top > 0 && await page.locator('#save-connection').isVisible(), JSON.stringify(dialogScroll));
  const saveRect = await page.locator('#save-connection').boundingBox();
  const modalRect = await page.locator('#connection-dialog').boundingBox();
  check(`${label} save button within modal viewport`, saveRect && modalRect && saveRect.y >= modalRect.y && saveRect.y + saveRect.height <= modalRect.y + modalRect.height, JSON.stringify({ saveRect, modalRect }));
  await page.screenshot({ path: `previews/regression-${label}-form-bottom.png` });
  await page.locator('#cancel-connection').click();
  check(`${label} modal closes`, !(await page.locator('#connection-dialog').evaluate(el => el.open)));
  await page.locator('#mobile-navigation [aria-label="更多"]').click();
  check(`${label} switching page closes drawer`, await page.locator('#connections-drawer').evaluate(el => el.hidden));
  await page.locator('#mobile-more-panel button').filter({ hasText:'密钥管理' }).click();
  await page.locator('#keys-dialog[open]').waitFor();
  await page.locator('#import-key').click();
  await page.locator('#key-editor-dialog[open]').waitFor();
  check(`${label} unusable key path hidden`, await page.locator('#key-import-fields > label').first().isHidden());
  await page.locator('.mobile-key-file-picker input').setInputFiles({ name:'sample.pem', mimeType:'text/plain', buffer:Buffer.from('-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n') });
  check(`${label} private key picker fills editor`, await page.locator('#key-editor-form textarea[name="privateKey"]').inputValue() !== '' && await page.locator('#key-editor-form input[name="name"]').inputValue() === 'sample.pem');
  await page.locator('#cancel-key-editor').click();
  await page.locator('#close-keys').click();
  await page.locator('#settings-button').click();
  check(`${label} settings menu opens`, await page.locator('#settings-menu').isVisible());
  await page.locator('#settings-button').click();

  await page.locator('#mobile-navigation [aria-label="文件"]').click();
  await page.locator('#choose-files').evaluate(el => { el.disabled = false; }); // Only inspect picker action without a live SSH session.
  const picker = page.waitForEvent('filechooser');
  await page.locator('#choose-files').click();
  const chooser = await picker;
  check(`${label} upload invokes device picker`, chooser.isMultiple());
  check(`${label} unsupported folder upload hidden`, await page.locator('#upload-folder-option').isHidden());
  await chooser.setFiles([]);
  await context.close();
}

(async () => {
  const browser = await chromium.launch({ executablePath:'/usr/bin/google-chrome', headless:true, args:['--no-sandbox'] });
  try {
    await testOrientation(browser, {width:390,height:844}, 'portrait');
    await testOrientation(browser, {width:844,height:390}, 'landscape');
  } finally { await browser.close(); }
  console.log(JSON.stringify(report, null, 2));
  if (report.checks.some(check => !check.ok) || report.errors.length) process.exitCode = 1;
})().catch(error => { console.error(error); process.exit(1); });
