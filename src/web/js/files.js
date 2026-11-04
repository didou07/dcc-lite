"use strict";
/* ───────────── Files ───────────── */
const files = {
  sessionID: "", profileID: "", protocol: "ftp",
  connecting: false, gen: 0, manualOff: false, active: "remote", drag: null,
  transfers: [], queueTimer: 0, queueBusy: false, seenDone: new Set(),
};
const coarsePointer = matchMedia("(pointer: coarse)").matches;

function makePane(kind) {
  const root = $(`#${kind}Pane`);
  const saved = store.get(`pane.${kind}`, {});
  return {
    kind, root, rows: $(`#${kind}Rows`), table: $(`#${kind}Table`), pathInput: $(`#${kind}Path`), filter: $(`#${kind}Filter`),
    summary: $(`#${kind}Summary`), empty: $(`#${kind}Empty`), hiddenBox: $('[data-role="hidden"]', root),
    entries: [], path: kind === "remote" ? "/" : ".", parent: kind === "remote" ? "/" : ".",
    selected: new Set(), anchor: -1, cursor: -1, visible: [],
    sort: { key: saved.key || "name", dir: saved.dir === "desc" ? "desc" : "asc" },
    showHidden: Boolean(saved.hidden), loaded: false,
  };
}
const panes = { remote: makePane("remote"), local: makePane("local") };
for (const pane of Object.values(panes)) pane.hiddenBox.checked = pane.showHidden;

function sessionLost(error) { return error?.status === 404 || /session not found/i.test(error?.message || ""); }
const requireSession = () => { if (!files.sessionID) { toast("Connect to the device first"); return false; } return true; };

/* Status & buttons */
function setFileStatus(state, text) {
  const pill = $("#fileStatus");
  pill.dataset.state = state;
  pill.textContent = text;
  pill.title = text;
}
function updateFileUI() {
  const connected = Boolean(files.sessionID);
  const device = currentDevice();
  $("#fileConnect").hidden = connected;
  $("#fileConnect").disabled = files.connecting || !device;
  $("#fileDisconnect").hidden = !connected && !files.connecting;
  $("#filePort").disabled = connected || files.connecting;
  $("#remoteHost").textContent = connected ? `${device?.name || ""} · ${device?.host || ""}` : "Not connected";
  const active = panes[files.active];
  const selection = selectedEntries(active);
  const remoteSel = selectedEntries(panes.remote), localSel = selectedEntries(panes.local);
  const can = (cmd, value) => { const b = $(`.actionbar [data-cmd="${cmd}"]`); if (b) b.disabled = !value; };
  const paneReady = files.active === "local" || connected;
  can("mkdir", paneReady); can("rename", paneReady && selection.length === 1); can("chmod", paneReady && selection.length > 0);
  can("delete", paneReady && selection.length > 0); can("copypath", paneReady); can("selectall", paneReady && active.visible.length > 0);
  $("#btnDownload").disabled = !(connected && remoteSel.length);
  $("#btnUpload").disabled = !(connected && localSel.length);
  const transferBtn = $("#btnTransferActive");
  transferBtn.disabled = !(connected && selection.length);
  $("#transferActiveLabel").textContent = files.active === "remote" ? "Download" : "Upload";
  transferBtn.querySelector("use").setAttribute("href", files.active === "remote" ? "#i-download" : "#i-upload");
  $("#fileTerminalHere").disabled = !connected;
  $("#fileRun").disabled = !(connected && remoteSel.length === 1 && !remoteSel[0].is_dir);
  for (const pane of Object.values(panes)) {
    const off = pane.kind === "remote" && !connected;
    pane.pathInput.disabled = off; pane.filter.disabled = off;
    $$(".pathbar .icon-btn", pane.root).forEach((b) => { b.disabled = off; });
  }
}

