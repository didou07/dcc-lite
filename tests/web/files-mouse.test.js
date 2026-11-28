const fs = require("fs");
const assert = require("assert");
const root = require("path").join(__dirname, "../../src/web");
const files = fs.readFileSync(root + "/js/files.js", "utf8");
const html = fs.readFileSync(root + "/index.html", "utf8");

const start = files.indexOf('pane.table.addEventListener("pointerdown", (event) => {');
const end = files.indexOf('pane.rows.addEventListener("dblclick"', start);
assert(start >= 0 && end > start, "FTP pointer handler is present");
const pointerHandler = files.slice(start, end);

assert(pointerHandler.includes('if (event.button !== 2 || event.pointerType === "touch") return;'), "only right-button drag starts rectangle selection");
assert(!pointerHandler.includes("event.button === 0"), "left-button presses are not intercepted for marquee selection");
assert(!pointerHandler.includes('setAttribute("draggable", "false")'), "row dragging stays enabled during left-button drag");
assert(pointerHandler.includes('pane.rmb = { down: true'), "right click is tracked until release for context-menu handling");
assert(pointerHandler.includes('document.addEventListener("pointercancel", finish, true)'), "cancelled right drags clean up the selection rectangle");
assert(pointerHandler.includes('window.addEventListener("blur", finish'), "window focus loss cleans up an active selection rectangle");

assert(files.includes('draggable: "true"'), "file rows remain natively draggable");
assert(files.includes('pane.rows.addEventListener("dragstart"'), "native file drag-and-drop remains wired");
assert(files.includes('files.drag = { from: pane.kind, entries: selectedEntries(pane)'), "native drag carries the selected items between panes");
assert(html.includes('<kbd>Right button</kbd> + drag'), "help explains right-button rectangle selection");
assert(html.includes('<kbd>Left button</kbd> + drag'), "help explains left-button file movement");
assert(!html.includes('<kbd>Alt</kbd> + drag'), "Alt is no longer required for file movement");

console.log("FTP mouse interaction checks: all passed");
