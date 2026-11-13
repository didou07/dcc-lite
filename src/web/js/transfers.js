"use strict";
async function transferSelection(direction, given = null, destPath = null) {
  if (!requireSession()) return;
  if (files.browserMode) {
    if (direction === "download") return downloadToDevice(given || selectedEntries(panes.remote));
    return chooseDeviceFiles(false);
  }
  const source = direction === "upload" ? panes.local : panes.remote;
  const entries = given || selectedEntries(source);
  if (!entries.length) { toast(direction === "upload" ? "Select local items to upload" : "Select remote items to download"); return; }
  const destPane = direction === "upload" ? panes.remote : panes.local;
  const destBase = destPath || destPane.path;
  let existing;
  try {
    const listing = direction === "upload"
      ? await api(sessionURL(files.sessionID, `list?path=${encodeURIComponent(destBase)}`))
      : await api(`/api/v1/files/local/list?path=${encodeURIComponent(destBase)}`);
    existing = new Set((listing.entries || []).map((e) => e.name));
  } catch (error) { handleOpError(error, direction === "upload" ? "remote" : "local"); return; }
  const clashes = entries.filter((e) => existing.has(e.name));
  let overwrite = false;
  if (clashes.length) {
    const list = clashes.slice(0, 5).map((e) => `• ${e.name}`).join("\n") + (clashes.length > 5 ? `\n… and ${clashes.length - 5} more` : "");
    const answer = await ask({ title: "Replace existing items?", message: `${list}\n\nalready exist${clashes.length === 1 ? "s" : ""} ${direction === "upload" ? "on the device" : "on this computer"}. Folders are merged and matching files replaced.`, input: false, ok: "Replace", danger: true });
    if (!answer) return;
    overwrite = true;
  }
  let queued = 0;
  for (const entry of entries) {
    const payload = direction === "upload"
      ? { direction, local_path: entry.path, remote_path: joinRemote(destBase, entry.name), is_directory: entry.is_dir, overwrite: existing.has(entry.name) && overwrite }
      : { direction, local_path: joinLocal(destBase, entry.name), remote_path: entry.path, is_directory: entry.is_dir, overwrite: existing.has(entry.name) && overwrite };
    try { await post(sessionURL(files.sessionID, "transfer"), payload); queued++; }
    catch (error) { toast(`${entry.name}: ${error.message}`, "error"); if (sessionLost(error)) { handleSessionLost(); break; } }
  }
  if (queued) toast(`${queued} ${direction}${queued === 1 ? "" : "s"} queued`);
  loadTransfers();
}
$("#btnDownload").addEventListener("click", () => transferSelection("download"));
$("#btnUpload").addEventListener("click", () => transferSelection("upload"));

function transferName(task) {
  const path = task.direction === "upload" ? task.local_path : task.remote_path;
  return path.split("/").filter(Boolean).pop() || path || "item";
}