/* Entries, sorting, rendering */
function compareEntries(pane) {
  const { key, dir } = pane.sort;
  const factor = dir === "desc" ? -1 : 1;
  const byName = (a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true });
  return (a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    let result = 0;
    if (key === "size") result = (a.size || 0) - (b.size || 0);
    else if (key === "modified") result = (Date.parse(a.modified_at || "") || 0) - (Date.parse(b.modified_at || "") || 0);
    else if (key === "mode") result = permissionOctal(a.mode).localeCompare(permissionOctal(b.mode));
    return (result || byName(a, b)) * factor;
  };
}
function computeVisible(pane) {
  const needle = pane.filter.value.trim().toLocaleLowerCase();
  pane.visible = pane.entries
    .filter((e) => (pane.showHidden || !e.name.startsWith(".")) && (!needle || e.name.toLocaleLowerCase().includes(needle)))
    .sort(compareEntries(pane));
  return pane.visible;
}
function entryIcon(entry) {
  if (entry.is_dir) return icon("folder", "dir-ic");
  if (String(entry.mode || "").startsWith("l")) return icon("link", "link-ic");
  return icon("file", "file-ic");
}
function renderPane(pane) {
  const visible = computeVisible(pane);
  const atRoot = pane.kind === "remote" ? (pane.path === "/" || pane.path === ".") : pane.path === ".";
  const fragment = document.createDocumentFragment();
  if (pane.loaded && !atRoot) {
    fragment.append(h("tr", { class: "row up", dataset: { up: "1" } }, h("td", { class: "name" }, icon("up", "dir-ic"), h("span", { text: ".." })), h("td"), h("td", { class: "hide-sm" }), h("td", { class: "hide-sm" })));
  }
  visible.forEach((entry, index) => {
    fragment.append(h("tr", {
      class: `row${pane.selected.has(entry.path) ? " selected" : ""}${index === pane.cursor ? " focus" : ""}`, dataset: { index }, draggable: "true",
    },
      h("td", { class: "name", title: entry.name }, entryIcon(entry), h("span", { text: entry.name })),
      h("td", { class: "num muted", text: entry.is_dir ? "—" : formatBytes(entry.size) }),
      h("td", { class: "muted hide-sm", text: formatDate(entry.modified_at) }),
      h("td", { class: "mode hide-sm", text: permissionOctal(entry.mode) })));
  });
  pane.rows.replaceChildren(fragment);
  $$("th[data-sort]", pane.root).forEach((th) => { th.dataset.dir = th.dataset.sort === pane.sort.key ? pane.sort.dir : ""; });
  const message = !pane.loaded ? (pane.kind === "remote" ? (files.connecting ? "Connecting…" : "Connect to a device to browse its files") : "Loading…")
    : !pane.entries.length ? "This folder is empty" : !visible.length ? "No items match the filter" : "";
  pane.empty.textContent = message;
  pane.empty.hidden = !message;
  updateSelectionUI(pane);
}
function updateSelectionUI(pane) {
  const rows = $$("tr.row[data-index]", pane.rows);
  rows.forEach((row) => {
    const entry = pane.visible[Number(row.dataset.index)];
    row.classList.toggle("selected", Boolean(entry) && pane.selected.has(entry.path));
    row.classList.toggle("focus", Number(row.dataset.index) === pane.cursor && files.active === pane.kind);
  });
  const selection = selectedEntries(pane);
  const folders = pane.entries.filter((e) => e.is_dir).length;
  const total = `${pane.entries.length} item${pane.entries.length === 1 ? "" : "s"}${folders ? ` · ${folders} folder${folders === 1 ? "" : "s"}` : ""}`;
  const bytes = selection.filter((e) => !e.is_dir).reduce((sum, e) => sum + (e.size || 0), 0);
  pane.summary.textContent = pane.loaded ? (selection.length ? `${selection.length} selected${bytes ? ` · ${formatBytes(bytes)}` : ""}  —  ${total}` : total) : "—";
  updateFileUI();
}
const selectedEntries = (pane) => pane.entries.filter((entry) => pane.selected.has(entry.path));

function setActivePane(kind) {
  files.active = kind;
  for (const pane of Object.values(panes)) pane.root.classList.toggle("active", pane.kind === kind);
  $("#commander").dataset.activePane = kind;
  $$("#paneSwitch button").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.pane === kind)));
  updateSelectionUI(panes[kind]);
  const other = panes[kind === "remote" ? "local" : "remote"];
  updateSelectionUI(other);
}

