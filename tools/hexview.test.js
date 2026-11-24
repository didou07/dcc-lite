const fs = require("fs");
const assert = require("assert");
globalThis.requestAnimationFrame = (f) => f();
globalThis.document = { activeElement: null, createElement: () => ({ style: {}, className: "", appendChild() {}, remove() {}, getBoundingClientRect: () => ({ height: 40 }), set textContent(v) {} }) };
const host = { clientHeight: 400, scrollTop: 0, listeners: {}, addEventListener(n, f) { this.listeners[n] = f; }, replaceChildren() {}, appendChild() {}, classList: { toggle() {} }, focus() {} };
const src = fs.readFileSync(require("path").join(__dirname, "../src/web/js/hexview.js"), "utf8");
const { HexView } = new Function(src + "; return { HexView };")();
const v = new HexView(host);
v.pre = { style: {}, set innerHTML(h) { this.html = h; }, set textContent(t) { this.html = t; } };
const key = (k, extra = {}) => v.onKey({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, preventDefault() {}, ...extra });

const orig = Uint8Array.from([0x00, 0x11, 0x22, 0x33]);
v.set(orig, { editable: true });
assert(!v.dirty());
// type "AB" over byte 0 -> 0xAB, cursor moves on
key("a"); key("b");
assert.deepStrictEqual([...v.bytes], [0xab, 0x11, 0x22, 0x33]);
assert.strictEqual(v.cursor, 1); assert.strictEqual(v.changedCount, 1); assert(v.dirty());
assert.deepStrictEqual([...orig], [0, 0x11, 0x22, 0x33], "the loaded array must not be edited");
// typing the same value back makes it clean again
v.setCursor(0); key("0"); key("0");
assert(!v.dirty(), "byte back to original should be clean");
// undo / redo
v.setCursor(0); key("f"); key("f");
assert.strictEqual(v.bytes[0], 0xff);
key("z", { ctrlKey: true }); assert.strictEqual(v.bytes[0], 0xf0);
key("z", { ctrlKey: true }); assert.strictEqual(v.bytes[0], 0x00); assert(!v.dirty());
key("y", { ctrlKey: true }); assert.strictEqual(v.bytes[0], 0xf0);
// non-hex keys are ignored in the hex column
v.revert(); key("x"); key("g"); assert(!v.dirty());
// insert, delete
v.setCursor(2); key("Insert");
assert.deepStrictEqual([...v.bytes], [0, 0x11, 0, 0x22, 0x33]); assert.strictEqual(v.sizeChange, 1); assert(v.dirty());
key("z", { ctrlKey: true });
assert.deepStrictEqual([...v.bytes], [0, 0x11, 0x22, 0x33]); assert(!v.dirty(), "undoing the insert restores a clean state");
v.setCursor(1); key("Delete");
assert.deepStrictEqual([...v.bytes], [0, 0x22, 0x33]);
key("Backspace"); // removes byte before cursor (index 0)
assert.deepStrictEqual([...v.bytes], [0x22, 0x33]);
key("z", { ctrlKey: true }); key("z", { ctrlKey: true });
assert.deepStrictEqual([...v.bytes], [0, 0x11, 0x22, 0x33]); assert(!v.dirty());
// append at the end by typing
v.setCursor(4); key("7"); key("e");
assert.deepStrictEqual([...v.bytes], [0, 0x11, 0x22, 0x33, 0x7e]); assert.strictEqual(v.sizeChange, 1);
// text column typing
v.revert(); key("Tab"); assert.strictEqual(v.column, "text");
key("A"); assert.strictEqual(v.bytes[0], 0x41); assert.strictEqual(v.cursor, 1);
key("é"); assert.strictEqual(v.bytes[1], 0x11, "non-ASCII characters are ignored");
// size change then byte edit then undo all
v.revert(); v.setCursor(0); key("Insert"); key("1"); key("2");
assert(v.dirty());
key("z", { ctrlKey: true }); key("z", { ctrlKey: true }); key("z", { ctrlKey: true });
assert(!v.dirty()); assert.deepStrictEqual([...v.bytes], [...orig]);
// rebase after save
v.revert(); key("a"); key("b"); v.rebase();
assert(!v.dirty()); assert.strictEqual(v.original[0], 0xab);
// read-only mode ignores typing
const r = new HexView(host); r.pre = v.pre; r.set(Uint8Array.from([1, 2, 3]), { editable: false });
r.onKey({ key: "a", preventDefault() {} }); assert.deepStrictEqual([...r.bytes], [1, 2, 3]); assert(!r.dirty());
r.goTo(2); assert(r.pre.html.includes("hx-pos"), "read-only navigation marks the current byte");
// empty editable file can be filled
const e = new HexView(host); e.pre = v.pre; e.set(new Uint8Array(0), { editable: true }); e.onKey({ key: "4", preventDefault() {} }); e.onKey({ key: "1", preventDefault() {} });
assert.deepStrictEqual([...e.bytes], [0x41]);
// drawing 16 MB of rows must only build the visible rows
const big = new HexView(host); big.pre = v.pre; big.set(new Uint8Array(16 << 20), { editable: true });
assert(big.pre.html.split("\n").length < 120, "only visible rows are built");
const hexPattern = HexView.parseSearchPattern("0x41 42,43", "hex");
assert.deepStrictEqual([...hexPattern.bytes], [0x41, 0x42, 0x43]);
assert(HexView.parseSearchPattern("ABC", "hex").error, "incomplete HEX pairs must be rejected");
const asciiPattern = HexView.parseSearchPattern("AB!", "ascii");
assert.deepStrictEqual([...asciiPattern.bytes], [0x41, 0x42, 0x21]);
assert(HexView.parseSearchPattern("é", "ascii").error, "non-ASCII search values must be rejected");

