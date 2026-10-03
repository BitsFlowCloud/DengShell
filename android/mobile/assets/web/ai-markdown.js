(function () {
  'use strict';

  // Parse Markdown, then build a small DOM vocabulary. Parser-generated HTML and
  // arbitrary token attributes never reach the document.
  const MAX_INPUT = 100 * 1024;
  const MAX_TOKENS = 5000;
  const MAX_NODES = 5000;
  const MAX_NESTING = 32;
  // Very deep containers also accumulate indentation in narrow chat windows.
  // Preserve their source rather than rendering unreadable or overflowing DOM.
  const MAX_DEPTH = 16;
  const OPEN = Object.freeze({
    paragraph_open: 'p', blockquote_open: 'blockquote', bullet_list_open: 'ul',
    ordered_list_open: 'ol', list_item_open: 'li', strong_open: 'strong',
    em_open: 'em', s_open: 's', table_open: 'table', thead_open: 'thead',
    tbody_open: 'tbody', tr_open: 'tr', th_open: 'th', td_open: 'td'
  });
  let parser;

  function safeURL(value) {
    if (typeof value !== 'string' || !/^https?:\/\//i.test(value) || /[\u0000-\u0020\u007f]/.test(value)) return null;
    try {
      const url = new URL(value);
      if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password) return null;
      return url.href;
    } catch (_) { return null; }
  }

  function activateLink(event) {
    // A renderer call or synthetic click must never launch an external page.
    if (!event.isTrusted) { event.preventDefault(); return; }
    if ((event.type === 'auxclick' && event.button !== 1) || (event.type === 'click' && event.button !== 0)) return;
    const href = safeURL(event.currentTarget.getAttribute('href'));
    if (!href) { event.preventDefault(); return; }
    if (typeof window.runtime?.BrowserOpenURL === 'function') {
      event.preventDefault();
      // Wails does not handle target=_blank itself. Only a real click reaches
      // its external-browser bridge; never navigate the assistant window.
      try { window.runtime.BrowserOpenURL(href); } catch (_) { /* Keep this window intact. */ }
    }
  }

  function render(element, value) {
    if (!element || typeof element.replaceChildren !== 'function') return false;
    const source = typeof value === 'string' ? value : String(value ?? '');
    element.classList.add('ai-markdown');
    element.classList.remove('ai-markdown-fallback');
    try {
      if (source.length > MAX_INPUT) throw new Error('Markdown input limit');
      if (!parser) {
        if (typeof window.markdownit !== 'function') throw new Error('Markdown parser unavailable');
        parser = window.markdownit({html: false, breaks: true, linkify: true, typographer: false, maxNesting: MAX_NESTING});
      }
      const doc = element.ownerDocument;
      let tokenCount = 0, nodeCount = 0;
      const countToken = token => {
        if (++tokenCount > MAX_TOKENS) throw new Error('Markdown token limit');
        // markdown-it can silently skip content at maxNesting. Treat reaching
        // that boundary as a full-text fallback, not a successful empty answer.
        if (Number.isInteger(token.level) && token.level >= MAX_NESTING - 1) throw new Error('Markdown nesting limit');
      };
      const node = (tag, text) => {
        if (++nodeCount > MAX_NODES) throw new Error('Markdown node limit');
        const item = tag ? doc.createElement(tag) : doc.createTextNode(text);
        if (tag && text !== undefined) {
          if (++nodeCount > MAX_NODES) throw new Error('Markdown node limit');
          item.textContent = text;
        }
        return item;
      };
      const attr = (token, name) => typeof token.attrGet === 'function' ? token.attrGet(name) : null;
      const imageText = (tokens, depth) => {
        if (depth > MAX_DEPTH) throw new Error('Markdown depth limit');
        let result = '';
        for (const token of tokens || []) {
          countToken(token);
          if (token.type === 'image') result += token.children ? imageText(token.children, depth + 1) : token.content || '';
          else if (token.type === 'softbreak' || token.type === 'hardbreak') result += '\n';
          else if (token.nesting === 0) result += token.content || '';
        }
        return result;
      };
      const appendTokens = (tokens, target, depth) => {
        if (depth > MAX_DEPTH || !Array.isArray(tokens)) throw new Error('Invalid Markdown tree');
        const stack = [{element: target, close: ''}];
        for (const token of tokens) {
          countToken(token);
          const parent = stack[stack.length - 1].element;
          if (token.type.endsWith('_close')) {
            if (stack.length < 2 || stack[stack.length - 1].close !== token.type) throw new Error('Invalid Markdown nesting');
            stack.pop();
            continue;
          }
          let tag = OPEN[token.type];
          if (token.type === 'heading_open') {
            if (!/^h[1-6]$/.test(token.tag)) throw new Error('Invalid Markdown heading');
            tag = token.tag;
          } else if (token.type === 'link_open') tag = safeURL(attr(token, 'href')) ? 'a' : 'span';
          if (tag) {
            if (depth + stack.length > MAX_DEPTH) throw new Error('Markdown depth limit');
            const item = token.hidden ? parent : node(tag);
            if (!token.hidden) {
              if (tag === 'a') {
                item.setAttribute('href', safeURL(attr(token, 'href')));
                item.setAttribute('target', '_blank');
                item.setAttribute('rel', 'noopener noreferrer');
                item.setAttribute('referrerpolicy', 'no-referrer');
                const title = attr(token, 'title');
                if (title) item.setAttribute('title', title);
                item.addEventListener('click', activateLink);
                item.addEventListener('auxclick', activateLink);
              }
              if (tag === 'ol') {
                const start = attr(token, 'start');
                if ((typeof start === 'string' || typeof start === 'number') && /^\d{1,9}$/.test(String(start))) item.setAttribute('start', String(start));
              }
              if (tag === 'td' || tag === 'th') {
                const alignment = /^text-align:(left|center|right)$/.exec(attr(token, 'style') || '');
                if (alignment) item.style.textAlign = alignment[1];
              }
              if (tag === 'table') {
                const scroll = node('div');
                scroll.className = 'ai-markdown-table';
                scroll.tabIndex = 0;
                scroll.setAttribute('role', 'region');
                scroll.setAttribute('aria-label', '表格（可横向滚动）');
                scroll.appendChild(item);
                parent.appendChild(scroll);
              } else parent.appendChild(item);
            }
            stack.push({element: item, close: token.type.replace(/_open$/, '_close')});
            continue;
          }
          switch (token.type) {
            case 'inline': appendTokens(token.children || [], parent, depth + stack.length); break;
            case 'text':
            case 'html_inline':
            case 'html_block': parent.appendChild(node(null, token.content)); break;
            case 'softbreak':
            case 'hardbreak': parent.appendChild(node('br')); break;
            case 'hr': parent.appendChild(node('hr')); break;
            case 'code_inline': parent.appendChild(node('code', token.content)); break;
            case 'fence':
            case 'code_block': {
              const pre = node('pre');
              pre.className = 'ai-markdown-code';
              pre.tabIndex = 0;
              pre.setAttribute('aria-label', '代码（可横向滚动）');
              pre.appendChild(node('code', token.content));
              parent.appendChild(pre);
              break;
            }
            case 'image': {
              const alt = token.children ? imageText(token.children, depth + stack.length) : token.content || '';
              const text = node('span', alt ? `[图片：${alt}]` : '[图片]');
              text.className = 'ai-markdown-image-text';
              parent.appendChild(text);
              break;
            }
            default: throw new Error('Unsupported Markdown token');
          }
        }
        if (stack.length !== 1) throw new Error('Unclosed Markdown token');
      };
      const fragment = doc.createDocumentFragment();
      appendTokens(parser.parse(source, {}), fragment, 0);
      element.replaceChildren(fragment);
      return true;
    } catch (_) {
      // Keep the complete answer readable if the parser is missing or a limit
      // is reached. A single text node does not expose HTML or partial output.
      element.textContent = source;
      element.classList.add('ai-markdown-fallback');
      return false;
    }
  }

  window.DengAIMarkdown = Object.freeze({render});
})();