/* Loading */
async function loadPane(kind, path, { keepSelection = false, quiet = false } = {}) {
  const pane = panes[kind];
  if (kind === "remote" && !files.sessionID) return false;
  const sessionAtStart = files.sessionID, gen = files.gen;
  try {
    const result = kind === "local"
      ? await api(`/api/v1/files/local/list?path=${encodeURIComponent(path)}`)
      : await api(`/api/v1/files/sessions/${encodeURIComponent(sessionAtStart)}/list?path=${encodeURIComponent(path)}`);
    if (kind === "remote" && (gen !== files.gen || sessionAtStart !== files.sessionID)) return false;
    const previous = keepSelection && pane.path === result.path ? new Set(pane.selected) : new Set();
    pane.path = result.path; pane.parent = result.parent; pane.entries = result.entries || [];
    pane.selected = new Set([...previous].filter((p) => pane.entries.some((e) => e.path === p)));
    pane.anchor = -1; pane.cursor = -1; pane.loaded = true;
    pane.pathInput.value = result.path;
    if (kind === "local") $("#localRoot").textContent = result.root ? `${result.root} · home` : "Home folder";
    renderPane(pane);
    if (kind === "local") store.set("localPath", result.path);
    else if (files.profileID) store.set(`remotePath.${files.profileID}.${files.protocol}`, result.path);
    return true;
  } catch (error) {
    if (kind === "remote" && sessionLost(error)) { handleSessionLost(); return false; }
    if (!quiet) toastError(error);
    pane.pathInput.value = pane.path;
    return false;
  }
}
function handleSessionLost() {
  if (!files.sessionID) return;
  files.sessionID = "";
  panes.remote.loaded = false; panes.remote.entries = []; panes.remote.selected.clear();
  setFileStatus("error", "Connection lost");
  renderPane(panes.remote);
  toast("The file connection was closed. Press Connect to reconnect.", "error");
}
const goUp = (kind) => loadPane(kind, panes[kind].parent || (kind === "remote" ? "/" : "."));
const goRoot = (kind) => loadPane(kind, kind === "remote" ? "/" : ".");
const refreshPane = (kind) => loadPane(kind, panes[kind].path, { keepSelection: true });
async function refreshBoth() { await Promise.all([refreshPane("local"), files.sessionID ? refreshPane("remote") : null]); }

/* Connect / disconnect */
function syncFilePort() {
  const device = currentDevice();
  $("#filePort").value = String(defaultPort(device, files.protocol));
}
async function fileConnect() {
  const device = currentDevice();
  if (!device) { toast("Add a device first"); showView("devices"); return; }
  if (files.sessionID || files.connecting) return;
  const port = Number($("#filePort").value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) { toast("Enter a valid port", "error"); return; }
  const gen = ++files.gen;
  const protocol = files.protocol;
  files.connecting = true; files.manualOff = false;
  setFileStatus("connecting", `Connecting to ${device.host}…`);
  renderPane(panes.remote); updateFileUI();
  let opened = "";
  try {
    const payload = { profile_id: device.id, protocol, port };
    const result = await post("/api/v1/files/sessions", payload);
    opened = result.session.id;
    if (gen !== files.gen) { api(`/api/v1/files/sessions/${encodeURIComponent(opened)}`, { method: "DELETE" }).catch(() => {}); return; }
    files.sessionID = opened; files.profileID = device.id;
    setFileStatus("connected", `${protocol.toUpperCase()} · ${device.host}`);
    const remembered = store.get(`remotePath.${device.id}.${protocol}`, "/");
    let ok = await loadPane("remote", remembered, { quiet: true });
    if (!ok && remembered !== "/") ok = await loadPane("remote", "/", { quiet: true });
    if (!ok) ok = await loadPane("remote", ".", { quiet: true });
    if (!ok && !files.sessionID) throw new Error("Connected, but the folder list could not be read.");
    if (!ok) toast("Connected, but the folder could not be listed.", "error");
    loadTransfers();
  } catch (error) {
    if (opened) api(`/api/v1/files/sessions/${encodeURIComponent(opened)}`, { method: "DELETE" }).catch(() => {});
    if (gen === files.gen) {
      files.sessionID = "";
      setFileStatus("error", error.message);
      toastError(error);
    }
  } finally {
    if (gen === files.gen) { files.connecting = false; renderPane(panes.remote); }
    updateFileUI();
  }
}
async function fileDisconnect(silent = false) {
  files.gen++;
  const id = files.sessionID;
  files.sessionID = ""; files.connecting = false; files.manualOff = !silent;
  if (id) await api(`/api/v1/files/sessions/${encodeURIComponent(id)}`, { method: "DELETE" }).catch(() => {});
  const remote = panes.remote;
  remote.entries = []; remote.selected.clear(); remote.loaded = false; remote.path = "/"; remote.parent = "/"; remote.pathInput.value = "/";
  setFileStatus("idle", "Not connected");
  renderPane(remote); updateFileUI();
  if (id && !silent) toast("Disconnected");
}
function filesEnter() {
  syncFilePort();
  if (!panes.local.loaded) loadPane("local", store.get("localPath", "."), { quiet: true }).then((ok) => { if (!ok) loadPane("local", ".", { quiet: true }); });
  if (!currentDevice()) { setFileStatus("idle", "No device selected"); renderPane(panes.remote); updateFileUI(); return; }
  if (!files.sessionID && !files.connecting && !files.manualOff) fileConnect();
  else updateFileUI();
  loadTransfers();
}
$("#fileConnect").addEventListener("click", () => { files.manualOff = false; fileConnect(); });
$("#fileDisconnect").addEventListener("click", () => fileDisconnect(false));

