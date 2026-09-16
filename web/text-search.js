'use strict';

// Literal search. Keep offsets in textarea (LF) coordinates and never retain
// every match: a large file containing millions of matches stays bounded.
window.DengTextSearch = (() => {
  const expression = (query, matchCase, raw = false) => {
    if (!query) return null;
    const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return new RegExp(raw ? escaped.replace(/\n/g, '(?:\\r\\n|\\r|\\n)') : escaped, matchCase ? 'gu' : 'giu');
  };
  function scan(text, query, matchCase, start = 0, end = start) {
    const pattern = expression(query, matchCase);
    let count = 0, current = 0, first = null, last = null, next = null, previous = null, match;
    if (pattern) while ((match = pattern.exec(text))) {
      const range = [match.index, match.index + match[0].length];
      count++; first ||= range; last = range;
      if (range[0] === start && range[1] === end) current = count;
      if (range[0] >= end && !(range[0] === start && range[1] === end)) next ||= range;
      if (range[0] < start) previous = range;
    }
    return { count, current, next: next || first, previous: previous || last };
  }
  function replaceAll(raw, query, replacement, matchCase) {
    const pattern = expression(query, matchCase, true);
    let count = 0;
    return { raw: pattern ? raw.replace(pattern, () => { count++; return replacement; }) : raw, get count() { return count; } };
  }
  function lineRange(text, line) {
    if (!Number.isSafeInteger(line) || line < 1) return null;
    let start = 0;
    for (let n = 1; n < line; n++) { const end = text.indexOf('\n', start); if (end < 0) return null; start = end + 1; }
    const end = text.indexOf('\n', start);
    return [start, end < 0 ? text.length : end];
  }
  return { scan, replaceAll, lineRange };
})();