const search = new HexView(host); search.pre = v.pre;
search.set(Uint8Array.from([0x41, 0x42, 0x43, 0x41, 0x42, 0x43, 0x00]), { editable: true });
let match = search.find(Uint8Array.from([0x41, 0x42]), 1);
assert.strictEqual(match.index, 0);
assert.deepStrictEqual(search.getSelectionRange(), { start: 0, end: 1, length: 2 });
assert.strictEqual(search.selectedText("hex"), "41 42");
assert.strictEqual(search.selectedText("ascii"), "AB");
match = search.find(Uint8Array.from([0x41, 0x42]), 1);
assert.strictEqual(match.index, 3, "find next advances past the current match");
match = search.find(Uint8Array.from([0x41, 0x42]), 1);
assert.strictEqual(match.index, 0); assert.strictEqual(match.wrapped, true, "find next wraps to the first match");
match = search.find(Uint8Array.from([0x41, 0x42]), -1);
assert.strictEqual(match.index, 3); assert.strictEqual(match.wrapped, true, "find previous wraps to the last match");
assert.strictEqual(search.find(Uint8Array.from([0xff]), 1), null);
assert.strictEqual(search.getSelectionRange(), null, "a failed search clears the previous search selection");
search.selectionAnchor = 1; search.selectionFocus = 3;
assert(search.deleteSelection());
assert.deepStrictEqual([...search.bytes], [0x41, 0x42, 0x43, 0x00]);
const searchKey = (k, extra = {}) => search.onKey({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, preventDefault() {}, ...extra });
searchKey("z", { ctrlKey: true });
assert.deepStrictEqual([...search.bytes], [0x41, 0x42, 0x43, 0x41, 0x42, 0x43, 0x00], "undo restores a deleted selection");
searchKey("y", { ctrlKey: true });
assert.deepStrictEqual([...search.bytes], [0x41, 0x42, 0x43, 0x00], "redo deletes the selected range again");
search.selectAll();
assert.strictEqual(search.getSelectionRange().length, search.bytes.length);

const mouseView = new HexView(host); mouseView.pre = v.pre;
mouseView.set(Uint8Array.from({ length: 32 }, (_, i) => i), { editable: false });
host.contains = () => true;
const cell = (index, column = "hex") => ({ dataset: { i: String(index), c: column }, closest() { return this; } });
mouseView.onMouse({ button: 0, shiftKey: false, target: cell(4), preventDefault() {} });
assert.deepStrictEqual(mouseView.getSelectionRange(), { start: 4, end: 4, length: 1 });
document.elementFromPoint = () => cell(11, "text");
mouseView.onMouseMove({ clientX: 10, clientY: 10 });
assert.deepStrictEqual(mouseView.getSelectionRange(), { start: 4, end: 11, length: 8 }, "mouse drag selects across HEX and ASCII columns");
mouseView.onMouse({ button: 0, shiftKey: true, target: cell(15), preventDefault() {} });
assert.deepStrictEqual(mouseView.getSelectionRange(), { start: 4, end: 15, length: 12 }, "Shift+Click extends from the original anchor");
mouseView.dragging = false;
delete document.elementFromPoint;

let seed = 0x13579bdf;
const randomByte = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return Math.floor((seed / 0x100000000) * 5); };
for (let trial = 0; trial < 180; trial++) {
  const length = 8 + (trial % 43);
  const data = Uint8Array.from({ length }, randomByte);
  const size = 1 + (trial % Math.min(7, length));
  const pattern = Uint8Array.from({ length: size }, randomByte);
  const all = [];
  for (let i = 0; i <= length - size; i++) {
    if (pattern.every((byte, j) => data[i + j] === byte)) all.push(i);
  }
  const probe = new HexView(host); probe.pre = v.pre; probe.set(data, { editable: false });
  const cursor = trial % length;
  probe.setCursor(cursor);
  const expectedNext = all.find((index) => index >= cursor) ?? all[0] ?? -1;
  const actualNext = probe.find(pattern, 1);
  assert.strictEqual(actualNext?.index ?? -1, expectedNext, `forward search matches a linear scan: trial=${trial} cursor=${cursor} data=${[...data]} pattern=${[...pattern]}`);
  probe.selectionAnchor = probe.selectionFocus = -1;
  probe.setCursor(cursor);
  const previous = all.filter((index) => index < cursor);
  const expectedPrev = previous.length ? previous[previous.length - 1] : (all[all.length - 1] ?? -1);
  const actualPrev = probe.find(pattern, -1);
  assert.strictEqual(actualPrev?.index ?? -1, expectedPrev, "backward search matches a linear scan");
}

const html = fs.readFileSync(require("path").join(__dirname, "../src/web/index.html"), "utf8");
for (const id of ["previewHexSearchMode", "previewHexSearch", "previewHexFindPrev", "previewHexFindNext", "previewHexSave", "previewHexRevert"]) {
  assert(html.includes(`id="${id}"`), `hex toolbar is missing ${id}`);
}
for (const stale of ["previewHexGo", "previewHexGoBtn", "previewHexHead", "previewHexCopy", "previewHexCopyAscii", "previewHexSelectAll", "previewHexClearSelection", "previewHexHelp"]) {
  assert(!html.includes(`id="${stale}"`), `obsolete control remains: ${stale}`);
}
assert(!html.includes('Go to hexadecimal offset'), "offset navigation is removed from the UI");
assert(!html.includes('id="previewHexHead"'), "the redundant column banner is removed");
assert(html.includes('<option value="hex">HEX</option><option value="ascii">ASCII</option>'), "HEX and ASCII search modes remain available");
console.log("hex editor logic: all checks passed");
