"use strict";
const bq = { profileID: "", dir: "/etc/enigma2", kind: "tv", lists: { tv: [], radio: [] }, file: "", entries: [], original: "", hash: "", sel: new Set(), anchor: -1, busy: false, dragging: [], namesKnown: true };
const bqHist = { undo: [], redo: [] };
const bqClip = { entries: [] };
const bqURL = (tail) => `/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/enigma2/${tail}`;
const bqSnapshot = () => JSON.stringify([$("#bqName").value, bq.entries.map((e) => [e.ref, e.description])]);
const bqDirty = () => Boolean(bq.file) && bqSnapshot() !== bq.original;
const bqFilter = () => $("#bqFilter").value.trim().toLowerCase();
const bqNormRef = (ref) => ref.replace(/:+$/, "");
const bqIsStream = (e) => !e.marker && (/^(4097|5001|5002|5003|8193):/.test(e.ref) || /%3a\/\//i.test(e.ref));
const bqText = (e) => e.name || e.description || "";
const bqPlural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
function bqStatus(message, kind = "") { const node = $("#bqStatus"); node.textContent = message; node.dataset.kind = kind; }

const bqCloneEntries = () => JSON.stringify({ e: bq.entries, s: [...bq.sel] });
function bqRemember() {
  bqHist.undo.push(bqCloneEntries());
  if (bqHist.undo.length > 100) bqHist.undo.shift();
  bqHist.redo = [];
}
function bqRestore(json) {
  const state = JSON.parse(json);
  bq.entries = state.e; bq.sel = new Set(state.s.filter((i) => i < state.e.length)); bq.anchor = -1;
  bqRenderEntries();
}
function bqUndo() { if (!bqHist.undo.length || bq.busy) return; bqHist.redo.push(bqCloneEntries()); bqRestore(bqHist.undo.pop()); }
function bqRedo() { if (!bqHist.redo.length || bq.busy) return; bqHist.undo.push(bqCloneEntries()); bqRestore(bqHist.redo.pop()); }
function bqClearHistory() { bqHist.undo = []; bqHist.redo = []; }

function bqButtons() {
  const open = Boolean(bq.file), hasSel = bq.sel.size > 0, canMove = hasSel && !bqFilter() && !bq.busy;
  $("#bqSave").disabled = bq.busy || !bqDirty();
  $("#bqUp").disabled = !canMove; $("#bqDown").disabled = !canMove;
  $("#bqDelete").disabled = !hasSel || bq.busy;
  $("#bqRename").disabled = bq.sel.size !== 1 || bq.busy;
  $("#bqCopy").disabled = !hasSel;
  $("#bqPaste").disabled = !open || bq.busy || !bqClip.entries.length;
  $("#bqUndo").disabled = !open || bq.busy || !bqHist.undo.length;
  $("#bqRedo").disabled = !open || bq.busy || !bqHist.redo.length;
  $("#bqMarker").disabled = !open || bq.busy;
  $("#bqAdd").disabled = !open || bq.busy; $("#bqStream").disabled = !open || bq.busy;
  $("#bqMore").disabled = !open || bq.busy;
  $("#bqName").disabled = !open; $("#bqFilter").disabled = !open;
  const listed = bq.lists[bq.kind].some((item) => item.file === bq.file);
  $("#bqListUp").disabled = !listed || bq.busy; $("#bqListDown").disabled = !listed || bq.busy; $("#bqListMore").disabled = !listed || bq.busy;
  if (open) {
    const markers = bq.entries.filter((e) => e.marker).length, streams = bq.entries.filter(bqIsStream).length;
    const channels = bq.entries.length - markers;
    const parts = [bqPlural(channels, "channel", "channels")];
    if (markers) parts.push(bqPlural(markers, "marker", "markers"));
    if (streams) parts.push(`${streams} IPTV`);
    if (bq.sel.size) parts.push(`${bq.sel.size} selected`);
    $("#bqCount").textContent = parts.join(" · ");
  } else $("#bqCount").textContent = "";
  $("#bqClipInfo").textContent = bqClip.entries.length ? `Clipboard: ${bqPlural(bqClip.entries.length, "entry", "entries")}` : "";
  $("#bqTitle").textContent = bqDirty() ? "Bouquet •" : "Bouquet";
  const dirty = bqDirty();
  $("#bqSave").classList.toggle("dirty", dirty);
  $("#listsEdit").classList.toggle("is-dirty", dirty);
}
function bqRenderLists() {
  const list = $("#bqBouquets"), needle = $("#bqListFilter").value.trim().toLowerCase();
  list.replaceChildren();
  let shown = 0;
  for (const item of bq.lists[bq.kind]) {
    if (needle && !item.name.toLowerCase().includes(needle)) continue;
    shown++;
    const active = item.file === bq.file;
    const li = h("li", { role: "option", "aria-selected": String(active), dataset: { file: item.file } },
      h("span", { class: "bq-name", title: item.file, text: active && bqDirty() ? `${item.name} •` : item.name }));
    list.append(li);
  }
  if (!shown) list.append(h("li", { class: "muted empty-row", text: needle ? "No bouquet matches." : "No bouquets" }));
}
function bqRowKind(entry) {
  if (entry.marker) return null;
  if (bqIsStream(entry)) return { text: "IPTV", cls: "stream", title: "IPTV stream" };
  if (bq.namesKnown && !entry.name) return { text: "?", cls: "missing", title: "This channel is not in the receiver's channel list (lamedb)" };
  return { text: bq.kind === "radio" ? "RADIO" : "TV", cls: bq.kind, title: bq.kind === "radio" ? "Radio channel" : "TV channel" };
}
function bqRenderEntries() {
  const list = $("#bqEntries"), filter = bqFilter();
  const scroll = list.scrollTop;
  list.replaceChildren();
  let channelNo = 0;
  const frag = document.createDocumentFragment();
  bq.entries.forEach((entry, index) => {
    if (!entry.marker) channelNo++;
    const label = bqText(entry);
    if (filter && !`${label} ${entry.ref}`.toLowerCase().includes(filter)) return;
    const li = h("li", { role: "option", "aria-selected": String(bq.sel.has(index)), dataset: { index: String(index) }, draggable: !filter });
    if (entry.marker) li.classList.add("marker");
    const no = h("span", { class: "bq-no", text: entry.marker ? "" : String(channelNo) });
    if (entry.marker) {
      li.append(no, h("span", { class: "bq-name", text: label || "(empty marker)" }));
    } else {
      const kind = bqRowKind(entry);
      const custom = !bqIsStream(entry) && entry.description && entry.base && entry.base !== label;
      const nameBox = h("span", { class: "bq-name" }, h("span", { class: "bq-title", text: label || "(name not known)" }),
        custom ? h("span", { class: "bq-was", title: "Name in the receiver's channel list", text: entry.base }) : null);
      li.append(h("span", { class: `bq-kind ${kind.cls}`, title: kind.title, text: kind.text }), no, nameBox);
      if (custom) li.append(h("span", { class: "bq-tag", title: "Renamed in this bouquet", text: "renamed" }));
      li.append(h("span", { class: "bq-ref", title: bqIsStream(entry) ? bqStreamURL(entry) : entry.ref, text: bqIsStream(entry) ? bqStreamURL(entry) : entry.ref }));
    }
    frag.append(li);
  });
  list.append(frag);
  list.scrollTop = scroll;
  bqButtons();
}
function bqSyncSelection() {
  $$("#bqEntries li[data-index]").forEach((li) => li.setAttribute("aria-selected", String(bq.sel.has(Number(li.dataset.index)))));
  bqButtons();
}
function bqScrollTo(index) { $("#bqEntries").querySelector(`li[data-index="${index}"]`)?.scrollIntoView({ block: "nearest" }); }

async function bqDiscardOK(title = "Discard unsaved changes?") {
  if (!bqDirty()) return true;
  return Boolean(await ask({ title, message: "The open bouquet has changes that are not saved yet.", input: false, ok: "Discard changes", danger: true }));
}
function bqError(error) {
  if (error?.code === "file_not_found" || error?.code === "web_unreachable" || error?.code === "web_error" || error?.code === "conflict" || error?.code === "not_text") { bqStatus(error.message, "error"); return; }
  handleOpError(error, "remote");
}
function bqResetOpen() {
  bq.file = ""; bq.entries = []; bq.sel.clear(); bq.anchor = -1; bq.original = ""; bq.hash = "";
  $("#bqName").value = ""; $("#bqFilter").value = "";
  bqClearHistory();
}

async function bqLoadLists(keepOpen = false) {
  bq.dir = $("#bqDir").value.trim() || "/etc/enigma2";
  bqStatus("Loading bouquets…");
  try {
    const data = await api(`${bqURL("bouquets")}?dir=${encodeURIComponent(bq.dir)}`);
    bq.lists = { tv: data.tv || [], radio: data.radio || [] };
    $("#bqSub").textContent = data.dir;
    if (!keepOpen) bqResetOpen();
    bqRenderLists(); bqRenderEntries();
    bqStatus(`${bq.lists.tv.length} TV and ${bq.lists.radio.length} radio bouquets.`);
    if (!keepOpen && bq.lists[bq.kind][0]) bqOpenBouquet(bq.lists[bq.kind][0].file);
  } catch (error) { bq.lists = { tv: [], radio: [] }; bqRenderLists(); bqRenderEntries(); bqError(error); }
}
async function bqOpenBouquet(file) {
  if (file === bq.file || !(await bqDiscardOK())) return;
  bqStatus("Opening…");
  try {
    const data = await api(`${bqURL("bouquet")}?dir=${encodeURIComponent(bq.dir)}&file=${encodeURIComponent(file)}`);
    Object.assign(bq, { file: data.file, entries: data.entries, hash: data.hash, namesKnown: Boolean(data.names_available) });
    bq.sel.clear(); bq.anchor = -1; bqClearHistory();
    $("#bqName").value = data.name; $("#bqFilter").value = "";
    bq.original = bqSnapshot();
    bqRenderLists(); bqRenderEntries();
    bqStatus(data.names_available ? "" : "Channel names were not found (no lamedb in this folder), so only references are shown.");
  } catch (error) { bqError(error); }
}

function bqMoveBlock(indices, target) {
  const picked = [...indices].sort((a, b) => a - b);
  const moving = picked.map((i) => bq.entries[i]);
  const before = picked.filter((i) => i < target).length;
  const rest = bq.entries.filter((_, i) => !picked.includes(i));
  bqRemember();
  rest.splice(target - before, 0, ...moving);
  bq.entries = rest;
  bq.sel = new Set(moving.map((_, n) => target - before + n));
  bq.anchor = target - before;
  bqRenderEntries();
  bqScrollTo(target - before);
}
function bqNudge(delta) {
  if (bqFilter() || !bq.sel.size) return;
  const picked = [...bq.sel].sort((a, b) => a - b);
  if (delta < 0 && picked[0] === 0) return;
  if (delta > 0 && picked[picked.length - 1] === bq.entries.length - 1) return;
  bqMoveBlock(picked, delta < 0 ? picked[0] - 1 : picked[picked.length - 1] + 2);
}
function bqToEdge(end) {
  if (bqFilter() || !bq.sel.size) return;
  bqMoveBlock([...bq.sel], end === "top" ? 0 : bq.entries.length);
}
function bqDeleteSelected() {
  if (!bq.sel.size) return;
  bqRemember();
  const first = Math.min(...bq.sel), count = bq.sel.size;
  bq.entries = bq.entries.filter((_, i) => !bq.sel.has(i));
  bq.sel = new Set(bq.entries.length ? [Math.min(first, bq.entries.length - 1)] : []);
  bqRenderEntries();
  bqStatus(`${bqPlural(count, "entry", "entries")} removed. Undo with Ctrl+Z, or press Save to keep the change.`);
}
function bqInsert(newEntries, at = bq.sel.size ? Math.max(...bq.sel) + 1 : bq.entries.length) {
  bqRemember();
  bq.entries.splice(at, 0, ...newEntries);
  bq.sel = new Set(newEntries.map((_, n) => at + n));
  bq.anchor = at;
  $("#bqFilter").value = "";
  bqRenderEntries();
  bqScrollTo(at);
}
async function bqAddMarker() {
  const title = await ask({ title: "New marker", label: "Marker text", ok: "Add", validate: (v) => (v.trim() ? "" : "Enter some text.") });
  if (!title) return;
  let next = 1;
  for (const entry of bq.entries) { const m = /^1:64:([0-9A-Fa-f]+):/.exec(entry.ref); if (m) next = Math.max(next, parseInt(m[1], 16) + 1); }
  const text = title.trim().slice(0, 100);
  bqInsert([{ ref: `1:64:${next.toString(16).toUpperCase()}:0:0:0:0:0:0:0:`, description: text, name: text, marker: true }]);
}
async function bqRename() {
  if (bq.sel.size !== 1) return;
  const index = [...bq.sel][0], entry = bq.entries[index];
  const stream = bqIsStream(entry);
  const value = await ask({
    title: entry.marker ? "Edit marker" : stream ? "Rename stream" : "Rename channel",
    message: entry.marker || stream ? "" : `This only changes the name inside this bouquet${entry.base ? ` (the receiver's list calls it “${entry.base}”)` : ""}. Leave it empty to go back to the original name.`,
    label: "Name", value: bqText(entry), ok: "Rename",
    validate: (v) => ((entry.marker || stream) && !v ? "Enter some text." : v.length > 100 ? "Use at most 100 characters." : /[\r\n]/.test(v) ? "Use one line." : ""),
  });
  if (value === null) return;
  bqRemember();
  entry.description = value;
  entry.name = value || entry.base || "";
  if (stream) entry.ref = bqSetStreamName(entry.ref, value);
  bqRenderEntries();
}
function bqSortRuns(compare) {
  if (!bq.entries.length) return;
  bqRemember();
  const out = []; let run = [];
  const flush = () => { run.sort(compare); out.push(...run); run = []; };
  for (const entry of bq.entries) { if (entry.marker) { flush(); out.push(entry); } else run.push(entry); }
  flush();
  bq.entries = out; bq.sel.clear(); bq.anchor = -1;
  bqRenderEntries();
}
const bqSortLabel = (e) => bqText(e).toLocaleLowerCase();
const bqSortAZ = () => bqSortRuns((a, b) => bqSortLabel(a).localeCompare(bqSortLabel(b), undefined, { numeric: true }));
const bqSortZA = () => bqSortRuns((a, b) => bqSortLabel(b).localeCompare(bqSortLabel(a), undefined, { numeric: true }));
function bqReverse() {
  if (!bq.entries.length) return;
  bqRemember();
  bq.entries.reverse(); bq.sel.clear(); bq.anchor = -1;
  bqRenderEntries();
}
function bqRemoveDuplicates() {
  const seen = new Set(), keep = [];
  for (const entry of bq.entries) {
    const key = bqNormRef(entry.ref);
    if (!entry.marker && seen.has(key)) continue;
    if (!entry.marker) seen.add(key);
    keep.push(entry);
  }
  const removed = bq.entries.length - keep.length;
  if (!removed) { bqStatus("No duplicate channels found.", "ok"); return; }
  bqRemember();
  bq.entries = keep; bq.sel.clear(); bq.anchor = -1;
  bqRenderEntries();
  bqStatus(`${bqPlural(removed, "duplicate", "duplicates")} removed. Press Save to keep the change.`, "ok");
}
async function bqRemoveMissing() {
  if (!bq.namesKnown) { bqStatus("The receiver's channel list (lamedb) was not found, so missing channels cannot be detected.", "error"); return; }
  const missing = bq.entries.filter((e) => !e.marker && !bqIsStream(e) && !e.name);
  if (!missing.length) { bqStatus("Every channel in this bouquet exists in the receiver's channel list.", "ok"); return; }
  if (!(await ask({ title: "Remove missing channels", message: `${bqPlural(missing.length, "channel is", "channels are")} not in the receiver's channel list any more (for example after a new scan). Remove them from this bouquet?`, input: false, ok: "Remove", danger: true }))) return;
  bqRemember();
  bq.entries = bq.entries.filter((e) => !missing.includes(e)); bq.sel.clear(); bq.anchor = -1;
  bqRenderEntries();
  bqStatus(`${bqPlural(missing.length, "missing channel", "missing channels")} removed. Press Save to keep the change.`, "ok");
}
function bqSelectAll() { bq.sel = new Set(bq.entries.keys()); bqSyncSelection(); }
function bqInvertSelection() { bq.sel = new Set([...bq.entries.keys()].filter((i) => !bq.sel.has(i))); bqSyncSelection(); }

function bqCopySelected(announce = true) {
  if (!bq.sel.size) return false;
  bqClip.entries = [...bq.sel].sort((a, b) => a - b).map((i) => ({ ...bq.entries[i] }));
  bqButtons();
  if (announce) bqStatus(`${bqPlural(bqClip.entries.length, "entry", "entries")} copied. Open any bouquet and press Paste (Ctrl+V).`, "ok");
  return true;
}
function bqCutSelected() { if (bqCopySelected(false)) { bqDeleteSelected(); bqStatus(`${bqPlural(bqClip.entries.length, "entry", "entries")} cut. Open any bouquet and press Paste (Ctrl+V).`, "ok"); } }
function bqPasteClipboard() {
  if (!bq.file || !bqClip.entries.length) return;
  const have = new Set(bq.entries.filter((e) => !e.marker).map((e) => bqNormRef(e.ref)));
  const fresh = bqClip.entries.filter((e) => e.marker || !have.has(bqNormRef(e.ref))).map((e) => ({ ...e }));
  const skipped = bqClip.entries.length - fresh.length;
  if (!fresh.length) { bqStatus("Those channels are already in this bouquet.", "error"); return; }
  bqInsert(fresh);
  bqStatus(`${bqPlural(fresh.length, "entry", "entries")} pasted${skipped ? `, ${skipped} already in the bouquet` : ""}. Press Save to keep the change.`, "ok");
}

async function bqSendEntries(file, indices, move) {
  const target = bq.lists[bq.kind].find((item) => item.file === file);
  if (!target || file === bq.file || !indices.length) return;
  const picked = [...indices].sort((a, b) => a - b).map((i) => bq.entries[i]);
  bq.busy = true; bqButtons(); bqStatus(`${move ? "Moving" : "Copying"} to “${target.name}”…`);
  try {
    const data = await api(`${bqURL("bouquet")}?dir=${encodeURIComponent(bq.dir)}&file=${encodeURIComponent(file)}`);
    const have = new Set(data.entries.filter((e) => !e.marker).map((e) => bqNormRef(e.ref)));
    const fresh = picked.filter((e) => e.marker || !have.has(bqNormRef(e.ref)));
    const skipped = picked.length - fresh.length;
    if (fresh.length) {
      await api(bqURL("bouquet"), { method: "PUT", body: JSON.stringify({
        dir: bq.dir, file, name: data.name,
        entries: [...data.entries, ...fresh].map((e) => ({ ref: e.ref, description: e.description })), expected_hash: data.hash, force: false,
      }) });
    }
    bq.busy = false;
    if (move) { bq.sel = new Set(indices); bqDeleteSelected(); }
    else bqButtons();
    bqStatus(`${bqPlural(fresh.length, "entry", "entries")} ${move ? "moved" : "copied"} to “${target.name}”${skipped ? ` (${skipped} already there)` : ""}.${move ? " Press Save to finish removing them here." : ""}`, "ok");
  } catch (error) { bq.busy = false; bqButtons(); bqError(error); }
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
  if (action === "delete" && !(await ask({ title: "Delete the bouquet", message: `Delete “${$("#bqName").value}” from the ${bq.kind.toUpperCase()} bouquet list and erase the file ${file} from the receiver? This cannot be undone.`, input: false, ok: "Delete", danger: true }))) return;
  try {
    const answer = await api(bqURL("bouquet/list-change"), { method: "POST", body: JSON.stringify({ dir: bq.dir, file, action }) });
    if (answer?.note) toast(answer.note, "error");
    if (action === "remove" || action === "delete") { bqResetOpen(); await bqLoadLists(true); }
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
async function bqDuplicate() {
  if (!bq.file) return;
  const entries = bq.entries.map((e) => ({ ref: e.ref, description: e.description }));
  const name = await ask({ title: "Duplicate bouquet", label: "Name of the copy", value: `${$("#bqName").value.trim()} copy`.slice(0, 120), ok: "Duplicate", validate: (v) => (v.trim() ? "" : "Enter a name.") });
  if (!name) return;
  bq.busy = true; bqButtons(); bqStatus("Creating the copy…");
  try {
    const made = await api(bqURL("bouquet/new"), { method: "POST", body: JSON.stringify({ dir: bq.dir, kind: bq.kind, name: name.trim() }) });
    const fresh = await api(`${bqURL("bouquet")}?dir=${encodeURIComponent(bq.dir)}&file=${encodeURIComponent(made.file)}`);
    await api(bqURL("bouquet"), { method: "PUT", body: JSON.stringify({ dir: bq.dir, file: made.file, name: name.trim(), entries, expected_hash: fresh.hash, force: false }) });
    bq.busy = false;
    const keep = bq.file; bq.file = ""; bq.original = "";
    await bqLoadLists(true);
    bq.file = keep;
    await bqOpenBouquet(made.file);
    bqStatus(`Created “${name.trim()}” with ${bqPlural(entries.length, "entry", "entries")}. Use “Reload on receiver” to apply it.`, "ok");
  } catch (error) { bq.busy = false; bqButtons(); bqError(error); }
}
async function bqReloadReceiver() {
  bqStatus("Asking the receiver to reload…");
  try { await api(bqURL("reload"), { method: "POST", body: "{}" }); bqStatus("The receiver reloaded its channel lists.", "ok"); toast("Receiver reloaded its bouquets"); }
  catch (error) { bqError(error); }
}

const bqStreamEncode = (url) => url.trim().replace(/%/g, "%25").replace(/ /g, "%20").replace(/:/g, "%3a");
function bqStreamId(url) { let hash = 0; for (let i = 0; i < url.length; i++) hash = (Math.imul(hash, 31) + url.charCodeAt(i)) >>> 0; return ((hash % 0xfffe) + 1).toString(16).toUpperCase(); }
const bqStreamSafeName = (name) => name.replace(/[:\r\n]+/g, " ").trim().slice(0, 100);
function bqStreamRef(type, url, name) { return `${type}:0:1:${bqStreamId(url)}:0:0:0:0:0:0:${bqStreamEncode(url)}:${bqStreamSafeName(name)}`; }
function bqStreamParts(ref) { const f = ref.split(":"); return { type: f[0], url: f[10] || "", name: f.slice(11).join(":") }; }
function bqStreamURL(entry) { try { return decodeURIComponent(bqStreamParts(entry.ref).url); } catch { return bqStreamParts(entry.ref).url; } }
function bqStreamHost(entry) { try { const u = new URL(bqStreamURL(entry)); return u.host + (u.pathname.length > 1 ? u.pathname : ""); } catch { return bqStreamURL(entry); } }
function bqSetStreamName(ref, name) { const f = ref.split(":"); return [...f.slice(0, 11), bqStreamSafeName(name)].join(":"); }
const bqStreamURLOK = (url) => /^(https?|rtmp|rtsp|rtp|udp|mms):\/\/\S+$/i.test(url.trim());
function bqParseM3U(text) {
  const out = []; let name = "", group = "";
  for (const raw of text.replace(/^﻿/, "").split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) continue;
    if (line.startsWith("#EXTINF")) {
      const comma = line.lastIndexOf(",");
      name = comma >= 0 ? line.slice(comma + 1).trim() : "";
      const g = /group-title="([^"]*)"/i.exec(line); if (g) group = g[1].trim();
      if (!name) { const t = /tvg-name="([^"]*)"/i.exec(line); if (t) name = t[1].trim(); }
    } else if (line.startsWith("#EXTGRP:")) group = line.slice(8).trim();
    else if (line.startsWith("#")) continue;
    else {
      if (bqStreamURLOK(line)) out.push({ name: name || line, url: line, group });
      name = ""; group = "";
    }
  }
  return out;
}
const bqStream = { mode: "one", parsed: [] };
function bqStreamUpdate() {
  const m3u = bqStream.mode === "m3u";
  $("#bqStreamOne").hidden = m3u; $("#bqStreamM3u").hidden = !m3u;
  $$("#bqStreamMode button").forEach((b) => b.setAttribute("aria-checked", String(b.dataset.mode === bqStream.mode)));
  $("#bqStreamTitle").textContent = m3u ? "Import an M3U playlist" : "Add an IPTV stream";
  if (m3u) {
    bqStream.parsed = bqParseM3U($("#bqM3uText").value);
    $("#bqStreamCount").textContent = $("#bqM3uText").value.trim() ? `${bqPlural(bqStream.parsed.length, "stream", "streams")} found` : "";
    $("#bqStreamOk").textContent = bqStream.parsed.length ? `Add ${bqStream.parsed.length}` : "Add";
  } else { $("#bqStreamCount").textContent = ""; $("#bqStreamOk").textContent = "Add"; }
}
function bqOpenStream() {
  if (!bq.file) return;
  $("#bqStreamSub").textContent = `${bq.kind.toUpperCase()} · ${$("#bqName").value || bq.file}`;
  $("#bqStreamName").value = ""; $("#bqStreamUrl").value = ""; $("#bqM3uText").value = ""; $("#bqM3uFile").value = "";
  $("#bqM3uInfo").textContent = "or paste the playlist below";
  $("#bqStreamError").hidden = true;
  bqStream.mode = "one"; bqStreamUpdate();
  openDialog($("#bqStreamDialog"));
  $("#bqStreamName").focus();
}
function bqStreamFail(message) { const box = $("#bqStreamError"); box.textContent = message; box.hidden = !message; }
function bqStreamConfirm(event) {
  event.preventDefault();
  const type = $("#bqStreamType").value;
  let fresh = [];
  if (bqStream.mode === "one") {
    const url = $("#bqStreamUrl").value.trim(), name = $("#bqStreamName").value.trim();
    if (!name) { bqStreamFail("Enter a name for the channel."); $("#bqStreamName").focus(); return; }
    if (!bqStreamURLOK(url)) { bqStreamFail("Enter a full stream address, for example http://server:8000/live/stream.ts"); $("#bqStreamUrl").focus(); return; }
    fresh = [{ ref: bqStreamRef(type, url, name), description: name, name, marker: false }];
  } else {
    const items = bqStream.parsed;
    if (!items.length) { bqStreamFail("No streams found. A playlist has lines like #EXTINF:-1,Name followed by the address."); return; }
    if (bq.entries.length + items.length > 19000) { bqStreamFail("That would make the bouquet too large."); return; }
    const groups = $("#bqM3uGroups").checked;
    let markerNo = 1;
    for (const entry of bq.entries) { const m = /^1:64:([0-9A-Fa-f]+):/.exec(entry.ref); if (m) markerNo = Math.max(markerNo, parseInt(m[1], 16) + 1); }
    let last = null;
    for (const item of items) {
      if (groups && item.group && item.group !== last) {
        fresh.push({ ref: `1:64:${(markerNo++).toString(16).toUpperCase()}:0:0:0:0:0:0:0:`, description: item.group.slice(0, 100), name: item.group.slice(0, 100), marker: true });
        last = item.group;
      }
      const name = bqStreamSafeName(item.name) || item.url;
      fresh.push({ ref: bqStreamRef(type, item.url, name), description: name, name, marker: false });
    }
  }
  $("#bqStreamDialog").close();
  bqInsert(fresh);
  const n = fresh.filter((e) => !e.marker).length;
  bqStatus(`${bqPlural(n, "stream", "streams")} added. Press Save to keep the change.`, "ok");
}
function bqLoadM3UFile(file) {
  if (!file) return;
  if (file.size > 8 * 1048576) { bqStreamFail("That file is larger than 8 MB."); return; }
  file.text().then((text) => { $("#bqM3uText").value = text; $("#bqM3uInfo").textContent = file.name; bqStreamFail(""); bqStreamUpdate(); });
}