function taskSignature(task) {
  const p = task.progress || {};
  return [task.state, Math.round((p.fraction || 0) * 1000), Math.round((p.speed_bytes_per_second || 0) / 1024), p.eta_seconds || 0, p.bytes_done || 0, task.error || "", task.warning || "", task.can_resume ? 1 : 0, files.fetched.has(task.id) ? 1 : 0].join("|");
}
function rowActions(...buttons) { return h("div", { class: "t-actions" }, ...buttons.filter(Boolean)); }
function taskRow(task) {
  const percent = Math.round(Math.max(0, Math.min(1, task.progress?.fraction || (task.state === "completed" ? 1 : 0))) * 100);
  const live = task.state === "running" || task.state === "queued";
  const done = task.progress?.bytes_done || 0, total = task.progress?.bytes_total || 0;
  const speed = Number(task.progress?.speed_bytes_per_second || 0);
  const toDevice = task.staged && task.direction === "download";
  const meta = task.state === "queued" ? "Waiting"
    : live ? [`${percent}%`, speed > 0 ? `${formatBytes(speed)}/s` : "", formatETA(task.progress?.eta_seconds)].filter(Boolean).join(" · ")
    : task.state === "completed" ? (toDevice ? (files.fetched.has(task.id) ? "Saved to this device" : "Ready to save") : `Done${done ? ` · ${formatBytes(done)}` : ""}`)
    : task.state === "cancelled" ? "Cancelled" : "Failed";
  const bar = h("div", { class: `bar${task.state === "queued" ? " indeterminate" : ""}` }, h("i"));
  bar.firstElementChild.style.width = `${percent}%`;
  const terminal = task.state === "failed" || task.state === "cancelled";
  const buttons = [];
  if (live) buttons.push(iconButton("x", "Cancel transfer", () => transferAction(task.id, "cancel")));
  if (toDevice && task.state === "completed" && !files.fetched.has(task.id)) buttons.push(iconButton("download", "Save to this device", () => saveStagedDownload(task)));
  if (terminal && task.can_resume) buttons.push(iconButton("play", "Resume from the partial file", () => transferAction(task.id, "resume")), iconButton("refresh", "Start over", () => transferAction(task.id, "restart")));
  if (terminal && !task.can_resume && !task.staged) buttons.push(iconButton("refresh", "Try again", () => retryTransfer(task)));
  const sizes = total ? `${formatBytes(done)} / ${formatBytes(total)}` : formatBytes(done);
  const where = task.staged ? task.remote_path : `${task.local_path} ↔ ${task.remote_path}`;
  const icons = task.direction === "upload" ? "upload" : "download";
  return h("div", { class: "transfer", dataset: { state: task.state, id: task.id } },
    h("span", { class: "t-dir" }, icon(icons, "sm")),
    h("div", { class: "t-main" },
      h("div", { class: "t-top" },
        h("span", { class: "t-name", title: `${where}\n${sizes}`, text: `${task.is_directory ? "📁 " : ""}${transferName(task)}` }),
        task.staged ? h("span", { class: "t-tag", text: "Your device" }) : null,
        h("span", { class: "t-meta", text: meta })),
      task.state === "completed" || terminal ? null : bar,
      task.error ? h("div", { class: "t-meta err", text: task.error }) : null,
      task.warning ? h("div", { class: "t-meta warn", text: task.warning }) : null),
    rowActions(...buttons));
}
async function saveStagedDownload(task) {
  files.fetched.add(task.id); files.autoDone.add(task.id);
  const link = h("a", { href: transferURL(task.id, "file"), download: transferName(task), hidden: true });
  document.body.append(link); link.click(); link.remove();
  window.setTimeout(loadTransfers, 1500);
}

