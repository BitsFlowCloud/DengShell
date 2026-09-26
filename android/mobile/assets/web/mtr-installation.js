'use strict';

// Reading a plan is harmless. A package transaction starts only from the
// explicit confirmation handler, using the immutable plan reviewed on screen.
const mtrInstallRecords = new Map(), mtrInstallPrompted = new Set();
let mtrInstallVisible = null;
function mtrInstallKey(sessionID, direction) { return `${sessionID}:${direction}`; }
function currentMTRInstallRecord() { return mtrInstallRecords.get(mtrInstallKey(diagnosticView.sessionID, diagnosticView.direction)); }
function updateMTRInstallNotice(report = diagnosticView.report) {
  const record = currentMTRInstallRecord(), missing = report?.missingTool?.name === 'mtr' && report.missingTool.installable !== false;
  const show = missing || record?.job?.status === 'running' || record?.job?.status === 'unmonitored' || record?.phase === 'interactive';
  $('#mtr-install-notice').hidden = !show;
  $('#mtr-install-summary').textContent = record?.job?.status === 'running' ? '正在安装 mtr，关闭窗口不会中断安装。' : record?.job?.status === 'unmonitored' ? '安装进度暂时无法读取，请查看任务详情。' : record?.phase === 'interactive' ? '安装命令已发送，完成后请重新检测。' : report?.direction === 'remote' ? '远程服务器缺少 mtr，安装后可显示路由与 ASN。' : '本机缺少 mtr，安装后可进行网络诊断。';
  $('#mtr-install-open').textContent = record?.job ? '查看安装' : record?.phase === 'interactive' ? '查看安装说明' : '安装 mtr…';
  if (!missing || !report.id || !$('#diagnostics-dialog').open || mtrInstallPrompted.has(report.id)) return;
  if ([...document.querySelectorAll('dialog[open]')].some(dialog => dialog.id !== 'diagnostics-dialog' && dialog.matches(':modal'))) return;
  mtrInstallPrompted.add(report.id);
  openMTRInstallation().catch(error => toast(error.message));
}
function renderMTRInstallation(record) {
  if (record !== mtrInstallVisible) return;
  const plan = record.plan, job = record.job, state = sessions.get(record.sessionID);
  $('#mtr-install-title').textContent = job ? 'mtr 安装' : plan?.alreadyInstalled ? 'mtr 已可用' : '需要安装 mtr';
  $('#mtr-install-location').textContent = `${record.direction === 'remote' ? profileFor(state)?.name || '远程服务器' : '本机'}${plan?.distro ? ` · ${plan.distro}` : ''}${plan?.packageManager ? ` · ${plan.packageManager}` : ''}`;
  $('#mtr-install-command').textContent = plan?.command || '';
  $('#mtr-install-command').hidden = !plan?.command;
  const output = $('#mtr-install-output'), follow = output.scrollTop + output.clientHeight >= output.scrollHeight - 24;
  output.hidden = !job;
  output.textContent = job ? [job.output,job.error,job.checkCommand ? `检查进度：\n${job.checkCommand}` : '',job.logPath ? `安装日志：${job.logPath}` : ''].filter(Boolean).join('\n\n') : '';
  if (follow) output.scrollTop = output.scrollHeight;
  const button = $('#mtr-install-confirm'); button.disabled = !!record.pending || (!plan && !record.error) || (job?.status === 'running' && !record.error);
  $('#mtr-install-dismiss').textContent = job || record.phase === 'interactive' ? '关闭窗口' : '暂不安装';
  let description = '', note = '', action = '确定安装';
  if (record.pending) { description = plan ? '正在提交已确认的安装任务…' : '正在检查系统与包管理器…'; }
  else if (record.error) { description = record.error; action = job ? '刷新进度' : '重新检查'; }
  else if (job) {
    description = { running:'正在安装，请稍候…',done:'mtr 安装完成，可以重新运行网络诊断。',failed:'安装未完成，请查看下面的错误信息。',unmonitored:'无法继续读取安装进度。安装进程可能仍在运行，请先检查日志。' }[job.status] || job.status;
    action = job.status === 'done' ? '重新检测' : job.status === 'failed' ? '重新检查安装方案' : job.status === 'unmonitored' ? '复制检查命令' : '安装中…';
    button.disabled ||= job.status === 'unmonitored' && !job.checkCommand;
    note = '关闭此窗口不会终止已经启动的系统包安装。请勿同时在此机器上重复安装。';
  } else if (record.phase === 'interactive') {
    description = '请在服务器终端完成安装；需要密码时直接在终端输入。';
    action = '重新检测'; note = '安装完成后点击「重新检测」。DengShell 不读取或保存 sudo 密码。';
  } else if (plan?.alreadyInstalled) {
    description = '检测到 mtr 已安装，无需重复安装。'; action = '重新检测';
  } else if (plan?.canAutoInstall) {
    description = `将安装 ${plan.packageName}，并在完成后重新检测。是否确定执行以上命令？`;
    note = plan.permission === 'polkit' ? '确认后由系统授权窗口请求管理员权限；应用不会读取密码。关闭窗口不会中断已启动的包安装。' : '需要下载软件包。确认后开始安装；关闭窗口不会中断已启动的包管理器。';
  } else if (plan?.permission === 'interactive' && record.direction === 'remote') {
    description = '此账号需要在终端中完成权限验证。确认后，将在这台服务器的 Shell 提示符处执行以上命令。';
    action = '确定，在终端安装'; note = '请先结束终端中正在运行的程序。如果无法确认终端处于命令提示符，将仅复制安装命令。';
  } else if (plan) {
    description = plan.manualReason || '请在本机终端使用管理员权限执行以上命令，然后重新检测。';
    action = plan.command ? '复制安装命令' : '关闭'; note = 'DengShell 不会自动更改软件源。';
  }
  $('#mtr-install-description').textContent = description;
  $('#mtr-install-note').textContent = note;
  button.textContent = action;
}
async function loadMTRInstallPlan(record) {
  if (record.pending) return;
  record.pollGeneration = (record.pollGeneration || 0) + 1;
  clearTimeout(record.timer); record.timer = null;
  record.pending = true; record.error = ''; record.job = null; record.plan = null; record.phase = 'plan';
  renderMTRInstallation(record);
  try { record.plan = await post(`/api/sessions/${record.sessionID}/mtr-install-plan`, { direction:record.direction }); }
  catch (error) { record.error = error.message; }
  finally { record.pending = false; renderMTRInstallation(record); }
}
async function openMTRInstallation() {
  const sessionID = diagnosticView.sessionID, direction = diagnosticView.direction;
  if (diagnosticView.report?.id && diagnosticView.report.missingTool?.name === 'mtr') mtrInstallPrompted.add(diagnosticView.report.id);
  const key = mtrInstallKey(sessionID,direction);
  let record = mtrInstallRecords.get(key);
  if (!record) { record = { sessionID,direction,target:diagnosticView.target,phase:'plan',pending:false,plan:null,job:null,error:'',timer:null }; mtrInstallRecords.set(key,record); }
  record.target = diagnosticView.target; mtrInstallVisible = record;
  if (!$('#mtr-install-dialog').open) $('#mtr-install-dialog').showModal();
  renderMTRInstallation(record);
  if (!record.plan && !record.job && !record.pending) await loadMTRInstallPlan(record);
  if (mtrInstallVisible === record && $('#mtr-install-dialog').open) $('#mtr-install-dismiss').focus();
}
async function rerunMTRAfterInstall(record) {
  if (!sessions.get(record.sessionID)?.ready) return toast('会话已断开，请重新连接后检测');
  $('#mtr-install-dialog').close();
  diagnosticView.sessionID = record.sessionID;
  if (!$('#diagnostics-dialog').open) $('#diagnostics-dialog').showModal();
  await startDiagnostic(record.direction,record.target);
}
async function pollMTRInstallation(record) {
  clearTimeout(record.timer); record.timer = null;
  const generation = record.pollGeneration = (record.pollGeneration || 0) + 1, jobID = record.job?.id;
  if (!jobID) return;
  try {
    const job = await api(`/api/mtr-installations/${jobID}`);
    // A manual refresh or a new plan may supersede an in-flight status request.
    if (generation !== record.pollGeneration || record.job?.id !== jobID) return;
    record.job = job; record.error = '';
    renderMTRInstallation(record); updateMTRInstallNotice();
    if (record.job.status === 'running') record.timer = setTimeout(() => pollMTRInstallation(record),1000);
    if (record.job.status === 'done' && !record.retried) {
      record.retried = true; toast('mtr 安装完成');
      if ($('#diagnostics-dialog').open && diagnosticView.generation === record.diagnosticGeneration && diagnosticView.sessionID === record.sessionID) await rerunMTRAfterInstall(record);
    }
  } catch (error) {
    if (generation !== record.pollGeneration || record.job?.id !== jobID) return;
    record.error = `暂时无法读取进度：${error.message}。安装任务不会因此被取消。`;
    renderMTRInstallation(record);
    // Keep observing a running detached job after transient API failures.
    if (record.job.status === 'running') record.timer = setTimeout(() => pollMTRInstallation(record),3000);
  }
}
async function confirmMTRInstallation() {
  const record = mtrInstallVisible; if (!record || record.pending) return;
  if (record.error) return record.job ? pollMTRInstallation(record) : loadMTRInstallPlan(record);
  const plan = record.plan, job = record.job;
  if (job?.status === 'running') return;
  if (job?.status === 'failed') return loadMTRInstallPlan(record);
  if (job?.status === 'unmonitored') { if (job.checkCommand) { await copyText(job.checkCommand); toast('检查命令已复制'); } return; }
  if (job?.status === 'done' || record.phase === 'interactive' || plan?.alreadyInstalled) return rerunMTRAfterInstall(record);
  if (!plan) return;
  if (plan.canAutoInstall) {
    record.pending = true; record.error = ''; record.diagnosticGeneration = diagnosticView.generation; record.retried = false; renderMTRInstallation(record);
    try { record.job = await post('/api/mtr-installations', { planId:plan.id,approved:true }); }
    catch (error) { record.error = error.message; }
    finally { record.pending = false; renderMTRInstallation(record); updateMTRInstallNotice(); }
    if (record.job) await pollMTRInstallation(record);
    return;
  }
  if (record.direction === 'remote' && plan.permission === 'interactive') {
    const state = sessions.get(record.sessionID);
    if (state?.ready && state.shellIntegration?.ready && state.shellIntegration.atPrompt) {
      activate(state.id); record.phase = 'interactive';
      $('#mtr-install-dialog').close(); $('#diagnostics-dialog').close();
      pasteTerminalText(state,plan.command,{execute:true}); toast('请在终端中完成安装，随后重新运行 MTR');
      updateMTRInstallNotice(); return;
    }
    await copyText(plan.command); toast('安装命令已复制，请在服务器 Shell 提示符处粘贴执行'); return;
  }
  if (plan.command) { await copyText(plan.command); toast('安装命令已复制'); }
  else $('#mtr-install-dialog').close();
}
function initializeMTRInstallation() {
  $('#mtr-install-open').onclick = safe(openMTRInstallation);
  $('#mtr-install-confirm').onclick = safe(confirmMTRInstallation);
  $('#mtr-install-close').onclick = $('#mtr-install-dismiss').onclick = () => $('#mtr-install-dialog').close();
}
