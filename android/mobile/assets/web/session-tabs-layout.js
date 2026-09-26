// Keep the session list usable without taking the whole window from the terminal.
(() => {
  const tabs = document.getElementById('session-tabs');
  const titlebar = document.querySelector('.titlebar');
  let frame = 0, previousHeight = 0;
  function layout() {
    frame = 0;
    // Natural tab widths; consume the rest of a row only after its sixth tab.
    // No wrapper nodes: drag, keyboard navigation and process tabs keep their IDs.
    const items = [...tabs.children], width = tabs.clientWidth, gap = 8;
    for (const tab of items) tab.style.marginRight = '0px';
    let used = 0, count = 0;
    for (const [index, tab] of items.entries()) {
      const size = tab.getBoundingClientRect().width / (Number(document.body.style.zoom) || 1);
      if (count && used + gap + size > width) { used = 0; count = 0; }
      used += (count ? gap : 0) + size; count++;
      if (count === 6 && index < items.length - 1) {
        tab.style.marginRight = Math.max(0, width - used - 1) + 'px';
        used = 0; count = 0;
      }
    }
    const height = titlebar.offsetHeight;
    if (height !== previousHeight) {
      previousHeight = height;
      document.documentElement.style.setProperty('--titlebar-height', `${height}px`);
      if (typeof fitActive === 'function') fitActive();
      if (typeof $ === 'function' && typeof fitServerExplorer === 'function') fitServerExplorer();
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