const PENDING_PARALLEL = 2, SERVER_QUEUE_LIMIT = 40;
let pendingSeq = 0;
function pendingSignature(item) { return [item.state, Math.floor((item.loaded / (item.total || 1)) * 100), Math.round(item.speed / 1024), item.error].join("|"); }
function pendingRow(item) {
  const percent = item.total ? Math.min(100, Math.floor((item.loaded / item.total) * 100)) : 0;
  const meta = item.state === "failed" ? "Failed"
    : item.state === "waiting" ? "Waiting to send"
    : [item.total && item.loaded >= item.total ? "Handing over…" : `Sending ${percent}%`, item.speed > 0 ? `${formatBytes(item.speed)}/s` : ""].filter(Boolean).join(" · ");
  const bar = h("div", { class: `bar${item.state === "waiting" ? " indeterminate" : ""}` }, h("i"));
  bar.firstElementChild.style.width = `${percent}%`;
  const buttons = item.state === "failed"
    ? [iconButton("refresh", "Try again", () => { item.state = "waiting"; item.error = ""; pumpUploads(); renderQueue(); }), iconButton("x", "Remove", () => { files.pending = files.pending.filter((p) => p !== item); renderQueue(); })]
    : [iconButton("x", "Cancel", () => { item.xhr?.abort(); files.pending = files.pending.filter((p) => p !== item); renderQueue(); pumpUploads(); })];
  return h("div", { class: "transfer", dataset: { state: item.state === "failed" ? "failed" : "running", pending: item.key } },
    h("span", { class: "t-dir" }, icon("upload", "sm")),
    h("div", { class: "t-main" },
      h("div", { class: "t-top" },
        h("span", { class: "t-name", title: `${item.destPath}\n${formatBytes(item.total)}`, text: item.name }),
        h("span", { class: "t-tag", text: "Your device" }),
        h("span", { class: "t-meta", text: meta })),
      item.state === "failed" ? null : bar,
      item.error ? h("div", { class: "t-meta err", text: item.error }) : null),
    rowActions(...buttons));
}
function liveServerTasks() { return files.transfers.filter((t) => t.state === "running" || t.state === "queued").length; }
function pumpUploads() {
  let sending = files.pending.filter((p) => p.state === "sending").length;
  let room = SERVER_QUEUE_LIMIT - liveServerTasks();
  for (const item of files.pending) {
    if (sending >= PENDING_PARALLEL || room <= 0) break;
    if (item.state !== "waiting") continue;
    if (!files.sessionID) { item.state = "failed"; item.error = "Not connected to the device."; continue; }
    sendPending(item); sending++; room--;
  }
  startQueuePolling();
}
function sendPending(item) {
  item.state = "sending"; item.loaded = 0; item.speed = 0; item.error = "";
  const session = files.sessionID;
  let last = performance.now(), lastLoaded = 0;
  const xhr = new XMLHttpRequest();
  item.xhr = xhr;
  const query = `path=${encodeURIComponent(item.destPath)}${item.overwrite ? "&overwrite=1" : ""}&mtime=${Math.max(0, item.file.lastModified || 0)}`;
  xhr.open("POST", sessionURL(session, `upload?${query}`));
  xhr.setRequestHeader("Content-Type", "application/octet-stream");
  xhr.upload.onprogress = (event) => {
    const now = performance.now();
    item.loaded = event.loaded; item.total = event.lengthComputable ? event.total : item.total;
    if (now - last >= 400) { const rate = ((event.loaded - lastLoaded) / (now - last)) * 1000; item.speed = item.speed ? item.speed * 0.6 + rate * 0.4 : rate; last = now; lastLoaded = event.loaded; }
    scheduleQueueRender();
  };
  const fail = (message) => { item.state = "failed"; item.error = message; item.xhr = null; renderQueue(); pumpUploads(); };
  xhr.onload = () => {
    item.xhr = null;
    if (xhr.status === 202) { files.pending = files.pending.filter((p) => p !== item); loadTransfers(); pumpUploads(); return; }
    let data = {};
    try { data = JSON.parse(xhr.responseText); } catch { }
    if (authExpired(xhr.status, data)) return;
    if (xhr.status === 404 && /session not found/i.test(data.error || "")) { handleSessionLost(); }
    fail(data.error || `Upload failed (${xhr.status})`);
  };
  xhr.onerror = () => fail("The connection was interrupted. Press Try again.");
  xhr.onabort = () => { item.xhr = null; };
  xhr.send(item.file);
}
let renderTimer = 0;
function scheduleQueueRender() { if (!renderTimer) renderTimer = window.setTimeout(() => { renderTimer = 0; renderQueue(); }, 250); }

