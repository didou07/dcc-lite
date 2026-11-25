"use strict";
async function transferSelection(direction, given = null, destPath = null, expected = null) {
  if (!requireSession()) return;
  const sessionID = expected ? expected.sessionID : files.sessionID;
  const sessionKey = expected ? expected.sessionKey : files.sessionKey;
  const profileID = expected ? expected.profileID : files.profileID;
  if (!isFileSessionCurrent(sessionID, sessionKey) || files.profileID !== profileID) { toast("The receiver connection changed. Transfer cancelled.", "error"); return []; }
  if (files.browserMode) {
    if (direction === "download") return downloadToDevice(given || selectedEntries(panes.remote), expected);
    return chooseDeviceFiles(false);
  }
  const source = direction === "upload" ? panes.local : panes.remote;
  let entries = given || selectedEntries(source);
  if (!entries.length) { toast(direction === "upload" ? "Select local items to upload" : "Select remote items to download"); return; }
  const destPane = direction === "upload" ? panes.remote : panes.local;
  const destBase = destPath || destPane.path;
  let existing, existingMap;
  try {
    const listing = direction === "upload"
      ? await api(sessionURL(sessionID, `list?path=${encodeURIComponent(destBase)}`))
      : await api(`/api/v1/files/local/list?path=${encodeURIComponent(destBase)}`);
    if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Transfer cancelled.", "error"); return []; }
    existingMap = new Map((listing.entries || []).map((e) => [e.name, e]));
    existing = new Set(existingMap.keys());
  } catch (error) { if (sessionLost(error)) handleSessionLost(sessionID); else handleOpError(error, direction === "upload" ? "remote" : "local", sessionID); return []; }
  const clashes = entries.filter((e) => existing.has(e.name));
  let overwrite = false;
  if (clashes.length) {
    const list = clashes.slice(0, 5).map((e) => `• ${e.name}`).join("\n") + (clashes.length > 5 ? `\n… and ${clashes.length - 5} more` : "");
    const answer = await ask({ title: "Replace existing items?", message: `${list}\n\nalready exist${clashes.length === 1 ? "s" : ""} ${direction === "upload" ? "on the device" : "on this computer"}.`, input: false, ok: clashes.length === 1 ? "Replace" : "Replace all", alt: "Skip existing", alt2: "Only if newer", danger: true });
    if (!answer) return;
    if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Transfer cancelled.", "error"); return []; }
    if (answer === "alt") {
      entries = entries.filter((e) => !existing.has(e.name));
      if (!entries.length) { toast("Nothing to transfer: every item already exists"); return; }
    } else if (answer === "alt2") {
      const time = (e) => Date.parse(e?.modified_at || "") || 0;
      entries = entries.filter((e) => !existing.has(e.name) || time(e) > time(existingMap.get(e.name)));
      if (!entries.length) { toast("Nothing to transfer: the existing items are already up to date"); return; }
      overwrite = true;
    } else overwrite = true;
  }
  let queued = 0;
  const queuedTasks = [];
  for (const entry of entries) {
    if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Remaining transfers were not queued.", "error"); break; }
    const payload = direction === "upload"
      ? { direction, local_path: entry.path, remote_path: joinRemote(destBase, entry.name), is_directory: entry.is_dir, overwrite: existing.has(entry.name) && overwrite }
      : { direction, local_path: joinLocal(destBase, entry.name), remote_path: entry.path, is_directory: entry.is_dir, overwrite: existing.has(entry.name) && overwrite };
    try {
      const result = await post(sessionURL(sessionID, "transfer"), payload);
      queued++; if (result?.task?.id) queuedTasks.push({ id: result.task.id, entry });
    } catch (error) { toast(`${entry.name}: ${error.message}`, "error"); if (sessionLost(error)) { handleSessionLost(sessionID); break; } }
  }
  if (queued) toast(`${queued} ${direction}${queued === 1 ? "" : "s"} queued`);
  loadTransfers();
  return queuedTasks;
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
  const canRetry = isFileSessionCurrent(item.sessionID, item.sessionKey);
  const buttons = item.state === "failed"
    ? [canRetry ? iconButton("refresh", "Try again", () => { item.state = "waiting"; item.error = ""; pumpUploads(); renderQueue(); }) : null, iconButton("x", "Remove", () => { files.pending = files.pending.filter((p) => p !== item); renderQueue(); })]
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
    if (!isFileSessionCurrent(item.sessionID, item.sessionKey)) { item.state = "failed"; item.error = "The receiver connection changed before this upload started. Queue it again from the original device."; continue; }
    sendPending(item); sending++; room--;
  }
  startQueuePolling();
}
function sendPending(item) {
  if (!isFileSessionCurrent(item.sessionID, item.sessionKey)) { item.state = "failed"; item.error = "The receiver connection changed before this upload started."; renderQueue(); return; }
  item.state = "sending"; item.loaded = 0; item.speed = 0; item.error = "";
  const session = item.sessionID;
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
    if (xhr.status === 404 && /session not found/i.test(data.error || "")) { handleSessionLost(session); }
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
  files.devicePicker = { sessionID: files.sessionID, sessionKey: files.sessionKey, profileID: files.profileID, destPath: panes.remote.path };
  const input = $(folder ? "#deviceFolderInput" : "#deviceFileInput");
  input.value = "";
  input.click();
}
async function uploadFromDevice(items, destPath = null, expected = null) {
  if (!requireSession()) return;
  const sessionID = expected ? expected.sessionID : files.sessionID;
  const sessionKey = expected ? expected.sessionKey : files.sessionKey;
  const profileID = expected ? expected.profileID : files.profileID;
  const dest = destPath ?? expected?.destPath ?? panes.remote.path;
  if (!isFileSessionCurrent(sessionID, sessionKey) || files.profileID !== profileID) { toast("The receiver connection changed. Upload cancelled.", "error"); return; }
  if (typeof dest !== "string" || !dest.trim() || /[\x00\r\n]/.test(dest)) { toast("Open a valid folder on the device first.", "error"); return; }
  items = items.filter((it) => it.dir || it.file);
  if (!items.length) return;
  if (items.length > 100000) { toast("That folder contains too many items to upload in one batch.", "error"); return; }
  const filePaths = new Set();
  const directoryPaths = new Set();
  for (const item of items) {
    const isDirectory = Boolean(item.dir);
    const raw = String(isDirectory ? item.dir : item.rel || "").replace(/\\/g, "/");
    const relative = isDirectory ? raw.replace(/\/+$/, "") : raw;
    const parts = relative.split("/");
    if (!relative || relative.length > 4096 || relative.startsWith("/") || /^[A-Za-z]:(?:\/|$)/.test(relative) || /[\x00-\x1f\x7f]/.test(relative) || parts.some((part) => !part || part === "." || part === "..")) {
      toast(`Upload cancelled: an item has an unsafe relative path (${relative || "empty"}).`, "error"); return;
    }
    if (isDirectory) { item.dir = relative; directoryPaths.add(relative); }
    else {
      item.rel = relative;
      if (filePaths.has(relative)) { toast(`Upload cancelled: duplicate file path ${relative}.`, "error"); return; }
      filePaths.add(relative);
    }
  }
  const requiredDirectories = new Set(directoryPaths);
  for (const filePath of filePaths) {
    const parts = filePath.split("/");
    for (let i = 1; i < parts.length; i++) requiredDirectories.add(parts.slice(0, i).join("/"));
  }
  if ([...filePaths].some((filePath) => [...requiredDirectories].some((dirPath) => dirPath === filePath || dirPath.startsWith(`${filePath}/`)))) {
    toast("Upload cancelled: a file path conflicts with a folder path.", "error"); return;
  }
  let fileItems = items.filter((it) => it.file);
  if (fileItems.length > 5000) { toast("That is more than 5000 files. Upload them in smaller batches.", "error"); return; }

  const tops = [...new Set(items.map((it) => (it.dir || it.rel).split("/")[0]))];
  let existing = new Set();
  try { const listing = await api(sessionURL(sessionID, `list?path=${encodeURIComponent(dest)}`)); existing = new Set((listing.entries || []).map((e) => e.name)); }
  catch (error) { if (sessionLost(error)) handleSessionLost(sessionID); else handleOpError(error, "remote", sessionID); return; }
  if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Upload cancelled.", "error"); return; }
  const clashes = tops.filter((name) => existing.has(name));
  let overwrite = false;
  if (clashes.length) {
    const list = clashes.slice(0, 5).map((n) => `• ${n}`).join("\n") + (clashes.length > 5 ? `\n… and ${clashes.length - 5} more` : "");
    const answer = await ask({ title: "Replace existing items?", message: `${list}\n\nalready exist${clashes.length === 1 ? "s" : ""} on the device.`, input: false, ok: clashes.length === 1 ? "Replace" : "Replace all", alt: "Skip existing", danger: true });
    if (!answer) return;
    if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Upload cancelled.", "error"); return; }
    if (answer === "alt") {
      const skip = new Set(clashes);
      items = items.filter((it) => !skip.has((it.dir || it.rel).split("/")[0]));
      fileItems = items.filter((it) => it.file);
      if (!items.length) { toast("Nothing to upload: every item already exists"); return; }
    } else overwrite = true;
  }

  const dirs = new Set();
  for (const item of items) {
    const parts = (item.dir ? item.dir : item.rel.split("/").slice(0, -1).join("/")).split("/").filter(Boolean);
    for (let i = 1; i <= parts.length; i++) dirs.add(parts.slice(0, i).join("/"));
  }
  let createdDirs = 0;
  for (const rel of [...dirs].sort((a, b) => a.split("/").length - b.split("/").length || a.localeCompare(b))) {
    if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Remaining folders were not created.", "error"); return; }
    const remoteDir = joinRemote(dest, rel);
    try { await post(sessionURL(sessionID, "mkdir"), { path: remoteDir }); createdDirs++; }
    catch (error) {
      if (sessionLost(error)) { handleSessionLost(sessionID); return; }
      let alreadyDirectory = false;
      try {
        const parent = RemoteParentPath(remoteDir);
        const listing = await api(sessionURL(sessionID, `list?path=${encodeURIComponent(parent)}`));
        alreadyDirectory = (listing.entries || []).some((entry) => entry.name === baseOf(remoteDir) && entry.is_dir);
      } catch (checkError) {
        if (sessionLost(checkError)) handleSessionLost(sessionID);
      }
      if (!alreadyDirectory) {
        if (createdDirs) toast(`${createdDirs} folder(s) created, but ${rel} could not be created. No files were queued.`, "error");
        else toast(`Could not create folder ${rel}: ${error.message}. No files were queued.`, "error");
        if (isFileSessionCurrent(sessionID, sessionKey)) refreshPane("remote");
        return;
      }
    }
  }
  for (const it of fileItems) {
    files.pending.push({ key: `p${++pendingSeq}`, file: it.file, name: it.rel, destPath: joinRemote(dest, it.rel), overwrite, sessionID, sessionKey, profileID, state: "waiting", loaded: 0, total: it.file.size, speed: 0, error: "", xhr: null });
  }
  if (fileItems.length) toast(`${fileItems.length} file${fileItems.length === 1 ? "" : "s"} queued for upload`);
  else toast("Folders created");
  pumpUploads(); renderQueue();
  if (!fileItems.length && isFileSessionCurrent(sessionID, sessionKey)) refreshPane("remote");
}
$("#deviceFileInput").addEventListener("change", (event) => { const target = files.devicePicker; files.devicePicker = null; uploadFromDevice([...event.target.files].map((file) => ({ file, rel: file.name })), null, target); });
$("#deviceFolderInput").addEventListener("change", (event) => { const target = files.devicePicker; files.devicePicker = null; uploadFromDevice([...event.target.files].map((file) => ({ file, rel: file.webkitRelativePath || file.name })), null, target); });
$("#deviceUpload").addEventListener("click", () => chooseDeviceFiles(false));
$("#deviceUploadFolder").addEventListener("click", () => chooseDeviceFiles(true));
$("#deviceDownload").addEventListener("click", () => downloadToDevice(selectedEntries(panes.remote)));