function bqExportM3U() {
  const device = currentDevice();
  const host = (device?.host || "").replace(/^\[|\]$/g, "");
  const chosen = bq.sel.size ? [...bq.sel].sort((a, b) => a - b).map((i) => bq.entries[i]) : bq.entries;
  const lines = ["#EXTM3U"];
  let count = 0;
  for (const entry of chosen) {
    if (entry.marker) continue;
    const name = bqText(entry).replace(/[\r\n,]+/g, " ") || entry.ref;
    const url = bqIsStream(entry) ? bqStreamURL(entry) : host ? `http://${host.includes(":") ? `[${host}]` : host}:8001/${entry.ref}` : "";
    if (!url) continue;
    lines.push(`#EXTINF:-1,${name}`, url);
    count++;
  }
  if (!count) { bqStatus("Nothing to export.", "error"); return; }
  const file = `${($("#bqName").value.trim() || "bouquet").replace(/[^\w.-]+/g, "_")}.m3u`;
  const link = h("a", { href: URL.createObjectURL(new Blob([lines.join("\n") + "\n"], { type: "audio/x-mpegurl" })), download: file });
  document.body.append(link); link.click(); link.remove();
  window.setTimeout(() => URL.revokeObjectURL(link.href), 2000);
  bqStatus(`Exported ${bqPlural(count, "channel", "channels")} to ${file}.`, "ok");
}
async function bqCopyNames() {
  const chosen = bq.sel.size ? [...bq.sel].sort((a, b) => a - b).map((i) => bq.entries[i]) : bq.entries;
  const names = chosen.filter((e) => !e.marker).map(bqText);
  if (await clipboardWrite(names.join("\n"))) bqStatus(`${bqPlural(names.length, "name", "names")} copied to the clipboard.`, "ok");
  else bqStatus("Copy was blocked by the browser.", "error");
}

