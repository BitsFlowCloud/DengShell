// Render full font programs, not glyph subsets. Font downloads happen only in
// this build step; the website and app initially display these small PNGs.
import { readFile, writeFile, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
const [directory, browserPath, puppeteerPath] = process.argv.slice(2);
if (!directory || !browserPath || !puppeteerPath) throw new Error('Usage: node render-library.mjs WEBSITE CHROME PUPPETEER_MODULE');
const { default: puppeteer } = await import(puppeteerPath);
const root = resolve(new URL('../..', import.meta.url).pathname), output = resolve(directory);
const data = JSON.parse(await readFile(join(output, 'fonts/render-input.json'), 'utf8'));
const profile = await mkdtemp(join(tmpdir(), 'fl-'));
const browser = await puppeteer.launch({ executablePath: browserPath, headless: true, userDataDir: profile, args: ['--no-sandbox','--disable-dev-shm-usage'] });
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const validation = [];
try {
  const page = await browser.newPage();
  await page.setViewport({ width: 800, height: 200, deviceScaleFactor: 1 });
  await page.setContent('<!doctype html><html><head><style>*{box-sizing:border-box}html,body{margin:0;background:white}</style></head><body><canvas width="800" height="200"></canvas></body></html>');
  const ui = await readFile(join(root,'web/assets/ui-fonts/ibm-plex-sans-sc.woff2'));
  const cjk = await readFile(join(root,'web/assets/fonts/maple-mono-cn.woff2'));
  await page.evaluate(async (ui,cjk) => {
    for (const [name,bytes] of [['UI Fallback',ui],['CJK Fallback',cjk]]) {
      const face = new FontFace(name, `url(data:font/woff2;base64,${bytes})`); await face.load(); document.fonts.add(face);
    }
  }, ui.toString('base64'),cjk.toString('base64'));
  for (const font of data.fonts) {
    const bytes = await readFile(join(output,font.file.path));
    if (hash(bytes) !== font.file.sha256) throw new Error(`${font.id}: checksum mismatch`);
    const rendered = await page.evaluate(async f => {
      if (window.currentFace) document.fonts.delete(window.currentFace);
      const face = new FontFace('Sample', `url(data:font/woff2;base64,${f.bytes})`, { weight:f.weightRange || '400' });
      await face.load(); document.fonts.add(face); window.currentFace = face;
      const canvas = document.querySelector('canvas'), c = canvas.getContext('2d');
      c.fillStyle = '#ffffff'; c.fillRect(0,0,800,200); c.fillStyle = '#243b4e'; c.textBaseline = 'middle'; c.fontKerning = 'none';
      const fallback = f.kind === 'font' ? 'CJK Fallback' : 'UI Fallback';
      c.font = `400 28px "Sample", "${fallback}"`;
      for (const [text,y] of (f.kind === 'font' ? [['root@server:~# ssh -p 22 user@host',43],['简体中文 / 繁體中文  文件管理 012345',100],['Il1 O0 {} [] => != ┌─┬─┐  192.168.1.1',157]] : [['连接管理  系统信息  设置  文件编辑器',43],['連線管理  繁體中文  設定  檔案編輯器',100],['DengShell  English  0123456789  Il1 O0',157]])) c.fillText(text,24,y);
      c.font = '400 100px "Sample"';
      return {png:canvas.toDataURL('image/png').split(',')[1], widths:[...'iWm0 .|'].map(x=>c.measureText(x).width), loaded:face.status};
    }, {...font,bytes:bytes.toString('base64')});
    if (font.kind === 'font' && Math.max(...rendered.widths)-Math.min(...rendered.widths) > .1) throw new Error(`${font.id}: browser ASCII width mismatch`);
    const png = Buffer.from(rendered.png,'base64'), name = `${font.id}-${hash(png).slice(0,16)}.png`;
    await writeFile(join(output,'fonts/previews',name),png);
    font.preview = {path:`/fonts/previews/${name}`,size:png.length,sha256:hash(png)};
    validation.push({id:font.id,...font._validation,browser: {loaded:rendered.loaded,asciiWidths:rendered.widths},fontBytes:bytes.length,previewBytes:png.length});
    delete font._validation;
    process.stdout.write(`${font.id} ${png.length}\n`);
  }
  const catalog = JSON.stringify(data,null,2)+'\n';
  await writeFile(join(output,'fonts/catalog.json'),catalog);
  await writeFile(join(root,'internal/app/font_library_catalog.json'),catalog);
  await writeFile(join(output,'fonts/validation.json'),JSON.stringify(validation,null,2)+'\n');
} finally { await browser.close(); await rm(profile,{recursive:true,force:true}); }
