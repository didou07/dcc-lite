const { JSDOM } = require("jsdom");
const fs = require("fs");
const assert = require("assert");
const root = require("path").join(__dirname, "../../src/web");
const core = fs.readFileSync(root + "/js/core.js", "utf8");
const a = core.indexOf("let menuKeys = null;");
const b = core.indexOf("function applyTheme");
const code = core.slice(a, b);
const dom = new JSDOM(`<body><textarea id="t">hello world</textarea><div id="menu" hidden></div></body>`, { runScripts: "outside-only" });
const { window } = dom; const { document } = window;
window.eval(`
  const $ = (s) => document.querySelector(s);
  function h(tag, props = {}, ...children) { const n = document.createElement(tag); for (const [k, v] of Object.entries(props)) { if (v === undefined || v === null || v === false) continue; if (k === "class") n.className = v; else if (k === "dataset") Object.assign(n.dataset, v); else if (k.startsWith("on")) n.addEventListener(k.slice(2).toLowerCase(), v); else if (k === "text") n.textContent = v; else n.setAttribute(k, v === true ? "" : String(v)); } for (const c of children.flat()) { if (c === null || c === undefined) continue; n.append(c.nodeType ? c : document.createTextNode(String(c))); } return n; }
  function icon() { return document.createElement("i"); }
` + code + `; window.openMenu = openMenu; window.closeMenu = closeMenu;`);
const t = document.getElementById("t");
t.focus(); t.setSelectionRange(0, 5);
let ran = "";
window.openMenu([{ label: "Copy", run: () => { ran = "copy"; } }, { label: "Paste", run: () => { ran = "paste"; } }], 10, 10, { keepFocus: true });
assert.strictEqual(document.activeElement, t, "focus must stay in the text box while the menu is open");
assert.strictEqual(t.selectionStart, 0); assert.strictEqual(t.selectionEnd, 5);
// mouse down on an item must not take focus (default prevented)
const btn = document.querySelector("#menu button");
const down = new window.MouseEvent("mousedown", { bubbles: true, cancelable: true });
btn.dispatchEvent(down); assert(down.defaultPrevented, "mousedown on an item is prevented so focus stays");
// arrow down moves into the menu
t.dispatchEvent(new window.KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }));
assert.strictEqual(document.activeElement, btn);
// choosing an item runs it and closes the menu
btn.click(); assert.strictEqual(ran, "copy"); assert(document.getElementById("menu").hidden);
// typing a letter with the menu open closes it and the key still reaches the box
t.focus();
window.openMenu([{ label: "Copy", run() {} }], 10, 10, { keepFocus: true });
const key = new window.KeyboardEvent("keydown", { key: "x", bubbles: true, cancelable: true });
t.dispatchEvent(key); assert(document.getElementById("menu").hidden); assert(!key.defaultPrevented);
// a normal menu still focuses its first item
window.openMenu([{ label: "One", run() {} }], 10, 10);
assert.strictEqual(document.activeElement.textContent, "One");
console.log("menu focus: all checks passed");
// every menu that sits over selected text must keep the focus where the selection is
const term = fs.readFileSync(root + "/js/terminal.js", "utf8");
const termMenu = term.slice(term.indexOf("function openTerminalMenu"), term.indexOf("async function renameTab"));
assert(/keepFocus:\s*true/.test(termMenu), "terminal menu keeps focus in the terminal");
const textmenu = fs.readFileSync(root + "/js/textmenu.js", "utf8");
assert.strictEqual((textmenu.match(/keepFocus:\s*true/g) || []).length, 2, "both text menus keep focus");
console.log("menu sites: all checks passed");
