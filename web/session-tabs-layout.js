// Keep the session list usable without taking the whole window from the terminal.
(() => {
  const tabs = document.getElementById('session-tabs');
  const titlebar = document.querySelector('.titlebar');
  let frame = 0, previousHeight = 0;
  function layout() {
    frame = 0;
    const columns = Math.min(6, Math.max(1, tabs.childElementCount), Math.max(1, Math.floor((tabs.clientWidth + 8) / 148)));
    const value = String(columns);
    if (tabs.style.getPropertyValue('--session-columns') !== value) tabs.style.setProperty('--session-columns', value);
    tabs.dataset.compactTabs = String(Math.min(240, (tabs.clientWidth - (columns - 1) * 8) / columns) < 190);
    const height = titlebar.offsetHeight;
    if (height !== previousHeight) {
      previousHeight = height;
      document.documentElement.style.setProperty('--titlebar-height', `${height}px`);
      if (typeof fitActive === 'function') fitActive();
      if (typeof fitServerExplorer === 'function') fitServerExplorer();
    }
  }
  function schedule() {
    if (!frame) frame = requestAnimationFrame(layout);
  }
  const sizeObserver = new ResizeObserver(schedule);
  sizeObserver.observe(tabs); sizeObserver.observe(titlebar);
  new MutationObserver(schedule).observe(tabs, { childList: true });
  window.addEventListener('resize', schedule);
  schedule();
})();
