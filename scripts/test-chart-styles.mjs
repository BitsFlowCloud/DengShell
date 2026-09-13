import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
const values = new Map();
const ctx = { window: {}, document: { documentElement: { style: { setProperty: (key, value) => values.set(key, value), removeProperty: key => values.delete(key) } }, addEventListener() {} } };
vm.createContext(ctx);
vm.runInContext(readFileSync(new URL('../web/chart-styles.js', import.meta.url), 'utf8'), ctx);
const styles = ctx.window.DengChartStyles;
styles.apply({ upload: { color: '#ff1234', width: 4, dashed: true }, download: { color: '#1234ff', width: 1, dashed: false }, latency: { color: '#12ff34', width: 2.5, dashed: false } });
assert.equal(values.get('--chart-upload-color'), '#ff1234');
assert.equal(values.get('--chart-upload-width'), '4px');
assert.equal(values.get('--chart-upload-dash'), '5 4');
assert.equal(values.get('--chart-download-width'), '1px');
assert.equal(values.get('--chart-download-dash'), 'none');
assert.equal(values.get('--chart-latency-width'), '2.5px');
styles.apply({ upload: { color: 'url(https://invalid/)', width: Infinity }, latency: { color: '', width: .5 } });
assert.equal(values.has('--chart-upload-color'), false);
assert.equal(values.get('--chart-upload-width'), '2.3px');
assert.equal(values.get('--chart-download-dash'), '5 4');
assert.equal(values.get('--chart-latency-width'), '0.5px');
assert.equal(values.has('--chart-latency-color'), false);

// Structured style map entries must be compared by value. Changing only one
// line may not resend stale styles for lines edited in another window.
const source = readFileSync(new URL('../web/appearance.js', import.meta.url), 'utf8');
const context = vm.createContext({ structuredClone, appearanceMapFields: new Set(['fontColors','fontBold','chartStyles']) });
vm.runInContext(source.slice(source.indexOf('function appearanceDifference('),source.indexOf('function persistAppearance(')),context);
const before = { chartStyles: { upload: { color:'#123456',width:2,dashed:false }, download: { color:'#abcdef',width:1,dashed:true } } };
const after = structuredClone(before); after.chartStyles.upload.width=4;
const patch = context.appearanceDifference(after,before);
assert.deepEqual(Object.keys(patch.chartStyles),['upload']);
const latest = structuredClone(before); latest.chartStyles.download.color='#ff0000';
const merged = context.mergeAppearanceChanges(latest,patch);
assert.equal(merged.chartStyles.upload.width,4);assert.equal(merged.chartStyles.download.color,'#ff0000');
console.log('PASS: line styles remain independent, support theme defaults, validate preview inputs, and patch only changed lines across windows.');