/* Navigation inputs */
for (const pane of Object.values(panes)) {
  pane.pathInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter") { event.preventDefault(); loadPane(pane.kind, pane.pathInput.value.trim() || (pane.kind === "remote" ? "/" : "."), {}).then(() => pane.table.focus()); }
    if (event.key === "Escape") { pane.pathInput.value = pane.path; pane.table.focus(); }
  });
  pane.filter.addEventListener("input", () => { pane.cursor = -1; renderPane(pane); });
  pane.hiddenBox.addEventListener("change", () => { pane.showHidden = pane.hiddenBox.checked; saveSort(pane); renderPane(pane); });
  $$("th[data-sort]", pane.root).forEach((th) => th.addEventListener("click", () => {
    const key = th.dataset.sort;
    pane.sort = { key, dir: pane.sort.key === key && pane.sort.dir === "asc" ? "desc" : "asc" };
    saveSort(pane); renderPane(pane);
  }));
  $$(".pathbar .icon-btn", pane.root).forEach((button) => button.addEventListener("click", (event) => {
    const act = button.dataset.act;
    if (act === "up") goUp(pane.kind); else if (act === "root") goRoot(pane.kind); else if (act === "refresh") refreshPane(pane.kind);
    else if (act === "bookmarks") { const rect = button.getBoundingClientRect(); openBookmarks(pane.kind, rect.left, rect.bottom + 4); }
  }));
  pane.root.addEventListener("pointerdown", () => { if (files.active !== pane.kind) setActivePane(pane.kind); });
}
function saveSort(pane) { store.set(`pane.${pane.kind}`, { key: pane.sort.key, dir: pane.sort.dir, hidden: pane.showHidden }); }
$$("#paneSwitch button").forEach((button) => button.addEventListener("click", () => setActivePane(button.dataset.pane)));

