// Browser capabilities arrive only in the launch URL fragment. Keep them out
// of request URLs and remove the fragment before any application asset loads.
(() => {
  if (location.protocol === 'wails:' || location.hostname === 'wails.localhost') {
    // The native asset handler supplies its own authorization header.
    document.write('<script src="boot.js"><\/script>');
    return;
  }
  const key = 'dengshell.bootstrap-token';
  const fragment = new URLSearchParams(location.hash.slice(1));
  const supplied = fragment.get('token');
  let token = '';
  try {
    if (/^[a-f0-9]{48}$/.test(supplied || '')) sessionStorage.setItem(key, supplied);
    token = sessionStorage.getItem(key) || '';
  } catch { token = supplied || ''; }
  if (fragment.has('token')) {
    fragment.delete('token');
    history.replaceState(null, '', location.pathname + location.search + (fragment.size ? '#' + fragment : ''));
  }
  try {
    if (!/^[a-f0-9]{48}$/.test(token)) throw new Error('missing bootstrap capability');
    // This small synchronous request completes before theme/app scripts run.
    // The token is sent in a header; no executable response is evaluated.
    const request = new XMLHttpRequest();
    request.open('GET', 'boot.js', false);
    request.setRequestHeader('X-CloudShell-Token', token);
    request.send();
    const prefix = 'window.CLOUDSHELL = ';
    const text = request.responseText;
    if (request.status !== 200 || !text.startsWith(prefix) || !text.endsWith(';')) throw new Error('bootstrap rejected');
    window.CLOUDSHELL = JSON.parse(text.slice(prefix.length, -1));
  } catch {
    // A separate, script-free page avoids running the app against an absent
    // configuration and makes an expired launch address understandable.
    window.stop();
    location.replace('bootstrap-required.html');
  }
})();