function chooseDeviceFiles(folder) {
  if (!requireSession()) return;
  const input = $(folder ? "#deviceFolderInput" : "#deviceFileInput");
  input.value = "";
  input.click();
}
async function uploadFromDevice(items, destPath = null) {
  if (!requireSession()) return;
  const dest = destPath || panes.remote.path;
  if (!dest || !dest.startsWith("/")) { toast("Open a folder on the device first.", "error"); return; }
  items = items.filter((it) => it.dir || it.file);
  if (!items.length) return;
  const fileItems = items.filter((it) => it.file);
  if (fileItems.length > 5000) { toast("That is more than 5000 files. Upload them in smaller batches.", "error"); return; }

  const tops = [...new Set(items.map((it) => (it.dir || it.rel).split("/")[0]))];
  let existing = new Set();
  try { const listing = await api(sessionURL(files.sessionID, `list?path=${encodeURIComponent(dest)}`)); existing = new Set((listing.entries || []).map((e) => e.name)); }
  catch (error) { handleOpError(error, "remote"); return; }
  const clashes = tops.filter((name) => existing.has(name));
  let overwrite = false;
  if (clashes.length) {
    const list = clashes.slice(0, 5).map((n) => `• ${n}`).join("\n") + (clashes.length > 5 ? `\n… and ${clashes.length - 5} more` : "");
    const answer = await ask({ title: "Replace existing items?", message: `${list}\n\nalready exist${clashes.length === 1 ? "s" : ""} on the device. Folders are merged and matching files replaced.`, input: false, ok: "Replace", danger: true });
    if (!answer) return;
    overwrite = true;
  }

  const dirs = new Set();
  for (const it of items) {
    const parts = (it.dir ? it.dir : it.rel.split("/").slice(0, -1).join("/")).split("/").filter(Boolean);
    for (let i = 1; i <= parts.length; i++) dirs.add(parts.slice(0, i).join("/"));
  }
  for (const rel of [...dirs].sort((a, b) => a.split("/").length - b.split("/").length || a.localeCompare(b))) {
    try { await post(sessionURL(files.sessionID, "mkdir"), { path: joinRemote(dest, rel) }); }
    catch (error) { if (sessionLost(error)) { handleSessionLost(); return; } }
  }
  for (const it of fileItems) {
    files.pending.push({ key: `p${++pendingSeq}`, file: it.file, name: it.rel, destPath: joinRemote(dest, it.rel), overwrite, state: "waiting", loaded: 0, total: it.file.size, speed: 0, error: "", xhr: null });
  }
  if (fileItems.length) toast(`${fileItems.length} file${fileItems.length === 1 ? "" : "s"} queued for upload`);
  else toast("Folders created");
  pumpUploads(); renderQueue();
  if (!fileItems.length) refreshPane("remote");
}
$("#deviceFileInput").addEventListener("change", (event) => uploadFromDevice([...event.target.files].map((file) => ({ file, rel: file.name }))));
$("#deviceFolderInput").addEventListener("change", (event) => uploadFromDevice([...event.target.files].map((file) => ({ file, rel: file.webkitRelativePath || file.name }))));
$("#deviceUpload").addEventListener("click", () => chooseDeviceFiles(false));
$("#deviceUploadFolder").addEventListener("click", () => chooseDeviceFiles(true));
$("#deviceDownload").addEventListener("click", () => downloadToDevice(selectedEntries(panes.remote)));

async function downloadToDevice(entries) {
  if (!requireSession()) return;
  const wanted = (entries || []).filter((e) => !e.is_dir);
  if (!wanted.length) { toast((entries || []).length ? "Folders cannot be saved to this device yet. Select files." : "Select remote files to download"); return; }
  if (wanted.length < entries.length) toast("Folders were skipped. Only files can be saved to this device.");
  let queued = 0;
  for (const entry of wanted) {
    try { await post(sessionURL(files.sessionID, "download-prepare"), { remote_path: entry.path }); queued++; }
    catch (error) { toast(`${entry.name}: ${error.message}`, "error"); if (sessionLost(error)) { handleSessionLost(); break; } }
  }
  if (queued) toast(`${queued} file${queued === 1 ? "" : "s"} will be saved to this device when ready`);
  loadTransfers();
}

