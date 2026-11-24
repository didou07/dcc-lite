const { JSDOM } = require("jsdom");
const fs = require("fs");
const assert = require("assert");
const root = require("path").join(__dirname, "../src/web");
const html = fs.readFileSync(root + "/index.html", "utf8");
const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true });
const { window } = dom;
const { document } = window;
window.ResizeObserver = undefined;
const core = fs.readFileSync(root + "/js/core.js", "utf8");
const files = fs.readFileSync(root + "/js/files.js", "utf8");
const a = files.indexOf("(function editorFindReplace() {");
const b = files.indexOf("})();", a) + 5;
const iife = files.slice(a, b);
// minimal helpers used by the find code
const helpers = `
  const $ = (s) => document.querySelector(s);
  function h(tag, props = {}, ...children) { const n = document.createElement(tag); for (const [k, v] of Object.entries(props)) { if (v === undefined || v === null || v === false) continue; if (k === "class") n.className = v; else n.setAttribute(k, v === true ? "" : String(v)); } for (const c of children.flat()) n.append(c); return n; }
  function editorCursor() {}
`;
window.eval(helpers + iife);

const $ = (s) => document.querySelector(s);
const text = $("#editorText"), needle = $("#findText"), info = $("#findInfo"), bar = $("#editorFind");
text.value = "Foo bar foo\nbaz FOO <b>foo</b> & foo\n";
bar.hidden = false;
const type = (v) => { needle.value = v; needle.dispatchEvent(new window.Event("input", { bubbles: true })); };
const marks = () => [...document.querySelectorAll(".editor-marks mark")];

type("foo");
assert.strictEqual(marks().length, 5, "all matches coloured (case-insensitive)");
assert.match(info.textContent, /^1\/5/, info.textContent);
assert.strictEqual(document.querySelectorAll(".editor-marks mark.current").length, 1);
assert.strictEqual(text.selectionStart, 0);
// the coloured layer holds the same text as the editor, so it lines up
assert.strictEqual($(".editor-marks-inner").textContent, text.value);
// markup in the file is shown as text, not turned into elements
assert.strictEqual(document.querySelectorAll(".editor-marks b").length, 0);

$("#findNext").click(); assert.match(info.textContent, /^2\/5/); assert.strictEqual(marks().findIndex((m) => m.classList.contains("current")), 1);
for (let i = 0; i < 3; i++) $("#findNext").click();
assert.match(info.textContent, /^5\/5/);
$("#findNext").click(); assert.match(info.textContent, /^1\/5 ↻/, "wraps to the first");
$("#findPrev").click(); assert.match(info.textContent, /^5\/5 ↻/, "previous wraps to the last");
$("#findPrev").click(); assert.match(info.textContent, /^4\/5/);

$("#findCase").checked = true; $("#findCase").dispatchEvent(new window.Event("change"));
assert.strictEqual(marks().length, 3, "match case: foo x3");
assert.match(info.textContent, /\/3$/);

type("nothing here");
assert.strictEqual(marks().length, 0);
assert.strictEqual(info.textContent, "No matches"); assert(info.classList.contains("none"));
assert.strictEqual(needle.getAttribute("aria-invalid"), "true");
type("");
assert.strictEqual(info.textContent, ""); assert.strictEqual(needle.getAttribute("aria-invalid"), null);

// regex characters are searched literally
text.value = "a.b a*b (x) [y]"; type("a.b"); assert.strictEqual(marks().length, 1); type("[y]"); assert.strictEqual(marks().length, 1); type("(x)"); assert.strictEqual(marks().length, 1);

// replace one / replace all keep the counter right
text.value = "x1 x2 x3"; $("#findCase").checked = false; $("#replaceText").value = "Y";
type("x");
assert.match(info.textContent, /^1\/3/);
$("#replaceAll").click();
assert.strictEqual(text.value.includes("x"), false, text.value);
assert.match(info.textContent, /3 replaced/);

// editing the text refreshes the colours
type("y"); const before = marks().length; assert(before >= 3);
text.value += " y"; text.dispatchEvent(new window.Event("input", { bubbles: true }));
setTimeout(() => {
  assert.strictEqual(marks().length, before + 1, "colours follow edits");
  // closing clears everything
  $("#editorFindBtn").click();
  assert.strictEqual(document.querySelectorAll(".editor-marks mark").length, 0);
  assert(bar.hidden);
  // a big file is counted but not coloured
  bar.hidden = false; text.value = "ab ".repeat(600000); type("ab");
  assert.strictEqual(marks().length, 0); assert.match(info.textContent, /\/100000\+/); assert.match(info.title, /Colouring is off/);
  console.log("editor find: all checks passed");
  process.exit(0);
}, 400);