/* Row interaction */
function rowEntry(pane, row) { return row && row.dataset.index !== undefined ? pane.visible[Number(row.dataset.index)] : null; }
function openEntry(pane, entry) {
  if (!entry) return;
  if (entry.is_dir) loadPane(pane.kind, entry.path);
  else if (pane.kind === "remote") transferSelection("download", [entry]);
  else transferSelection("upload", [entry]);
}
for (const pane of Object.values(panes)) {
  pane.rows.addEventListener("click", (event) => {
    const row = event.target.closest("tr.row");
    if (!row) return;
    setActivePane(pane.kind);
    if (row.dataset.up) { if (coarsePointer || event.detail === 1) goUp(pane.kind); return; }
    const index = Number(row.dataset.index), entry = pane.visible[index];
    if (!entry) return;
    if (coarsePointer && !event.ctrlKey && !event.shiftKey) {
      if (entry.is_dir) { loadPane(pane.kind, entry.path); return; }
      if (pane.selected.has(entry.path)) pane.selected.delete(entry.path); else pane.selected.add(entry.path);
      pane.anchor = pane.cursor = index; updateSelectionUI(pane); return;
    }
    if (event.shiftKey && pane.anchor >= 0) {
      const [from, to] = [Math.min(pane.anchor, index), Math.max(pane.anchor, index)];
      if (!(event.ctrlKey || event.metaKey)) pane.selected.clear();
      for (let i = from; i <= to; i++) pane.selected.add(pane.visible[i].path);
    } else if (event.ctrlKey || event.metaKey) {
      if (pane.selected.has(entry.path)) pane.selected.delete(entry.path); else pane.selected.add(entry.path);
      pane.anchor = index;
    } else { pane.selected = new Set([entry.path]); pane.anchor = index; }
    pane.cursor = index;
    updateSelectionUI(pane);
  });
  pane.rows.addEventListener("dblclick", (event) => {
    if (coarsePointer) return;
    const row = event.target.closest("tr.row");
    if (!row || row.dataset.up) { if (row?.dataset.up) goUp(pane.kind); return; }
    openEntry(pane, rowEntry(pane, row));
  });
  pane.table.addEventListener("contextmenu", (event) => {
    event.preventDefault();
    if (pane.kind === "remote" && !files.sessionID) return;
    setActivePane(pane.kind);
    const row = event.target.closest("tr.row[data-index]");
    const entry = rowEntry(pane, row);
    if (entry && !pane.selected.has(entry.path)) { pane.selected = new Set([entry.path]); pane.anchor = pane.cursor = Number(row.dataset.index); updateSelectionUI(pane); }
    openMenu(contextItems(pane, Boolean(entry)), event.clientX, event.clientY);
  });
  /* drag & drop between the two panels */
  pane.rows.addEventListener("dragstart", (event) => {
    const row = event.target.closest("tr.row[data-index]");
    const entry = rowEntry(pane, row);
    if (!entry || (pane.kind === "remote" && !files.sessionID)) { event.preventDefault(); return; }
    if (!pane.selected.has(entry.path)) { pane.selected = new Set([entry.path]); updateSelectionUI(pane); }
    files.drag = { from: pane.kind, entries: selectedEntries(pane) };
    event.dataTransfer.effectAllowed = "copy";
    event.dataTransfer.setData("text/plain", files.drag.entries.map((e) => e.name).join("\n"));
    row.classList.add("dragging");
  });
  pane.rows.addEventListener("dragend", () => { files.drag = null; $$(".dragging", pane.rows).forEach((r) => r.classList.remove("dragging")); Object.values(panes).forEach((p) => p.root.classList.remove("drop")); });
  pane.root.addEventListener("dragover", (event) => {
    if (!files.drag || files.drag.from === pane.kind) return;
    event.preventDefault(); event.dataTransfer.dropEffect = "copy"; pane.root.classList.add("drop");
  });
  pane.root.addEventListener("dragleave", (event) => { if (!pane.root.contains(event.relatedTarget)) pane.root.classList.remove("drop"); });
  pane.root.addEventListener("drop", (event) => {
    if (!files.drag || files.drag.from === pane.kind) return;
    event.preventDefault(); pane.root.classList.remove("drop");
    const dragged = files.drag; files.drag = null;
    transferSelection(dragged.from === "remote" ? "download" : "upload", dragged.entries);
  });
}

function contextItems(pane, onEntry) {
  const kind = pane.kind, sel = selectedEntries(pane), single = sel.length === 1 ? sel[0] : null;
  const items = [];
  if (onEntry) {
    if (single?.is_dir) items.push({ label: "Open", icon: "folder", run: () => loadPane(kind, single.path) });
    items.push(kind === "remote"
      ? { label: sel.length > 1 ? `Download ${sel.length} items` : "Download", icon: "download", run: () => transferSelection("download") }
      : { label: sel.length > 1 ? `Upload ${sel.length} items` : "Upload", icon: "upload", run: () => transferSelection("upload") });
    items.push("-");
    if (kind === "remote") items.push({ label: "Edit as text", icon: "file", sub: "F4", disabled: !single || single.is_dir, run: () => cmdEdit() });
    items.push({ label: "Rename", icon: "edit", sub: "F2", disabled: !single, run: () => cmdRename() });
    items.push({ label: "Permissions…", icon: "lock", sub: "F9", run: () => cmdChmod() });
    items.push({ label: "Copy path", icon: "copy", run: () => cmdCopyPath() });
    if (kind === "remote") {
      items.push("-");
      items.push({ label: "Terminal here", icon: "terminal", run: () => openTerminalHere(single?.is_dir ? single.path : pane.path) });
      if (single && !single.is_dir) items.push({ label: "Run in terminal…", icon: "play", run: () => runRemoteFile() });
    }
    items.push("-");
    items.push({ label: sel.length > 1 ? `Delete ${sel.length} items` : "Delete", icon: "trash", sub: "F8", danger: true, run: () => cmdDelete() });
  } else {
    items.push({ label: "New folder", icon: "newfolder", sub: "F7", run: () => cmdMkdir() });
    if (kind === "remote") items.push({ label: "New text file…", icon: "file", sub: "⇧F4", run: () => cmdNewFile() });
    items.push({ label: "Refresh", icon: "refresh", run: () => refreshPane(kind) });
    items.push({ label: "Select all", icon: "check", run: () => cmdSelectAll() });
    if (kind === "remote") items.push("-", { label: "Terminal here", icon: "terminal", run: () => openTerminalHere(pane.path) });
  }
  return items;
}