const hasOSFiles = (event) => Array.from(event.dataTransfer?.types || []).includes("Files");
async function droppedItems(dataTransfer) {
  const entries = Array.from(dataTransfer.items || []).filter((i) => i.kind === "file").map((i) => i.webkitGetAsEntry?.()).filter(Boolean);
  if (!entries.length) return Array.from(dataTransfer.files || []).map((file) => ({ file, rel: file.name }));
  const out = [];
  const walk = async (entry, prefix) => {
    if (entry.isFile) { const file = await new Promise((resolve, reject) => entry.file(resolve, reject)); out.push({ file, rel: prefix + entry.name }); }
    else if (entry.isDirectory) {
      out.push({ dir: prefix + entry.name });
      const reader = entry.createReader();
      for (;;) {
        const batch = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
        if (!batch.length) break;
        for (const child of batch) await walk(child, `${prefix}${entry.name}/`);
      }
    }
  };
  for (const entry of entries) await walk(entry, "");
  return out;
}
{
  const target = panes.remote.root;
  target.addEventListener("dragover", (event) => {
    if (files.drag || !hasOSFiles(event) || !files.sessionID) return;
    event.preventDefault(); event.dataTransfer.dropEffect = "copy";
    const folder = dropFolder(panes.remote, event);
    markDropRow(panes.remote, folder?.row || null);
    target.classList.toggle("drop-os", !folder);
  });
  target.addEventListener("dragleave", (event) => { if (!target.contains(event.relatedTarget)) { target.classList.remove("drop-os"); markDropRow(panes.remote, null); } });
  target.addEventListener("drop", async (event) => {
    if (files.drag || !hasOSFiles(event)) return;
    event.preventDefault(); target.classList.remove("drop-os");
    const folder = dropFolder(panes.remote, event); markDropRow(panes.remote, null);
    if (!files.sessionID) { toast("Connect to the device first"); return; }
    try { await uploadFromDevice(await droppedItems(event.dataTransfer), folder ? folder.path : null); } catch (error) { toastError(error); }
  });
  for (const type of ["dragover", "drop"]) window.addEventListener(type, (event) => { if (hasOSFiles(event) && !target.contains(event.target)) event.preventDefault(); });
}

async function transferAction(id, action) {
  try {
    if (action === "cancel") await api(transferURL(id), { method: "DELETE" });
    else await post(transferURL(id, `${action}`));
    loadTransfers();
  } catch (error) { toastError(error); }
}
async function retryTransfer(task) {
  if (!files.sessionID) { toast("Connect to the device first"); return; }
  try {
    await post(sessionURL(files.sessionID, "transfer"), { direction: task.direction, local_path: task.local_path, remote_path: task.remote_path, is_directory: task.is_directory, overwrite: true });
    loadTransfers();
  } catch (error) { toastError(error); }
}

const queueRows = new Map();
function syncQueueList(list, wanted) {
  const scroll = list.scrollTop;
  const keep = new Set(wanted.map((w) => w.key));
  for (const [key, row] of queueRows) if (!keep.has(key)) { row.element.remove(); queueRows.delete(key); }
  wanted.forEach((w, index) => {
    let row = queueRows.get(w.key);
    if (!row || row.signature !== w.signature) {
      const element = w.build();
      if (row) row.element.replaceWith(element);
      row = { signature: w.signature, element };
      queueRows.set(w.key, row);
    }
    if (list.children[index] !== row.element) list.insertBefore(row.element, list.children[index] || null);
  });
  list.scrollTop = scroll;
}
function renderQueue() {
  const list = $("#transferList");
  const serverLive = (t) => t.state === "running" || t.state === "queued";
  const pendingLive = files.pending.filter((p) => p.state !== "failed").length;
  const active = files.transfers.filter(serverLive).length + pendingLive;
  const failed = files.transfers.filter((t) => t.state === "failed").length + files.pending.filter((p) => p.state === "failed").length;
  const total = files.transfers.length + files.pending.length;
  $("#transferSummary").textContent = total ? `${active} active · ${total} total${failed ? ` · ${failed} failed` : ""}` : "No transfers yet";
  $("#transferClear").disabled = !(files.transfers.some((t) => !serverLive(t)) || files.pending.some((p) => p.state === "failed"));
  $("#queuePanel").classList.toggle("empty", !total);
  const recent = files.transfers.slice(-60).reverse();
  const wanted = [
    ...files.pending.map((p) => ({ key: `p:${p.key}`, signature: pendingSignature(p), build: () => pendingRow(p) })),
    ...[...recent.filter(serverLive), ...recent.filter((t) => !serverLive(t))].map((t) => ({ key: `t:${t.id}`, signature: taskSignature(t), build: () => taskRow(t) })),
  ];
  syncQueueList(list, wanted);
  return active;
}
function startQueuePolling() { if (!files.queueTimer) files.queueTimer = window.setInterval(loadTransfers, 900); }
async function loadTransfers() {
  if (files.queueBusy) return;
  files.queueBusy = true;
  try {
    const { transfers } = await api("/api/v1/files/transfers");
    files.transfers = transfers || [];
    const active = renderQueue();
    if (active) startQueuePolling();
    else if (files.queueTimer) { window.clearInterval(files.queueTimer); files.queueTimer = 0; }
    pumpUploadsIfWaiting();
    const finished = files.transfers.filter((t) => t.state === "completed" && !files.seenDone.has(t.id));
    finished.forEach((t) => files.seenDone.add(t.id));
    if (finished.length && view.current === "files") refreshBoth();
    for (const t of files.transfers) {
      if (!(t.staged && t.direction === "download" && t.state === "completed") || files.autoDone.has(t.id)) continue;
      files.autoDone.add(t.id);
      if (files.transfersSeen) saveStagedDownload(t);
    }
    files.transfersSeen = true;
  } catch { }
  finally { files.queueBusy = false; }
}
function pumpUploadsIfWaiting() { if (files.pending.some((p) => p.state === "waiting")) pumpUploads(); }