async function downloadToDevice(entries, expected = null) {
  if (!requireSession()) return;
  const sessionID = expected ? expected.sessionID : files.sessionID;
  const sessionKey = expected ? expected.sessionKey : files.sessionKey;
  const profileID = expected ? expected.profileID : files.profileID;
  if (!isFileSessionCurrent(sessionID, sessionKey) || files.profileID !== profileID) { toast("The receiver connection changed. Download cancelled.", "error"); return; }
  const wanted = (entries || []).filter((e) => !e.is_dir);
  if (!wanted.length) { toast((entries || []).length ? "Folders cannot be saved here yet." : "Select remote files to download"); return; }
  if (wanted.length < entries.length) toast("Folders were skipped.");
  let queued = 0;
  for (const entry of wanted) {
    if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Remaining downloads were not queued.", "error"); break; }
    try { await post(sessionURL(sessionID, "download-prepare"), { remote_path: entry.path }); queued++; }
    catch (error) { toast(`${entry.name}: ${error.message}`, "error"); if (sessionLost(error)) { handleSessionLost(sessionID); break; } }
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
    event.preventDefault();
    const folder = dropFolder(panes.remote, event); clearFileDropState();
    if (!files.sessionID) { toast("Connect to the device first"); return; }
    const expected = { sessionID: files.sessionID, sessionKey: files.sessionKey, profileID: files.profileID, destPath: folder ? folder.path : panes.remote.path };
    try { await uploadFromDevice(await droppedItems(event.dataTransfer), expected.destPath, expected); } catch (error) { toastError(error); }
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
  return transferAction(task.id, "restart");
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
function syncTransferBadge(active) {
  const badge = $("#xferBadge");
  if (!badge) return;
  badge.hidden = !active;
  if (!active) return;
  let done = 0, total = 0;
  for (const t of files.transfers) {
    if (t.state !== "running" && t.state !== "queued") continue;
    done += t.progress?.bytes_done || 0; total += t.progress?.bytes_total || 0;
  }
  for (const p of files.pending) {
    if (p.state === "failed") continue;
    done += Math.min(p.loaded || 0, p.total || 0); total += p.total || 0;
  }
  const known = total > 0;
  const percent = known ? Math.max(0, Math.min(100, Math.floor((done / total) * 100))) : 0;
  $("#xferBadgeText").textContent = known ? `${percent}%${active > 1 ? ` · ${active}` : ""}` : String(active);
  badge.classList.toggle("indeterminate", !known);
  badge.querySelector(".xfer-bar b").style.width = `${percent}%`;
  badge.title = `${active} transfer${active === 1 ? "" : "s"} in progress${known ? ` · ${percent}%` : ""}`;
  badge.setAttribute("aria-label", badge.title);
}
$("#xferBadge")?.addEventListener("click", () => showView("files"));

function renderQueue() {
  const list = $("#transferList");
  const serverLive = (t) => t.state === "running" || t.state === "queued";
  const pendingLive = files.pending.filter((p) => p.state !== "failed").length;
  const active = files.transfers.filter(serverLive).length + pendingLive;
  const failed = files.transfers.filter((t) => t.state === "failed").length + files.pending.filter((p) => p.state === "failed").length;
  const total = files.transfers.length + files.pending.length;
  $("#transferSummary").textContent = total ? `${active} active · ${total} total${failed ? ` · ${failed} failed` : ""}` : "No transfers yet";
  syncTransferBadge(active);
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
  const sessionID = files.sessionID, sessionKey = files.sessionKey;
  const device = currentDevice();
  const command = await ask({ title: `Run ${sel[0].name}`, message: `Runs on ${device.name}. Review it first.`, label: "Command", value: defaultRunCommand(sel[0]), ok: "Run in terminal", validate: (v) => (!v ? "Enter a command." : /[\r\n\0]/.test(v) ? "Use a single line." : "") });
  if (!command) return;
  if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Command cancelled.", "error"); return; }
  createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), directory: panes.remote.path, initialCommand: command });
  showView("terminal");
}
function runOnDevice(command, directory, device = currentDevice()) {
  if (!device) return;
  createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), directory: directory || panes.remote.path, initialCommand: command });
  showView("terminal");
}
const remoteAbsolute = (v) => (!v ? "Enter a path." : !v.startsWith("/") ? "Use a full path that starts with /." : /[\r\n\0]/.test(v) ? "Use a single line." : "");
async function cmdSymlink() {
  if (!requireSession()) return;
  const sessionID = files.sessionID, sessionKey = files.sessionKey;
  const device = currentDevice();
  const directory = panes.remote.path;
  const sel = selectedEntries(panes.remote);
  let target = sel.length === 1 ? sel[0].path : "";
  if (!target) {
    target = await ask({ title: "New symbolic link", message: "What should the link point to?", label: "Target path on the device", value: panes.remote.path, ok: "Next", validate: remoteAbsolute });
    if (!target) return;
  }
  const base = target.replace(/\/+$/, "").split("/").pop() || "link";
  const linkPath = await ask({ title: "New symbolic link", message: `Target: ${target}`, label: "Link path (the new file)", value: joinRemote(directory, `${base}-link`), ok: "Create in terminal", validate: remoteAbsolute });
  if (!linkPath) return;
  if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Link creation cancelled.", "error"); return; }
  runOnDevice(`ln -s ${shellQuote(target)} ${shellQuote(linkPath)} && ls -l ${shellQuote(linkPath)}`, directory, device);
  toast("Created in the terminal.");
}
const copyName = (name) => { const dot = name.lastIndexOf("."); return dot > 0 ? `${name.slice(0, dot)}-copy${name.slice(dot)}` : `${name}-copy`; };
async function cmdCopyOnDevice() {
  if (!requireSession()) return;
  const sessionID = files.sessionID, sessionKey = files.sessionKey;
  const device = currentDevice();
  const directory = panes.remote.path;
  const sel = selectedEntries(panes.remote);
  if (!sel.length) { toast("Select the items to copy on the device"); return; }
  const dest = await ask({ title: sel.length === 1 ? `Copy ${sel[0].name}` : `Copy ${sel.length} items`, message: "Copied on the device itself.", label: "Destination folder on the device", value: panes.remote.path, ok: "Copy in terminal", validate: remoteAbsolute });
  if (!dest) return;
  if (!isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Copy cancelled.", "error"); return; }
  const clean = dest.replace(/\/+$/, "") || "/";
  if (sel.length > 1 || sel[0].is_dir || clean !== panes.remote.path) {
    runOnDevice(`cp -a ${sel.map((e) => shellQuote(e.path)).join(" ")} ${shellQuote(clean + "/")} && echo Done`, directory, device);
  } else {
    runOnDevice(`cp -a ${shellQuote(sel[0].path)} ${shellQuote(joinRemote(clean, copyName(sel[0].name)))} && echo Done`, directory, device);
  }
  toast("The copy runs in the terminal.");
}
$("#fileTerminalHere").addEventListener("click", () => openTerminalHere());
$("#fileRun").addEventListener("click", runRemoteFile);

document.addEventListener("keydown", (event) => {
  if (view.current !== "files" || $("dialog[open]") || event.target.classList?.contains("rename-input")) return;
  const tag = event.target.tagName;
  const typing = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
  const pane = panes[files.active], key = event.key;
  if (pane.renameTimer) cancelPendingRename(pane);
  const fkeys = { F2: cmdRename, F3: cmdPreview, F5: () => transferSelection(files.active === "remote" ? "download" : "upload"), F7: cmdMkdir, F8: cmdDelete, F9: cmdChmod };
  if (key === "F4" && !event.ctrlKey && !event.altKey) { event.preventDefault(); if (event.shiftKey) cmdNewFile(); else cmdEdit(); return; }
  if (fkeys[key] && !event.ctrlKey && !event.altKey) { event.preventDefault(); fkeys[key](); return; }
  if (typing) return;
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "a") { event.preventDefault(); if (event.shiftKey) cmdSelectNone(); else cmdSelectAll(); return; }
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "r") { event.preventDefault(); refreshPane(files.active); return; }
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "c") { event.preventDefault(); cmdCopy(); return; }
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "x") { event.preventDefault(); cmdCut(); return; }
  if ((event.ctrlKey || event.metaKey) && key.toLowerCase() === "v") { event.preventDefault(); cmdPaste(); return; }
  if ((event.ctrlKey || event.metaKey) && !event.shiftKey && key.toLowerCase() === "z") { event.preventDefault(); cmdUndo(); return; }
  const toggleAt = () => { const entry = pane.visible[pane.cursor]; if (!entry) return; cancelPendingRename(pane); if (pane.selected.has(entry.path)) pane.selected.delete(entry.path); else pane.selected.add(entry.path); pane.anchor = pane.cursor; updateSelectionUI(pane); };
  if ((event.ctrlKey || event.metaKey) && key === " ") { event.preventDefault(); toggleAt(); return; }
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  const count = pane.visible.length;
  const move = (to, extend) => {
    if (!count) return;
    cancelPendingRename(pane);
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
  else if (key === " ") { event.preventDefault(); if (pane.selected.size > 1) toggleAt(); else cmdPreview(); }
  else if (key === "Insert") {
    event.preventDefault();
    if (pane.cursor < 0 && count) pane.cursor = 0;
    toggleAt();
    if (pane.cursor < count - 1) { pane.cursor++; updateSelectionUI(pane); $(`tr[data-index="${pane.cursor}"]`, pane.rows)?.scrollIntoView({ block: "nearest" }); }
  }
  else if (key === "Escape") cmdSelectNone();
});

(function speedLimit() {
  const select = $("#speedLimit");
  if (!select) return;
  const saved = String(store.get("speedLimitKBps", 0));
  if ([...select.options].some((o) => o.value === saved)) select.value = saved;
  const send = (kbps) => api("/api/v1/files/rate-limit", { method: "PUT", body: JSON.stringify({ kbps }) }).catch(() => { });
  send(Number(select.value));
  select.addEventListener("change", () => { store.set("speedLimitKBps", Number(select.value)); send(Number(select.value)); toast(Number(select.value) ? `Transfers limited to ${select.selectedOptions[0].textContent}` : "Transfer speed is not limited"); });
})();