/* Bookmarks */
function bookmarkKey(kind) { return kind === "remote" ? `bookmarks.remote.${files.profileID || devices.currentID}` : "bookmarks.local"; }
const ENIGMA2_QUICK_JUMP = [
  ["Settings & bouquets", "/etc/enigma2"],
  ["Recordings", "/media/hdd/movie"],
  ["Hard disk", "/media/hdd"],
  ["USB drive", "/media/usb"],
  ["Plugins", "/usr/lib/enigma2/python/Plugins"],
  ["Skins", "/usr/share/enigma2"],
  ["Softcam config", "/etc/tuxbox/config"],
  ["Keys", "/var/keys"],
  ["Temporary files", "/tmp"],
  ["Root", "/"],
];
function openBookmarks(kind, x, y) {
  const pane = panes[kind];
  const list = store.get(bookmarkKey(kind), []);
  const has = list.includes(pane.path);
  const items = [];
  if (kind === "remote") {
    items.push({ heading: "Quick jump (Enigma2)" });
    for (const [label, path] of ENIGMA2_QUICK_JUMP) items.push({ label, icon: "folder", sub: path.length > 22 ? "…" + path.slice(-20) : path, run: () => loadPane(kind, path) });
    items.push("-");
  }
  items.push({ heading: "Bookmarks" });
  if (!list.length) items.push({ label: "No bookmarks yet", disabled: true });
  for (const path of list) items.push({ label: path, icon: "folder", run: () => loadPane(kind, path) });
  items.push("-");
  items.push(has
    ? { label: "Remove this folder", icon: "x", danger: true, run: () => { store.set(bookmarkKey(kind), list.filter((p) => p !== pane.path)); toast("Bookmark removed"); } }
    : { label: "Bookmark this folder", icon: "star", run: () => { store.set(bookmarkKey(kind), [...list, pane.path].slice(-30)); toast("Bookmark added"); } });
  openMenu(items, x, y);
}