function bqShowMenu(items, anchor) { const r = anchor.getBoundingClientRect(); openMenu(items, r.left, r.bottom + 4); }
function bqMoreMenu() {
  const sel = bq.sel.size > 0, filtered = Boolean(bqFilter());
  return [
    { heading: "Order" },
    { label: "Sort A–Z", icon: "up", disabled: bq.entries.length < 2 || filtered, run: bqSortAZ },
    { label: "Sort Z–A", icon: "up", disabled: bq.entries.length < 2 || filtered, run: bqSortZA },
    { label: "Reverse order", icon: "refresh", disabled: bq.entries.length < 2 || filtered, run: bqReverse },
    { label: "Move to top", disabled: !sel || filtered, run: () => bqToEdge("top") },
    { label: "Move to bottom", disabled: !sel || filtered, run: () => bqToEdge("bottom") },
    "-", { heading: "Clean up" },
    { label: "Remove duplicates", icon: "eraser", run: bqRemoveDuplicates },
    { label: "Remove missing channels", icon: "eraser", sub: "not in lamedb", run: bqRemoveMissing },
    "-", { heading: "Selection" },
    { label: "Select all", sub: "Ctrl+A", run: bqSelectAll },
    { label: "Invert selection", run: bqInvertSelection },
    "-", { heading: "Share" },
    { label: bq.sel.size ? "Export selection as .m3u" : "Export bouquet as .m3u", icon: "download", run: bqExportM3U },
    { label: "Copy channel names", icon: "copy", run: bqCopyNames },
  ];
}
function bqRowMenu(event) {
  const li = event.target.closest("li[data-index]");
  if (!li) return;
  event.preventDefault();
  const index = Number(li.dataset.index);
  if (!bq.sel.has(index)) { bq.sel = new Set([index]); bq.anchor = index; bqSyncSelection(); }
  const one = bq.sel.size === 1, filtered = Boolean(bqFilter());
  openMenu([
    { label: "Rename…", icon: "edit", sub: "F2", disabled: !one, run: bqRename },
    { label: "Copy", icon: "copy", sub: "Ctrl+C", run: () => bqCopySelected() },
    { label: "Cut", sub: "Ctrl+X", run: bqCutSelected },
    { label: "Paste", icon: "paste", sub: "Ctrl+V", disabled: !bqClip.entries.length, run: bqPasteClipboard },
    "-",
    { label: "Move to top", disabled: filtered, run: () => bqToEdge("top") },
    { label: "Move to bottom", disabled: filtered, run: () => bqToEdge("bottom") },
    "-",
    { label: "Delete", icon: "trash", sub: "Del", danger: true, run: bqDeleteSelected },
  ], event.clientX, event.clientY);
}
function bqListMenu() {
  const keepName = $("#bqName").value;
  return [
    { label: "Rename bouquet", icon: "edit", run: () => { $("#bqName").focus(); $("#bqName").select(); } },
    { label: "Duplicate…", icon: "copy", run: bqDuplicate },
    "-",
    { label: "Remove from the list", sub: "keeps the file", danger: true, run: () => bqListAction("remove") },
    { label: `Delete “${keepName.slice(0, 24)}” file`, icon: "trash", danger: true, run: () => bqListAction("delete") },
  ];
}

