'use strict';

// One model, load token and save snapshot per (SSH session, remote path).
// Controls in different windows never share a mutable "current document".
window.DengRemoteTextDocument = class {
  constructor(state, path, changed = () => {}, transport = { api, post }) {
    this.state = state; this.path = path; this.changed = changed; this.transport = transport;
    this.model = null; this.baseline = null; this.loading = false; this.saving = false;
    this.closed = false; this.loadToken = 0; this.error = '';
  }
  get dirty() { return !!this.baseline && this.model.raw !== this.baseline.text; }
  async load(encoding = 'auto') {
    if (this.closed || this.saving) return;
    const token = ++this.loadToken;
    const current = () => !this.closed && token === this.loadToken;
    this.loading = true; this.error = ''; this.changed();
    try {
      const data = await this.transport.api(`/api/sessions/${this.state.id}/file-content?path=${encodeURIComponent(this.path)}&encoding=${encodeURIComponent(encoding)}`);
      if (!current()) return;
      this.baseline = data; this.model = new DengTextModel(data.text); this.changed(true);
    } catch (error) { if (current()) { this.error = error.message; throw error; } }
    finally { if (current()) { this.loading = false; this.changed(); } }
  }
  async save() {
    if (this.closed || this.loading || this.saving || !this.dirty) return false;
    const baseline = this.baseline, text = this.model.raw;
    this.saving = true; this.error = ''; this.changed();
    try {
      const data = await this.transport.post(`/api/sessions/${this.state.id}/file-content`, { path: this.path, text, encoding: baseline.encoding, sha256: baseline.sha256 });
      if (!this.closed) {
        // Do not overwrite edits made while the save was in flight. Only move
        // the saved baseline and hash forward to the acknowledged contents.
        this.baseline = data;
      }
      return true;
    } catch (error) { if (!this.closed) this.error = error.message; throw error; }
    finally { this.saving = false; if (!this.closed) this.changed(); }
  }
  close() { this.closed = true; this.loadToken++; }
};
