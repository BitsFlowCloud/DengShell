'use strict';
// Keep source newline bytes separately: textarea.value normalises CR/CRLF to LF.
// Every selection offset is mapped back before a change is applied.
window.DengTextModel = class {
  constructor(raw = '') { this.raw = raw; this.undoStack = []; this.redoStack = []; }
  get visible() { return this.raw.replace(/\r\n|\r/g, '\n'); }
  rawOffset(position) { let visual = 0, raw = 0; while (raw < this.raw.length && visual < position) { if (this.raw[raw] === '\r' && this.raw[raw + 1] === '\n') raw++; raw++; visual++; } return raw; }
  get newline() { const found = this.raw.match(/\r\n|\r|\n/g) || []; const counts = new Map(); for (const s of found) counts.set(s, (counts.get(s) || 0) + 1); return [...counts].sort((a,b) => b[1]-a[1])[0]?.[0] || '\n'; }
  checkpoint() { this.undoStack.push(this.raw); while (this.undoStack.length > 1 && (this.undoStack.length > 100 || this.undoStack.reduce((n,s) => n+s.length,0) > 16*1024*1024)) this.undoStack.shift(); this.redoStack = []; }
  replace(start, end, insertion, remember = true) { if (remember) this.checkpoint(); const a = this.rawOffset(start), b = this.rawOffset(end); this.raw = this.raw.slice(0,a) + insertion + this.raw.slice(b); return start + insertion.replace(/\r\n|\r/g,'\n').length; }
  edit(visible) { const before = this.visible; if (before === visible) return; let a=0,b=before.length,c=visible.length; while(a<b && a<c && before[a]===visible[a])a++; while(b>a && c>a && before[b-1]===visible[c-1]){b--;c--;} this.replace(a,b,visible.slice(a,c).replace(/\n/g,this.newline)); }
  undo() { if (!this.undoStack.length) return; this.redoStack.push(this.raw); this.raw=this.undoStack.pop(); }
  redo() { if (!this.redoStack.length) return; this.undoStack.push(this.raw); this.raw=this.redoStack.pop(); }
};