{
  const panel = $("#queuePanel"), handle = $("#queueResize");
  const apply = (px) => { panel.classList.toggle("sized", Boolean(px)); if (px) panel.style.setProperty("--queue-h", `${Math.round(px)}px`); else panel.style.removeProperty("--queue-h"); store.set("queue.height", px ? Math.round(px) : 0); };
  const saved = Number(store.get("queue.height", 0));
  if (saved >= 96) apply(saved);
  const limit = (px) => Math.max(96, Math.min(window.innerHeight * 0.72, px));
  handle.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    const startY = event.clientY, startH = panel.getBoundingClientRect().height;
    panel.classList.add("dragging-size");
    handle.setPointerCapture(event.pointerId);
    const move = (e) => apply(limit(startH + (startY - e.clientY)));
    const stop = () => { panel.classList.remove("dragging-size"); handle.removeEventListener("pointermove", move); handle.removeEventListener("pointerup", stop); handle.removeEventListener("pointercancel", stop); };
    handle.addEventListener("pointermove", move); handle.addEventListener("pointerup", stop); handle.addEventListener("pointercancel", stop);
  });
  handle.addEventListener("dblclick", () => apply(0));
  handle.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
    event.preventDefault();
    apply(limit(panel.getBoundingClientRect().height + (event.key === "ArrowUp" ? 24 : -24)));
  });
}
function setQueueCollapsed(collapsed) {
  $("#queuePanel").classList.toggle("collapsed", collapsed);
  const toggle = $("#transferToggle");
  toggle.setAttribute("aria-expanded", String(!collapsed));
  toggle.title = collapsed ? "Show the list" : "Hide the list";
  toggle.setAttribute("aria-label", toggle.title);
  toggle.replaceChildren(icon(collapsed ? "chev-up" : "chev-down"));
  store.set("queue.collapsed", collapsed ? "1" : "");
}
$("#transferToggle").addEventListener("click", () => setQueueCollapsed(!$("#queuePanel").classList.contains("collapsed")));
setQueueCollapsed(Boolean(store.get("queue.collapsed", "")));
$("#transferClear").addEventListener("click", async () => {
  files.pending = files.pending.filter((p) => p.state !== "failed");
  try { await api("/api/v1/files/transfers", { method: "DELETE" }); loadTransfers(); } catch (error) { toastError(error); }
});