/* File operations */
async function cmdMkdir() {
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  const name = await ask({ title: "New folder", label: "Folder name", ok: "Create", validate: (v) => (validName(v) ? "" : "Enter a valid folder name (no slashes).") });
  if (!name) return;
  try {
    if (kind === "local") await post("/api/v1/files/local/mkdir", { path: joinLocal(panes.local.path, name) });
    else await post(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/mkdir`, { path: joinRemote(panes.remote.path, name) });
    await refreshPane(kind);
    toast(`Folder “${name}” created`);
  } catch (error) { handleOpError(error, kind); }
}
async function cmdRename() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (sel.length !== 1) { toast("Select one item to rename"); return; }
  if (kind === "remote" && !requireSession()) return;
  const entry = sel[0];
  const name = await ask({ title: "Rename", label: "New name", value: entry.name, ok: "Rename", validate: (v) => (validName(v) ? "" : "Enter a valid name (no slashes).") });
  if (!name || name === entry.name) return;
  const parentPath = kind === "local" ? panes.local.path : panes.remote.path;
  const target = kind === "local" ? joinLocal(parentPath, name) : joinRemote(parentPath, name);
  try {
    if (kind === "local") await post("/api/v1/files/local/rename", { old_path: entry.path, new_path: target });
    else await post(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/rename`, { old_path: entry.path, new_path: target });
    panes[kind].selected = new Set([target]);
    await loadPane(kind, panes[kind].path, { keepSelection: true });
    toast("Renamed");
  } catch (error) { handleOpError(error, kind); }
}
async function cmdDelete() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to delete"); return; }
  if (kind === "remote" && !requireSession()) return;
  const names = sel.slice(0, 6).map((e) => `${e.is_dir ? "📁" : "•"} ${e.name}`).join("\n") + (sel.length > 6 ? `\n… and ${sel.length - 6} more` : "");
  const where = kind === "remote" ? "on the device" : "on this computer";
  const confirmed = await ask({ title: `Delete ${sel.length === 1 ? "item" : `${sel.length} items`}?`, message: `${names}\n\nThis permanently deletes the selection ${where}${sel.some((e) => e.is_dir) ? ", including everything inside folders" : ""}. It cannot be undone.`, input: false, ok: "Delete", danger: true });
  if (!confirmed) return;
  let failed = 0, last = null;
  for (const entry of sel) {
    try {
      if (kind === "local") await post("/api/v1/files/local/remove", { path: entry.path });
      else await post(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/remove`, { path: entry.path, directory: entry.is_dir });
    } catch (error) { failed++; last = error; if (sessionLost(error)) break; }
  }
  await refreshPane(kind);
  if (failed) { toast(`${failed} item(s) could not be deleted: ${last.message}`, "error"); if (sessionLost(last)) handleSessionLost(); }
  else toast(`${sel.length} item${sel.length === 1 ? "" : "s"} deleted`);
}
async function cmdChmod() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items first"); return; }
  if (kind === "remote" && !requireSession()) return;
  const mode = await ask({ title: "Permissions", message: sel.length === 1 ? `${sel[0].name} — now ${permissionOctal(sel[0].mode)}` : `${sel.length} items`, label: "Octal mode (for example 755, 644, 777)", value: sel.length === 1 && /^[0-7]{3,4}$/.test(permissionOctal(sel[0].mode)) ? permissionOctal(sel[0].mode) : "755", ok: "Apply", validate: (v) => (/^[0-7]{3,4}$/.test(v) ? "" : "Use three or four octal digits, like 755.") });
  if (!mode) return;
  let failed = 0, last = null;
  for (const entry of sel) {
    try {
      if (kind === "local") await post("/api/v1/files/local/chmod", { path: entry.path, mode });
      else await post(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/chmod`, { path: entry.path, mode });
    } catch (error) { failed++; last = error; }
  }
  await loadPane(kind, panes[kind].path, { keepSelection: true });
  if (failed) toast(`Permissions failed for ${failed} item(s): ${last.message}`, "error"); else toast(`Permissions set to ${mode}`);
}
/* Remote text editor (FTP) */
const editor = { path: "", name: "", size: 0, modifiedAt: "", hash: "", lineEnding: "lf", create: false, original: "", busy: false };
const editorURL = () => `/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/content`;
const editorDirty = () => $("#editorText").value !== editor.original;
function editorStatus(message, kind = "") { const node = $("#editorStatus"); node.textContent = message; node.dataset.kind = kind; }
function editorRender() {
  const dirty = editorDirty();
  $("#editorTitle").textContent = editor.name + (dirty ? " •" : "");
  $("#editorSave").disabled = editor.busy || (!dirty && !editor.create);
  $("#editorReload").disabled = editor.busy || editor.create;
  $("#editorMeta").textContent = `${formatBytes(editor.size)} · ${editor.lineEnding === "crlf" ? "CRLF" : editor.lineEnding === "mixed" ? "mixed → LF" : "LF"}${editor.create ? " · new file" : ""}`;
  const notice = $("#editorNotice");
  notice.hidden = editor.lineEnding !== "mixed";
  notice.textContent = editor.lineEnding === "mixed" ? "This file mixes Windows (CRLF) and Unix (LF) line endings. Saving converts every line ending to LF." : "";
}
function editorCursor() {
  const text = $("#editorText"), before = text.value.slice(0, text.selectionStart);
  const line = before.split("\n").length;
  $("#editorCursor").textContent = `Ln ${line}, Col ${before.length - before.lastIndexOf("\n")}`;
}
function showEditor(file, create) {
  Object.assign(editor, {
    path: file.path, name: file.path.split("/").filter(Boolean).pop() || file.path, size: Number(file.size) || 0,
    modifiedAt: file.modified_at || "", hash: file.hash || "", lineEnding: file.line_ending || "lf", create, original: file.content, busy: false,
  });
  const text = $("#editorText");
  text.value = file.content;
  $("#editorPath").textContent = file.path;
  $("#editorPath").title = file.path;
  editorStatus(create ? "New file — it is created on the device when you save." : "");
  const wrap = store.get("editorWrap", false);
  $("#editorWrap").checked = wrap;
  text.setAttribute("wrap", wrap ? "soft" : "off");
  editorRender();
  openDialog($("#editorDialog"));
  text.focus();
  text.setSelectionRange(0, 0);
  text.scrollTop = 0;
  editorCursor();
}
function editorError(error) {
  if (error?.code === "file_not_found") { toast(error.message, "error"); refreshPane("remote"); return; }
  handleOpError(error, "remote");
}
async function openEditor(remotePath) {
  try {
    const data = await api(`${editorURL()}?path=${encodeURIComponent(remotePath)}`);
    showEditor(data.file, false);
  } catch (error) { editorError(error); }
}
async function cmdEdit() {
  if (files.active !== "remote") { toast("Editing works on device files — select a file in the Remote panel"); return; }
  if (!requireSession()) return;
  const sel = selectedEntries(panes.remote);
  if (sel.length !== 1 || sel[0].is_dir) { toast("Select one file to edit"); return; }
  await openEditor(sel[0].path);
}
async function cmdNewFile() {
  if (!requireSession()) return;
  const name = await ask({ title: "New text file", label: "File name", ok: "Create", validate: (v) => (validName(v) ? "" : "Enter a valid file name (no slashes).") });
  if (!name) return;
  showEditor({ path: joinRemote(panes.remote.path, name), content: "", size: 0, line_ending: "lf" }, true);
}
async function editorSave(force = false) {
  if (editor.busy) return;
  editor.busy = true; editorRender(); editorStatus("Saving…");
  const content = $("#editorText").value;
  try {
    const data = await api(editorURL(), {
      method: "PUT",
      body: JSON.stringify({
        path: editor.path, content, line_ending: editor.lineEnding, expected_size: editor.size,
        expected_modified_at: editor.modifiedAt || undefined, expected_hash: editor.hash || undefined, force, create: editor.create,
      }),
    });
    Object.assign(editor, {
      size: Number(data.file.size) || 0, modifiedAt: data.file.modified_at || "", hash: data.file.hash || "", lineEnding: data.file.line_ending || "lf",
      create: false, original: content,
    });
    editor.busy = false; editorRender();
    editorStatus(data.file.mode_unknown
      ? `Saved ${new Date().toLocaleTimeString()} — the device did not report permissions; if this is a script, check them with Permissions (F9).`
      : `Saved ${new Date().toLocaleTimeString()}`, data.file.mode_unknown ? "" : "ok");
    if (panes.remote.path === RemoteParentPath(editor.path)) loadPane("remote", panes.remote.path, { keepSelection: true });
  } catch (error) {
    editor.busy = false; editorRender();
    editorStatus(error.message, "error");
    if (error.code === "conflict") {
      const overwrite = await ask({ title: "File changed on the device", message: `“${editor.name}” was modified on the device after you opened it.\n\nOverwrite it with your version, or cancel and use Reload to see the device’s version (your edits stay in the editor).`, input: false, ok: "Overwrite", danger: true });
      if (overwrite) await editorSave(true);
    } else if (error.code === "exists") {
      editor.create = false;
      editorRender();
    } else if (sessionLost(error) && error.code !== "file_not_found") {
      handleSessionLost();
    }
  }
}
const RemoteParentPath = (value) => { const index = value.lastIndexOf("/"); return index <= 0 ? "/" : value.slice(0, index); };
async function editorDiscardOK(title) {
  if (!editorDirty()) return true;
  return Boolean(await ask({ title, message: `“${editor.name}” has unsaved changes.`, input: false, ok: "Discard changes", danger: true }));
}
async function editorClose() {
  if (editor.busy) return;
  if (!(await editorDiscardOK("Close without saving?"))) return;
  $("#editorText").value = editor.original;
  $("#editorDialog").close();
}
async function editorReload() {
  if (editor.busy || editor.create) return;
  if (!(await editorDiscardOK("Reload from the device?"))) return;
  try {
    const data = await api(`${editorURL()}?path=${encodeURIComponent(editor.path)}`);
    showEditor(data.file, false);
    editorStatus("Reloaded from the device", "ok");
  } catch (error) { editorStatus(error.message, "error"); editorError(error); }
}
{
  const dialog = $("#editorDialog"), text = $("#editorText");
  text.addEventListener("input", () => { editorRender(); if ($("#editorStatus").dataset.kind) editorStatus(""); });
  for (const type of ["keyup", "click", "focus"]) text.addEventListener(type, editorCursor);
  dialog.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); if (!$("#editorSave").disabled) editorSave(); }
  });
  dialog.addEventListener("cancel", (event) => { event.preventDefault(); editorClose(); });
  $("#editorSave").addEventListener("click", () => editorSave());
  $("#editorClose").addEventListener("click", editorClose);
  $("#editorReload").addEventListener("click", editorReload);
  $("#editorWrap").addEventListener("change", (event) => { store.set("editorWrap", event.target.checked); text.setAttribute("wrap", event.target.checked ? "soft" : "off"); });
  window.addEventListener("beforeunload", (event) => { if (dialog.open && editorDirty()) { event.preventDefault(); event.returnValue = ""; } });
}
