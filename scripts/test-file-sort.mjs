import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const source = fs.readFileSync(new URL('../web/app.js', import.meta.url), 'utf8');
const start = source.indexOf('function compareFileEntries('), end = source.indexOf("\nfor (const [id, key]", start);
assert.ok(start >= 0 && end > start);
const compare = vm.runInNewContext(source.slice(start, end) + '; compareFileEntries');
const entries = [
 {name:'alpha',kind:'file',bytes:1024,modifiedAt:1700000059000},
 {name:'zeta',kind:'file',bytes:9,modifiedAt:1700000001000},
 {name:'empty',kind:'file',bytes:0,modifiedAt:0},
 {name:'beta',kind:'file',bytes:1024,modifiedAt:1700000030000},
 {name:'unknown',kind:'file',bytes:null},
 {name:'folder',kind:'folder',bytes:4096,modifiedAt:9999999999999},
];
const order = (key, asc) => entries.slice().sort((a,b)=>compare(a,b,key,asc)).map(e=>e.name);
assert.deepEqual(order('size',true),['folder','empty','zeta','alpha','beta','unknown']);
assert.deepEqual(order('size',false),['folder','beta','alpha','zeta','empty','unknown']);
assert.deepEqual(order('time',true),['folder','empty','zeta','beta','alpha','unknown']);
assert.deepEqual(order('time',false),['folder','alpha','beta','zeta','empty','unknown']);
assert.deepEqual(order('name',true),['folder','alpha','beta','empty','unknown','zeta']);
assert.deepEqual(order('name',false),['folder','zeta','unknown','empty','beta','alpha']);
assert.equal(entries[0].name,'alpha');
console.log('File sorting: numeric bytes, second precision, ascending/descending, folders first, ties, unknown metadata and immutable input passed.');