function openTerminalHere(directory, { split = "" } = {}) {
  if (!requireSession()) return;
  const device = currentDevice();
  if (!device) return;
  const host = split ? activeTab() : null;
  createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), directory: directory || panes.remote.path, split: host ? { of: host.id, dir: split } : null });
  showView("terminal");
}
function defaultRunCommand(entry) {
  const quoted = shellQuote(entry.path), name = entry.name.toLowerCase();
  if (name.endsWith(".sh")) return `sh ${quoted}`;
  if (name.endsWith(".ipk")) return `opkg install ${quoted}`;
  if (name.endsWith(".py")) return `python ${quoted}`;
  if (name.endsWith(".tar.gz") || name.endsWith(".tgz")) return `tar -xzf ${quoted} -C ${shellQuote(panes.remote.path)}`;
  if (name.endsWith(".tar.bz2")) return `tar -xjf ${quoted} -C ${shellQuote(panes.remote.path)}`;
  if (name.endsWith(".tar.xz")) return `tar -xJf ${quoted} -C ${shellQuote(panes.remote.path)}`;
  return quoted;
}
async function runRemoteFile() {
  const sel = selectedEntries(panes.remote);
  if (!requireSession() || sel.length !== 1 || sel[0].is_dir) { toast("Select one remote file to run"); return; }
  const device = currentDevice();
  const command = await ask({ title: `Run ${sel[0].name}`, message: `The command runs on ${device.name} with the saved account. Review it first.`, label: "Command", value: defaultRunCommand(sel[0]), ok: "Run in terminal", validate: (v) => (!v ? "Enter a command." : /[\r\n\0]/.test(v) ? "Use a single line." : "") });
  if (!command) return;
  createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), directory: panes.remote.path, initialCommand: command });
  showView("terminal");
}
$("#fileTerminalHere").addEventListener("click", () => openTerminalHere());
$("#fileRun").addEventListener("click", runRemoteFile);

document.addEventListener("keydown", (event) => {
  if (view.current !== "files" || $("dialog[open]")) return;
  const tag = event.target.tagName;
  const typing = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
  const pane = panes[files.active], key = event.key;
  const fkeys = { F2: cmdRename, F5: () => transferSelection(files.active === "remote" ? "download" : "upload"), F7: cmdMkdir, F8: cmdDelete, F9: cmdChmod };
  if (key === "F4" && !event.ctrlKey && !event.altKey) { event.preventDefault(); if (event.shiftKey) cmdNewFile(); else cmdEdit(); return; }
  if (fkeys[key] && !event.ctrlKey && !event.altKey) { event.preventDefault(); fkeys[key](); return; }
  if (typing) return;
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "a") { event.preventDefault(); cmdSelectAll(); return; }
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "r") { event.preventDefault(); refreshPane(files.active); return; }
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  const count = pane.visible.length;
  const move = (to, extend) => {
    if (!count) return;
    to = Math.max(0, Math.min(count - 1, to));
    if (extend) {
      if (pane.anchor < 0) pane.anchor = pane.cursor < 0 ? to : pane.cursor;
      pane.selected = new Set(pane.visible.slice(Math.min(pane.anchor, to), Math.max(pane.anchor, to) + 1).map((e) => e.path));
    } else { pane.selected = new Set([pane.visible[to].path]); pane.anchor = to; }
    pane.cursor = to;
    updateSelectionUI(pane);
    $(`tr[data-index="${to}"]`, pane.rows)?.scrollIntoView({ block: "nearest" });
  };
  if (key === "ArrowDown") { event.preventDefault(); move(pane.cursor + 1, event.shiftKey); }
  else if (key === "ArrowUp") { event.preventDefault(); move(pane.cursor < 0 ? 0 : pane.cursor - 1, event.shiftKey); }
  else if (key === "Home") { event.preventDefault(); move(0, event.shiftKey); }
  else if (key === "End") { event.preventDefault(); move(count - 1, event.shiftKey); }
  else if (key === "PageDown") { event.preventDefault(); move(pane.cursor + 10, event.shiftKey); }
  else if (key === "PageUp") { event.preventDefault(); move(pane.cursor - 10, event.shiftKey); }
  else if (key === "Enter") { event.preventDefault(); const entry = pane.visible[pane.cursor]; if (entry) openEntry(pane, entry); }
  else if (key === "Backspace") { event.preventDefault(); goUp(files.active); }
  else if (key === "Delete") { event.preventDefault(); cmdDelete(); }
  else if (key === " ") { event.preventDefault(); const entry = pane.visible[pane.cursor]; if (entry) { if (pane.selected.has(entry.path)) pane.selected.delete(entry.path); else pane.selected.add(entry.path); updateSelectionUI(pane); } }
  else if (key === "Escape") { pane.selected.clear(); updateSelectionUI(pane); }
});
