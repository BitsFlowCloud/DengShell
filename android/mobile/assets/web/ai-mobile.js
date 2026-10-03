// Android keeps the SSH WebView alive behind a separate AI WebView.
(() => {
  'use strict';
  window.DENGSHELL_ANDROID = true;
  window.runtime = window.runtime || {};
  window.runtime.ClipboardSetText = async text => {
    if (navigator.clipboard?.writeText) {
      try { await navigator.clipboard.writeText(text); return true; } catch {}
    }
    // System WebView versions without Clipboard API still support a selected
    // textarea copy. There is no clipboard read API or Android bridge exposed.
    const input = document.createElement('textarea');
    input.value = text;
    input.setAttribute('readonly', '');
    input.style.cssText = 'position:fixed;inset:0;opacity:0;pointer-events:none';
    const active = document.activeElement;
    document.body.append(input);
    input.focus({preventScroll:true});
    input.select();
    try { return document.execCommand('copy'); }
    finally { input.remove(); active?.focus({preventScroll:true}); }
  };
})();