const bqAdd = { items: [], sel: new Set(), timer: 0, seq: 0, anchor: -1, have: new Set() };
function bqAddUpdateCount() {
  $("#bqAddCount").textContent = bqAdd.sel.size ? `${bqAdd.sel.size} selected` : "";
  $("#bqAddConfirm").disabled = !bqAdd.sel.size;
  $("#bqAddConfirm").textContent = bqAdd.sel.size > 1 ? `Add ${bqAdd.sel.size}` : "Add selected";
  $("#bqAddAll").disabled = !bqAdd.items.length;
}
function bqAddRender() {
  const list = $("#bqAddList");
  list.replaceChildren();
  bqAdd.items.forEach((svc, index) => {
    const inside = bqAdd.have.has(bqNormRef(svc.ref));
    const li = h("li", { role: "option", "aria-selected": String(bqAdd.sel.has(index)), dataset: { index: String(index) }, class: inside ? "present" : "" },
      h("span", { class: "bq-name", text: svc.name }),
      inside ? h("span", { class: "bq-tag", text: "in bouquet" }) : null,
      h("span", { class: "bq-prov", text: svc.provider || "" }));
    list.append(li);
  });
  bqAddUpdateCount();
}
function bqAddProviders(providers, chosen) {
  const select = $("#bqAddProvider");
  if (select.dataset.kind === bq.kind && select.options.length > 1 && !chosen) return;
  select.replaceChildren(h("option", { value: "", text: "All providers" }));
  for (const p of providers || []) select.append(h("option", { value: p.name, text: `${p.name} (${p.count})` }));
  select.value = chosen || "";
  select.dataset.kind = bq.kind;
}
async function bqAddSearch() {
  const seq = ++bqAdd.seq, q = $("#bqAddSearch").value.trim(), provider = $("#bqAddProvider").value;
  $("#bqAddStatus").textContent = "Searching…";
  try {
    const data = await api(`${bqURL("services")}?dir=${encodeURIComponent(bq.dir)}&kind=${bq.kind}&q=${encodeURIComponent(q)}&provider=${encodeURIComponent(provider)}`);
    if (seq !== bqAdd.seq) return;
    bqAdd.items = data.services || []; bqAdd.sel.clear(); bqAdd.anchor = -1;
    bqAdd.have = new Set(bq.entries.filter((e) => !e.marker).map((e) => bqNormRef(e.ref)));
    bqAddProviders(data.providers, provider);
    bqAddRender();
    $("#bqAddStatus").textContent = !data.total ? "No channel matches." : data.truncated ? `Showing the first ${bqAdd.items.length} of ${data.total}. Pick a provider or type more of the name to narrow it down.` : `${bqPlural(data.total, "channel", "channels")}. Click to select, Shift+click for a range, double-click to add one.`;
  } catch (error) {
    if (seq !== bqAdd.seq) return;
    bqAdd.items = []; bqAdd.sel.clear(); bqAddRender();
    $("#bqAddStatus").textContent = error.message;
    if (error.status === 404 && error.code !== "file_not_found") handleOpError(error, "remote");
  }
}
function bqOpenAdd() {
  if (!bq.file) return;
  $("#bqAddSub").textContent = `${bq.kind.toUpperCase()} · ${$("#bqName").value || bq.file}`;
  $("#bqAddSearch").value = "";
  $("#bqAddProvider").dataset.kind = "";
  bqAdd.items = []; bqAdd.sel.clear(); bqAddRender();
  openDialog($("#bqAddDialog"));
  bqAddSearch();
  $("#bqAddSearch").focus();
}
function bqAddConfirm() {
  const picked = [...bqAdd.sel].sort((a, b) => a - b).map((i) => bqAdd.items[i]);
  if (!picked.length) return;
  const have = new Set(bq.entries.map((e) => bqNormRef(e.ref)));
  const fresh = picked.filter((svc) => !have.has(bqNormRef(svc.ref)));
  const skipped = picked.length - fresh.length;
  $("#bqAddDialog").close();
  if (!fresh.length) { bqStatus("Those channels are already in the bouquet.", "error"); return; }
  bqInsert(fresh.map((svc) => ({ ref: svc.ref, description: "", name: svc.name, base: svc.name, marker: false })));
  bqStatus(`${bqPlural(fresh.length, "channel", "channels")} added${skipped ? `, ${skipped} already in the bouquet` : ""}. Press Save to keep the change.`, "ok");
}
{
  const list = $("#bqAddList");
  list.addEventListener("click", (event) => {
    const li = event.target.closest("li[data-index]");
    if (!li) return;
    const index = Number(li.dataset.index);
    if (event.shiftKey && bqAdd.anchor >= 0) { const [a, b] = [Math.min(bqAdd.anchor, index), Math.max(bqAdd.anchor, index)]; for (let i = a; i <= b; i++) bqAdd.sel.add(i); }
    else { if (!bqAdd.sel.delete(index)) bqAdd.sel.add(index); bqAdd.anchor = index; }
    $$("#bqAddList li[data-index]").forEach((node) => node.setAttribute("aria-selected", String(bqAdd.sel.has(Number(node.dataset.index)))));
    bqAddUpdateCount();
  });
  list.addEventListener("dblclick", (event) => { const li = event.target.closest("li[data-index]"); if (li) { bqAdd.sel = new Set([Number(li.dataset.index)]); bqAddConfirm(); } });
  list.addEventListener("keydown", (event) => { if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "a") { event.preventDefault(); $("#bqAddAll").click(); } });
  $("#bqAddAll").addEventListener("click", () => { bqAdd.sel = new Set(bqAdd.items.map((_, i) => i)); $$("#bqAddList li[data-index]").forEach((n) => n.setAttribute("aria-selected", "true")); bqAddUpdateCount(); });
  $("#bqAddProvider").addEventListener("change", bqAddSearch);
  $("#bqAddSearch").addEventListener("input", () => { window.clearTimeout(bqAdd.timer); bqAdd.timer = window.setTimeout(bqAddSearch, 250); });
  $("#bqAddSearch").addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); window.clearTimeout(bqAdd.timer); bqAddSearch(); } });
  $("#bqAddConfirm").addEventListener("click", bqAddConfirm);
  $("#bqAddClose").addEventListener("click", () => $("#bqAddDialog").close());
  $("#bqAdd").addEventListener("click", bqOpenAdd);
  $("#bqStream").addEventListener("click", bqOpenStream);
  $("#bqStreamForm").addEventListener("submit", bqStreamConfirm);
  $("#bqStreamCancel").addEventListener("click", () => $("#bqStreamDialog").close());
  $("#bqStreamMode").addEventListener("click", (event) => { const b = event.target.closest("button[data-mode]"); if (b) { bqStream.mode = b.dataset.mode; bqStreamFail(""); bqStreamUpdate(); } });
  $("#bqM3uText").addEventListener("input", bqStreamUpdate);
  $("#bqM3uFile").addEventListener("change", (event) => bqLoadM3UFile(event.target.files[0]));
  $("#bqStreamDialog").addEventListener("dragover", (event) => { if (bqStream.mode === "m3u") event.preventDefault(); });
  $("#bqStreamDialog").addEventListener("drop", (event) => { if (bqStream.mode === "m3u" && event.dataTransfer?.files?.length) { event.preventDefault(); bqLoadM3UFile(event.dataTransfer.files[0]); } });
  $("#bqRename").addEventListener("click", bqRename);
  $("#bqCopy").addEventListener("click", () => bqCopySelected());
  $("#bqPaste").addEventListener("click", bqPasteClipboard);
  $("#bqUndo").addEventListener("click", bqUndo);
  $("#bqRedo").addEventListener("click", bqRedo);
  $("#bqMore").addEventListener("click", () => bqShowMenu(bqMoreMenu(), $("#bqMore")));
  $("#bqListMore").addEventListener("click", () => bqShowMenu(bqListMenu(), $("#bqListMore")));
  $("#bqListFilter").addEventListener("input", bqRenderLists);
}
function bqReset() {
  bqResetOpen(); bq.lists = { tv: [], radio: [] }; $("#bqSub").textContent = "";
  bqRenderLists(); bqRenderEntries();
}
function listsMode(mode) {
  $("#listsEdit").hidden = mode !== "edit";
  $("#listsBackup").hidden = mode !== "backup";
  const id = mode === "backup" ? "backup" : "editor";
  if (typeof tools !== "undefined") { tools.current = id; store.set("tool", id); if (typeof updateToolsNav === "function") updateToolsNav(); }
  const title = $("#listsTitle"); if (title) title.firstChild.textContent = mode === "backup" ? "Backup & restore " : "Channel editor ";
  const lead = $("#listsLead");
  if (lead) lead.innerHTML = mode === "backup"
    ? "Save the channel list of the receiver as a <b>.zip</b> file on this device, or put a saved copy back. Make a backup before you change the lists."
    : "Your receiver keeps its channels in <b>bouquets</b> (the favourite lists you zap through). Reorder, rename, add or remove channels and whole bouquets.";
}
function listsEnter() { files.ready.then(listsEnterNow); }
async function listsEnterNow() {
  const device = currentDevice();
  $("#listsDevice").textContent = device?.name || "";
  if (!device) { bqReset(); bqStatus("Add or choose a device first (Devices tab)."); return; }
  syncFilePort();
  if (!files.sessionID && !files.connecting && !files.manualOff) await fileConnect();
  if (!files.sessionID) { bqStatus(files.connecting ? "Connecting…" : "Not connected. Press Connect to read the channel lists."); return; }
  if (bq.profileID !== files.profileID) {
    if (bqDirty()) toast("Unsaved changes of the previous device were discarded", "error");
    bqReset();
    bq.profileID = files.profileID;
    $("#bqDir").value = bq.dir;
    await bqLoadLists();
  }
}
{
  const entries = $("#bqEntries");
  const page = $("#view-lists");
  $("#listsConnect").addEventListener("click", () => { files.manualOff = false; fileConnect().then(listsEnterNow); });
  $("#listsGoBackup").addEventListener("click", (event) => { event.preventDefault(); showTool("backup"); });
  $("#bqLoad").addEventListener("click", async () => { if (await bqDiscardOK()) bqLoadLists(); });
  $("#bqDir").addEventListener("keydown", async (event) => { if (event.key === "Enter") { event.preventDefault(); if (await bqDiscardOK()) bqLoadLists(); } });
  $("#bqSave").addEventListener("click", () => bqSave());
  $("#bqReload").addEventListener("click", bqReloadReceiver);
  $("#bqNew").addEventListener("click", bqNewBouquet);
  $("#bqListUp").addEventListener("click", () => bqListAction("up"));
  $("#bqListDown").addEventListener("click", () => bqListAction("down"));
  $("#bqUp").addEventListener("click", () => bqNudge(-1));
  $("#bqDown").addEventListener("click", () => bqNudge(1));
  $("#bqDelete").addEventListener("click", bqDeleteSelected);
  $("#bqMarker").addEventListener("click", bqAddMarker);
  $("#bqName").addEventListener("input", () => { bqButtons(); bqRenderLists(); });
  $("#bqFilter").addEventListener("input", bqRenderEntries);
  $("#bqKind").addEventListener("click", async (event) => {
    const button = event.target.closest("button[data-kind]");
    if (!button || button.dataset.kind === bq.kind || !(await bqDiscardOK())) return;
    bq.kind = button.dataset.kind;
    $$("#bqKind button").forEach((b) => b.setAttribute("aria-checked", String(b === button)));
    bqResetOpen();
    bqRenderLists(); bqRenderEntries();
    if (bq.lists[bq.kind][0]) bqOpenBouquet(bq.lists[bq.kind][0].file);
  });
  $("#bqBouquets").addEventListener("click", (event) => { const li = event.target.closest("li[data-file]"); if (li) bqOpenBouquet(li.dataset.file); });
  entries.addEventListener("click", (event) => {
    const li = event.target.closest("li[data-index]");
    if (!li) return;
    const index = Number(li.dataset.index);
    if (event.shiftKey && bq.anchor >= 0) { const [a, b] = [Math.min(bq.anchor, index), Math.max(bq.anchor, index)]; bq.sel = new Set(Array.from({ length: b - a + 1 }, (_, n) => a + n)); }
    else if (event.ctrlKey || event.metaKey) { if (!bq.sel.delete(index)) bq.sel.add(index); bq.anchor = index; }
    else { bq.sel = new Set([index]); bq.anchor = index; }
    bqSyncSelection();
  });
  entries.addEventListener("dblclick", (event) => { if (event.target.closest("li[data-index]")) bqRename(); });
  entries.addEventListener("contextmenu", bqRowMenu);
  entries.addEventListener("keydown", (event) => {
    const mod = event.ctrlKey || event.metaKey, key = event.key.toLowerCase();
    if (event.key === "Delete") { event.preventDefault(); bqDeleteSelected(); }
    else if (event.key === "F2" || (event.key === "Enter" && !mod)) { event.preventDefault(); bqRename(); }
    else if (event.altKey && event.key === "ArrowUp") { event.preventDefault(); bqNudge(-1); }
    else if (event.altKey && event.key === "ArrowDown") { event.preventDefault(); bqNudge(1); }
    else if (!mod && !event.altKey && ["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key) && bq.entries.length) {
      event.preventDefault();
      const cur = bq.anchor >= 0 ? bq.anchor : 0;
      const to = event.key === "Home" ? 0 : event.key === "End" ? bq.entries.length - 1 : Math.max(0, Math.min(bq.entries.length - 1, cur + (event.key === "ArrowUp" ? -1 : 1)));
      if (event.shiftKey && bq.anchor >= 0) { const [a, b] = [Math.min(bq.anchor, to), Math.max(bq.anchor, to)]; bq.sel = new Set(Array.from({ length: b - a + 1 }, (_, n) => a + n)); }
      else { bq.sel = new Set([to]); bq.anchor = to; }
      bqSyncSelection(); bqScrollTo(to);
    }
    else if (mod && key === "a") { event.preventDefault(); bqSelectAll(); }
    else if (mod && key === "c") { event.preventDefault(); bqCopySelected(); }
    else if (mod && key === "x") { event.preventDefault(); bqCutSelected(); }
    else if (mod && key === "v") { event.preventDefault(); bqPasteClipboard(); }
    else if (mod && key === "z" && !event.shiftKey) { event.preventDefault(); bqUndo(); }
    else if (mod && (key === "y" || (key === "z" && event.shiftKey))) { event.preventDefault(); bqRedo(); }
  });
  page.addEventListener("keydown", (event) => {
    if (!(event.ctrlKey || event.metaKey) || event.target.closest("input, textarea, select")) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); if (!$("#bqSave").disabled) bqSave(); }
      return;
    }
    const key = event.key.toLowerCase();
    if (key === "s") { event.preventDefault(); if (!$("#bqSave").disabled) bqSave(); }
    else if (key === "z" && !event.shiftKey) { event.preventDefault(); bqUndo(); }
    else if (key === "y" || (key === "z" && event.shiftKey)) { event.preventDefault(); bqRedo(); }
  });
  const clearDrop = () => $$("#bqEntries .drop-before, #bqEntries .drop-after, #bqBouquets .drop-target").forEach((li) => li.classList.remove("drop-before", "drop-after", "drop-target"));
  entries.addEventListener("dragstart", (event) => {
    const li = event.target.closest("li[data-index]");
    if (!li || bqFilter()) { event.preventDefault(); return; }
    const index = Number(li.dataset.index);
    if (!bq.sel.has(index)) { bq.sel = new Set([index]); bqSyncSelection(); }
    bq.dragging = [...bq.sel];
    event.dataTransfer.effectAllowed = "copyMove";
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
  const sideList = $("#bqBouquets");
  sideList.addEventListener("dragover", (event) => {
    if (!bq.dragging.length) return;
    const li = event.target.closest("li[data-file]");
    clearDrop();
    if (!li || li.dataset.file === bq.file) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = event.shiftKey ? "move" : "copy";
    li.classList.add("drop-target");
  });
  sideList.addEventListener("dragleave", (event) => { if (!sideList.contains(event.relatedTarget)) clearDrop(); });
  sideList.addEventListener("drop", (event) => {
    if (!bq.dragging.length) return;
    const li = event.target.closest("li[data-file]");
    if (!li || li.dataset.file === bq.file) return;
    event.preventDefault();
    const dragged = bq.dragging; bq.dragging = []; clearDrop();
    bqSendEntries(li.dataset.file, dragged, event.shiftKey);
  });
  window.addEventListener("beforeunload", (event) => { if (bqDirty()) { event.preventDefault(); event.returnValue = ""; } });
}
async function cmdCopyPath() {
  const pane = panes[files.active], sel = selectedEntries(pane);
  const text = sel.length ? sel.map((e) => e.path).join("\n") : pane.path;
  if (await clipboardWrite(text)) toast(sel.length > 1 ? "Paths copied" : "Path copied");
  else toast("Copy was blocked by the browser", "error");
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
  ({ mkdir: cmdMkdir, edit: cmdEdit, ipk: cmdIpk, rename: cmdRename, chmod: cmdChmod, delete: cmdDelete, copypath: cmdCopyPath, selectall: cmdSelectAll,
     transfer: () => transferSelection(files.active === "remote" ? "download" : "upload") })[button.dataset.cmd]?.();
});
