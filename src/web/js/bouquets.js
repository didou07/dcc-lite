"use strict";
/* Bouquet editor (Enigma2 channel lists) */
const bq = { dir: "/etc/enigma2", kind: "tv", lists: { tv: [], radio: [] }, file: "", entries: [], original: "", hash: "", sel: new Set(), anchor: -1, busy: false, dragging: [] };
const bqURL = (tail) => `/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/enigma2/${tail}`;
const bqSnapshot = () => JSON.stringify([$("#bqName").value, bq.entries.map((e) => [e.ref, e.description])]);
const bqDirty = () => Boolean(bq.file) && bqSnapshot() !== bq.original;
const bqFilter = () => $("#bqFilter").value.trim().toLowerCase();
function bqStatus(message, kind = "") { const node = $("#bqStatus"); node.textContent = message; node.dataset.kind = kind; }
function bqButtons() {
  const open = Boolean(bq.file), hasSel = bq.sel.size > 0, canMove = hasSel && !bqFilter() && !bq.busy;
  $("#bqSave").disabled = bq.busy || !bqDirty();
  $("#bqUp").disabled = !canMove; $("#bqDown").disabled = !canMove;
  $("#bqDelete").disabled = !hasSel || bq.busy;
  $("#bqMarker").disabled = !open || bq.busy;
  $("#bqName").disabled = !open; $("#bqFilter").disabled = !open;
  const listed = bq.lists[bq.kind].some((item) => item.file === bq.file);
  $("#bqListUp").disabled = !listed || bq.busy; $("#bqListDown").disabled = !listed || bq.busy; $("#bqListRemove").disabled = !listed || bq.busy;
  $("#bqCount").textContent = open ? `${bq.entries.length} entr${bq.entries.length === 1 ? "y" : "ies"}${bq.sel.size ? `, ${bq.sel.size} selected` : ""}` : "";
  $("#bqTitle").textContent = bqDirty() ? "Bouquet editor •" : "Bouquet editor";
}
function bqRenderLists() {
  const list = $("#bqBouquets");
  list.replaceChildren();
  for (const item of bq.lists[bq.kind]) {
    const li = document.createElement("li");
    li.setAttribute("role", "option");
    li.setAttribute("aria-selected", String(item.file === bq.file));
    li.dataset.file = item.file;
    const name = document.createElement("span");
    name.className = "bq-name"; name.textContent = item.name; name.title = item.file;
    li.append(name);
    list.append(li);
  }
  if (!bq.lists[bq.kind].length) { const li = document.createElement("li"); li.className = "muted"; li.textContent = "No bouquets"; list.append(li); }
}
function bqRenderEntries() {
  const list = $("#bqEntries"), filter = bqFilter();
  list.replaceChildren();
  bq.entries.forEach((entry, index) => {
    const label = entry.name || entry.description || "";
    if (filter && !`${label} ${entry.ref}`.toLowerCase().includes(filter)) return;
    const li = document.createElement("li");
    li.setAttribute("role", "option");
    li.setAttribute("aria-selected", String(bq.sel.has(index)));
    li.dataset.index = String(index);
    li.draggable = !filter;
    if (entry.marker) li.classList.add("marker");
    const no = Object.assign(document.createElement("span"), { className: "bq-no", textContent: String(index + 1) });
    const name = Object.assign(document.createElement("span"), { className: "bq-name", textContent: entry.marker ? `— ${label} —` : label || "(name not known)" });
    const ref = Object.assign(document.createElement("span"), { className: "bq-ref", textContent: entry.ref, title: entry.ref });
    li.append(no, name, ref);
    list.append(li);
  });
  bqButtons();
}
// Updates which rows look selected without rebuilding them (a rebuild would cancel a drag in progress).
function bqSyncSelection() {
  $$("#bqEntries li[data-index]").forEach((li) => li.setAttribute("aria-selected", String(bq.sel.has(Number(li.dataset.index)))));
  bqButtons();
}
async function bqDiscardOK(title = "Discard unsaved changes?") {
  if (!bqDirty()) return true;
  return Boolean(await ask({ title, message: "The open bouquet has changes that are not saved yet.", input: false, ok: "Discard changes", danger: true }));
}
function bqError(error) {
  if (error?.code === "file_not_found" || error?.code === "web_unreachable" || error?.code === "web_error" || error?.code === "conflict" || error?.code === "not_text") { bqStatus(error.message, "error"); return; }
  handleOpError(error, "remote");
}
async function bqLoadLists(keepOpen = false) {
  bq.dir = $("#bqDir").value.trim() || "/etc/enigma2";
  bqStatus("Loading bouquets…");
  try {
    const data = await api(`${bqURL("bouquets")}?dir=${encodeURIComponent(bq.dir)}`);
    bq.lists = { tv: data.tv || [], radio: data.radio || [] };
    $("#bqSub").textContent = data.dir;
    if (!keepOpen) { bq.file = ""; bq.entries = []; bq.sel.clear(); $("#bqName").value = ""; $("#bqFilter").value = ""; bq.original = ""; }
    bqRenderLists(); bqRenderEntries();
    bqStatus(`${bq.lists.tv.length} TV and ${bq.lists.radio.length} radio bouquets.`);
  } catch (error) { bq.lists = { tv: [], radio: [] }; bqRenderLists(); bqRenderEntries(); bqError(error); }
}
async function bqOpenBouquet(file) {
  if (file === bq.file || !(await bqDiscardOK())) return;
  bqStatus("Opening…");
  try {
    const data = await api(`${bqURL("bouquet")}?dir=${encodeURIComponent(bq.dir)}&file=${encodeURIComponent(file)}`);
    Object.assign(bq, { file: data.file, entries: data.entries, hash: data.hash });
    bq.sel.clear(); bq.anchor = -1;
    $("#bqName").value = data.name; $("#bqFilter").value = "";
    bq.original = bqSnapshot();
    bqRenderLists(); bqRenderEntries();
    bqStatus(data.names_available ? "" : "Channel names were not found (no lamedb in this folder), so only references are shown.");
  } catch (error) { bqError(error); }
}
function bqMoveBlock(indices, target) {
  // Move the entries at `indices` so that they sit before position `target` of the original list, keeping their order.
  const picked = [...indices].sort((a, b) => a - b);
  const moving = picked.map((i) => bq.entries[i]);
  const before = picked.filter((i) => i < target).length;
  const rest = bq.entries.filter((_, i) => !picked.includes(i));
  rest.splice(target - before, 0, ...moving);
  bq.entries = rest;
  bq.sel = new Set(moving.map((_, n) => target - before + n));
  bq.anchor = target - before;
  bqRenderEntries();
}
function bqNudge(delta) {
  if (bqFilter() || !bq.sel.size) return;
  const picked = [...bq.sel].sort((a, b) => a - b);
  if (delta < 0 && picked[0] === 0) return;
  if (delta > 0 && picked[picked.length - 1] === bq.entries.length - 1) return;
  bqMoveBlock(picked, delta < 0 ? picked[0] - 1 : picked[picked.length - 1] + 2);
}
function bqDeleteSelected() {
  if (!bq.sel.size) return;
  const first = Math.min(...bq.sel);
  bq.entries = bq.entries.filter((_, i) => !bq.sel.has(i));
  bq.sel = new Set(bq.entries.length ? [Math.min(first, bq.entries.length - 1)] : []);
  bqRenderEntries();
}
async function bqAddMarker() {
  const title = await ask({ title: "New marker", label: "Marker text", ok: "Add", validate: (v) => (v.trim() ? "" : "Enter some text.") });
  if (!title) return;
  let next = 1;
  for (const entry of bq.entries) { const m = /^1:64:(\d+):/.exec(entry.ref); if (m) next = Math.max(next, Number(m[1]) + 1); }
  const at = bq.sel.size ? Math.max(...bq.sel) + 1 : bq.entries.length;
  const text = title.trim().slice(0, 100);
  bq.entries.splice(at, 0, { ref: `1:64:${next}:0:0:0:0:0:0:0:`, description: text, name: text, marker: true });
  bq.sel = new Set([at]);
  bqRenderEntries();
}
async function bqSave(force = false) {
  if (bq.busy || !bq.file) return;
  const name = $("#bqName").value.trim();
  if (!name) { bqStatus("Give the bouquet a name.", "error"); return; }
  bq.busy = true; bqButtons(); bqStatus("Saving…");
  try {
    const data = await api(bqURL("bouquet"), { method: "PUT", body: JSON.stringify({
      dir: bq.dir, file: bq.file, name, entries: bq.entries.map((e) => ({ ref: e.ref, description: e.description })), expected_hash: bq.hash, force,
    }) });
    bq.hash = data.hash;
    $("#bqName").value = name;
    bq.original = bqSnapshot();
    const item = bq.lists[bq.kind].find((entry) => entry.file === bq.file);
    if (item) item.name = name;
    bq.busy = false; bqRenderLists(); bqRenderEntries();
    bqStatus(`Saved ${new Date().toLocaleTimeString()} — use “Reload on receiver” to apply it.`, "ok");
  } catch (error) {
    bq.busy = false; bqButtons();
    if (error.code === "conflict") {
      bqStatus(error.message, "error");
      if (await ask({ title: "Bouquet changed on the receiver", message: "This bouquet was changed on the receiver after you opened it (Enigma2 rewrites bouquets itself). Overwrite it with your version?", input: false, ok: "Overwrite", danger: true })) await bqSave(true);
    } else bqError(error);
  }
}
async function bqListAction(action) {
  const file = bq.file;
  if (!file) return;
  if (!(await bqDiscardOK())) return;
  if (action === "remove" && !(await ask({ title: "Remove from the list", message: `Remove “${$("#bqName").value}” from the ${bq.kind.toUpperCase()} bouquet list? The file ${file} stays on the receiver.`, input: false, ok: "Remove", danger: true }))) return;
  try {
    await api(bqURL("bouquet/list-change"), { method: "POST", body: JSON.stringify({ dir: bq.dir, file, action }) });
    if (action === "remove") { bq.file = ""; bq.entries = []; bq.sel.clear(); $("#bqName").value = ""; bq.original = ""; await bqLoadLists(true); }
    else { const keep = bq.file; await bqLoadLists(true); bq.file = keep; bqRenderLists(); }
  } catch (error) { bqError(error); }
}
async function bqNewBouquet() {
  if (!(await bqDiscardOK())) return;
  const name = await ask({ title: `New ${bq.kind.toUpperCase()} bouquet`, label: "Name", ok: "Create", validate: (v) => (v.trim() ? "" : "Enter a name.") });
  if (!name) return;
  try {
    const data = await api(bqURL("bouquet/new"), { method: "POST", body: JSON.stringify({ dir: bq.dir, kind: bq.kind, name: name.trim() }) });
    bq.file = ""; bq.original = "";
    await bqLoadLists(true);
    await bqOpenBouquet(data.file);
  } catch (error) { bqError(error); }
}
async function bqReloadReceiver() {
  bqStatus("Asking the receiver to reload…");
  try { await api(bqURL("reload"), { method: "POST", body: "{}" }); bqStatus("The receiver reloaded its channel lists.", "ok"); toast("Receiver reloaded its bouquets"); }
  catch (error) { bqError(error); }
}
async function cmdBouquets() {
  if (!requireSession()) return;
  bq.file = ""; bq.entries = []; bq.sel.clear(); bq.original = ""; bq.kind = "tv";
  $("#bqName").value = ""; $("#bqFilter").value = "";
  $$("#bqKind button").forEach((button) => button.setAttribute("aria-checked", String(button.dataset.kind === "tv")));
  openDialog($("#bouquetDialog"));
  await bqLoadLists();
}
{
  const dialog = $("#bouquetDialog"), entries = $("#bqEntries");
  const closeEditor = async () => { if (bq.busy || !(await bqDiscardOK("Close without saving?"))) return; bq.file = ""; bq.original = ""; dialog.close(); };
  dialog.addEventListener("cancel", (event) => { event.preventDefault(); closeEditor(); });
  $("#bqClose").addEventListener("click", closeEditor);
  $("#bqLoad").addEventListener("click", async () => { if (await bqDiscardOK()) bqLoadLists(); });
  $("#bqDir").addEventListener("keydown", async (event) => { if (event.key === "Enter") { event.preventDefault(); if (await bqDiscardOK()) bqLoadLists(); } });
  $("#bqSave").addEventListener("click", () => bqSave());
  $("#bqReload").addEventListener("click", bqReloadReceiver);
  $("#bqNew").addEventListener("click", bqNewBouquet);
  $("#bqListUp").addEventListener("click", () => bqListAction("up"));
  $("#bqListDown").addEventListener("click", () => bqListAction("down"));
  $("#bqListRemove").addEventListener("click", () => bqListAction("remove"));
  $("#bqUp").addEventListener("click", () => bqNudge(-1));
  $("#bqDown").addEventListener("click", () => bqNudge(1));
  $("#bqDelete").addEventListener("click", bqDeleteSelected);
  $("#bqMarker").addEventListener("click", bqAddMarker);
  $("#bqName").addEventListener("input", bqButtons);
  $("#bqFilter").addEventListener("input", bqRenderEntries);
  $("#bqKind").addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-kind]");
    if (!button || button.dataset.kind === bq.kind || !(await bqDiscardOK())) return;
    bq.kind = button.dataset.kind;
    $$("#bqKind button").forEach((b) => b.setAttribute("aria-checked", String(b === button)));
    bq.file = ""; bq.entries = []; bq.sel.clear(); bq.original = ""; $("#bqName").value = ""; $("#bqFilter").value = "";
    bqRenderLists(); bqRenderEntries();
  });
  $("#bqBouquets").addEventListener("click", (event) => { const li = event.target.closest("li[data-file]"); if (li) bqOpenBouquet(li.dataset.file); });
  entries.addEventListener("click", (event) => {
    const li = event.target.closest("li[data-index]");
    if (!li) return;
    const index = Number(li.dataset.index);
    if (event.shiftKey && bq.anchor >= 0) { const [a, b] = [Math.min(bq.anchor, index), Math.max(bq.anchor, index)]; bq.sel = new Set(Array.from({ length: b - a + 1 }, (_, n) => a + n)); }
    else if (event.ctrlKey || event.metaKey) { if (!bq.sel.delete(index)) bq.sel.add(index); bq.anchor = index; }
    else { bq.sel = new Set([index]); bq.anchor = index; }
    bqRenderEntries();
  });
  entries.addEventListener("keydown", (event) => {
    if (event.key === "Delete") { event.preventDefault(); bqDeleteSelected(); }
    else if (event.altKey && event.key === "ArrowUp") { event.preventDefault(); bqNudge(-1); }
    else if (event.altKey && event.key === "ArrowDown") { event.preventDefault(); bqNudge(1); }
    else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "a") { event.preventDefault(); bq.sel = new Set(bq.entries.keys()); bqRenderEntries(); }
  });
  dialog.addEventListener("keydown", (event) => { if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); if (!$("#bqSave").disabled) bqSave(); } });
  // Drag and drop to reorder.
  const clearDrop = () => $$("#bqEntries .drop-before, #bqEntries .drop-after").forEach((li) => li.classList.remove("drop-before", "drop-after"));
  entries.addEventListener("dragstart", (event) => {
    const li = event.target.closest("li[data-index]");
    if (!li || bqFilter()) { event.preventDefault(); return; }
    const index = Number(li.dataset.index);
    if (!bq.sel.has(index)) { bq.sel = new Set([index]); bqSyncSelection(); }
    bq.dragging = [...bq.sel];
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", "bouquet-entries");
  });
  entries.addEventListener("dragover", (event) => {
    if (!bq.dragging.length) return;
    event.preventDefault();
    clearDrop();
    const li = event.target.closest("li[data-index]");
    if (!li) return;
    const rect = li.getBoundingClientRect();
    li.classList.add(event.clientY < rect.top + rect.height / 2 ? "drop-before" : "drop-after");
  });
  entries.addEventListener("dragleave", (event) => { if (!entries.contains(event.relatedTarget)) clearDrop(); });
  entries.addEventListener("drop", (event) => {
    if (!bq.dragging.length) return;
    event.preventDefault();
    const li = event.target.closest("li[data-index]");
    let target = bq.entries.length;
    if (li) { const rect = li.getBoundingClientRect(); target = Number(li.dataset.index) + (event.clientY < rect.top + rect.height / 2 ? 0 : 1); }
    const dragged = bq.dragging; bq.dragging = []; clearDrop();
    bqMoveBlock(dragged, target);
  });
  entries.addEventListener("dragend", () => { bq.dragging = []; clearDrop(); });
  window.addEventListener("beforeunload", (event) => { if (dialog.open && bqDirty()) { event.preventDefault(); event.returnValue = ""; } });
}
async function cmdCopyPath() {
  const pane = panes[files.active], sel = selectedEntries(pane);
  const text = sel.length ? sel.map((e) => e.path).join("\n") : pane.path;
  try { await navigator.clipboard.writeText(text); toast(sel.length > 1 ? "Paths copied" : "Path copied"); }
  catch { toast("Clipboard access is not available", "error"); }
}
function cmdSelectAll() {
  const pane = panes[files.active];
  pane.selected = new Set(pane.visible.map((e) => e.path));
  updateSelectionUI(pane);
}
function handleOpError(error, kind) {
  if (kind === "remote" && sessionLost(error)) handleSessionLost();
  toastError(error);
}
$(".actionbar").addEventListener("click", (event) => {
  const button = event.target.closest("[data-cmd]");
  if (!button) return;
  ({ mkdir: cmdMkdir, edit: cmdEdit, bouquets: cmdBouquets, tools: cmdTools, rename: cmdRename, chmod: cmdChmod, delete: cmdDelete, copypath: cmdCopyPath, selectall: cmdSelectAll,
     transfer: () => transferSelection(files.active === "remote" ? "download" : "upload") })[button.dataset.cmd]?.();
});
