'use strict';

// Display storage locations without exposing credentials or decryption keys.
function initializeConfigDocument() {
  const button = node('button'); button.id = 'connection-config-document'; button.type = 'button'; button.setAttribute('role', 'menuitem');
  button.append(icon('folder'), document.createTextNode('连接配置文档'), node('span', '', '升级与备份'));
  const menu = $('#settings-menu'); menu.insertBefore(button, menu.querySelector('.scale-setting'));
  const dialog = node('dialog', 'config-document-dialog'); dialog.id = 'config-document-dialog'; dialog.setAttribute('aria-labelledby', 'config-document-title');
  dialog.innerHTML = `<div class="dialog-heading"><div><h2 id="config-document-title">连接配置文档</h2><p>新建、编辑和成功连接后自动保存，升级后从原位置继续读取。</p></div><button type="button" class="icon-button config-document-close" aria-label="关闭连接配置文档"><svg><use href="#i-close"/></svg></button></div>
    <section class="config-document-location"><span>当前配置文件</span><code id="config-document-path"></code><button type="button" id="copy-config-path" class="secondary-button">复制文件路径</button></section>
    <div class="config-document-guide"><p>便携版在程序旁的 data 目录保存加密配置，deb 安装版使用用户配置目录，包含明暗、窗口大小、缩放、布局、字体与背景选择，以及 SSH、分组、密钥引用、代理和历史。<span id="config-document-schema"></span></p><ol><li><strong>升级应用：</strong>在原目录替换程序文件，保留完整 data 配置目录。</li><li><strong>备份或换电脑：</strong>退出程序后复制整个配置目录，其中包含 <code>dengshell.config.enc</code>、<code>dengshell.config.key</code>、<code>assets</code> 和 <code>keys</code>。加密密钥不能丢失；外部私钥路径需要另行保留。</li><li><strong>全新安装：</strong>data 内没有配置时创建默认配置，不自动读取旧版的用户配置目录。旧版散落在程序旁的配置会验证后迁入 data；旧 <code>config.json</code> 会备份并转为加密格式。</li><li><strong>自定义资源：</strong>导入的字体和背景已复制到资源目录，原始文件移动或删除不会影响使用。只替换程序即可保留这些资源。</li></ol><p>加密配置自动解锁并校验完整性，明文只在内存中读取；它不能阻止其他拥有文件写入权限的程序删除数据。未选择保存的密码只用于当前运行。</p></div>
    <p id="config-document-feedback" role="status" aria-live="polite" hidden></p><div class="config-document-footer"><button type="button" id="copy-config-directory" class="secondary-button">复制配置目录</button><button type="button" class="primary-button config-document-close">知道了</button></div>`;
  document.body.append(dialog);
  let storage;
  button.onclick = safe(async () => {
    storage = await api('/api/config/storage');
    $('#config-document-path').textContent = storage.configPath;
    $('#config-document-schema').textContent = ` 文件格式版本：${storage.schemaVersion}。`;
    $('#config-document-feedback').hidden = true;
    menu.hidden = true; $('#settings-button').setAttribute('aria-expanded', 'false');
    dialog.showModal();
  });
  for (const close of dialog.querySelectorAll('.config-document-close')) close.onclick = () => dialog.close();
  const copyLocation = async (field, label) => {
    if (!storage) return;
    const feedback = $('#config-document-feedback');
    try { await copyText(storage[field]); feedback.textContent = `${label}已复制`; }
    catch (error) { feedback.textContent = `复制失败：${error.message || error}`; }
    feedback.hidden = false;
  };
  $('#copy-config-path').onclick = () => copyLocation('configPath', '配置文件路径');
  $('#copy-config-directory').onclick = () => copyLocation('configDir', '配置目录');
}
document.addEventListener('DOMContentLoaded', initializeConfigDocument, { once:true });
