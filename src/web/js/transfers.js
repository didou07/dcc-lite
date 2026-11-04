"use strict";
/* Transfers */
async function transferSelection(direction, given = null) {
  if (!requireSession()) return;
  const source = direction === "upload" ? panes.local : panes.remote;
  const entries = given || selectedEntries(source);
  if (!entries.length) { toast(direction === "upload" ? "Select local items to upload" : "Select remote items to download"); return; }
  const destPane = direction === "upload" ? panes.remote : panes.local;
  let existing;
  try {
    const listing = direction === "upload"
      ? await api(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/list?path=${encodeURIComponent(destPane.path)}`)
      : await api(`/api/v1/files/local/list?path=${encodeURIComponent(destPane.path)}`);
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
      ? { direction, local_path: entry.path, remote_path: joinRemote(destPane.path, entry.name), is_directory: entry.is_dir, overwrite: existing.has(entry.name) && overwrite }
      : { direction, local_path: joinLocal(destPane.path, entry.name), remote_path: entry.path, is_directory: entry.is_dir, overwrite: existing.has(entry.name) && overwrite };
    try { await post(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/transfer`, payload); queued++; }
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
function taskRow(task) {
  const percent = Math.round(Math.max(0, Math.min(1, task.progress?.fraction || (task.state === "completed" ? 1 : 0))) * 100);
  const live = task.state === "running" || task.state === "queued";
  const done = formatBytes(task.progress?.bytes_done || 0), total = task.progress?.bytes_total ? ` / ${formatBytes(task.progress.bytes_total)}` : "";
  const speed = Number(task.progress?.speed_bytes_per_second || 0);
  const details = [task.state === "queued" ? "Waiting" : `${done}${total}`, live && speed > 0 ? `${formatBytes(speed)}/s` : "", live ? formatETA(task.progress?.eta_seconds) : ""].filter(Boolean).join(" · ");
  const bar = h("div", { class: `bar${task.state === "queued" ? " indeterminate" : ""}` }, h("i"));
  bar.firstElementChild.style.width = `${percent}%`;
  const actions = h("div", { class: "t-actions" });
  const terminal = task.state === "failed" || task.state === "cancelled";
  if (live) actions.append(iconButton("x", "Cancel transfer", () => transferAction(task.id, "cancel")));
  if (terminal && task.can_resume) actions.append(iconButton("play", "Resume from the partial file", () => transferAction(task.id, "resume")), iconButton("refresh", "Start over", () => transferAction(task.id, "restart")));
  if (terminal && !task.can_resume) actions.append(iconButton("refresh", "Try again", () => retryTransfer(task)));
  return h("div", { class: "transfer", dataset: { state: task.state, id: task.id } },
    h("span", { class: "t-dir" }, icon(task.direction === "upload" ? "upload" : "download")),
    h("div", { class: "t-main" },
      h("div", { class: "t-name", title: `${task.local_path} ↔ ${task.remote_path}`, text: `${task.is_directory ? "📁 " : ""}${transferName(task)}` }),
      bar,
      task.error ? h("div", { class: "t-meta err", text: task.error }) : h("div", { class: "t-meta", text: `${task.state === "completed" ? "Completed · " : task.state === "cancelled" ? "Cancelled · " : ""}${details}` }),
      task.warning ? h("div", { class: "t-meta warn", text: task.warning }) : null),
    actions);
}
async function transferAction(id, action) {
  try {
    if (action === "cancel") await api(`/api/v1/files/transfers/${encodeURIComponent(id)}`, { method: "DELETE" });
    else await post(`/api/v1/files/transfers/${encodeURIComponent(id)}/${action}`);
    loadTransfers();
  } catch (error) { toastError(error); }
}
async function retryTransfer(task) {
  if (!files.sessionID) { toast("Connect to the device first"); return; }
  try {
    await post(`/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/transfer`, { direction: task.direction, local_path: task.local_path, remote_path: task.remote_path, is_directory: task.is_directory, overwrite: true });
    loadTransfers();
  } catch (error) { toastError(error); }
}
async function loadTransfers() {
  if (files.queueBusy) return;
  files.queueBusy = true;
  try {
    const { transfers } = await api("/api/v1/files/transfers");
    files.transfers = transfers || [];
    const list = $("#transferList");
    const active = files.transfers.filter((t) => t.state === "running" || t.state === "queued").length;
    const failed = files.transfers.filter((t) => t.state === "failed").length;
    $("#transferSummary").textContent = files.transfers.length ? `${active} active · ${files.transfers.length} total${failed ? ` · ${failed} failed` : ""}` : "No transfers yet";
    $("#transferClear").disabled = !files.transfers.some((t) => !["running", "queued"].includes(t.state));
    if (!files.transfers.length) list.replaceChildren(h("div", { class: "transfer-empty", text: "Select items and press Download / Upload, or drag them to the other panel." }));
    else list.replaceChildren(...files.transfers.slice(-40).reverse().map(taskRow));
    if (active && !files.queueTimer) files.queueTimer = window.setInterval(loadTransfers, 900);
    if (!active && files.queueTimer) { window.clearInterval(files.queueTimer); files.queueTimer = 0; }
    const finished = files.transfers.filter((t) => t.state === "completed" && !files.seenDone.has(t.id));
    finished.forEach((t) => files.seenDone.add(t.id));
    if (finished.length && view.current === "files") refreshBoth();
  } catch { /* queue unavailable */ }
  finally { files.queueBusy = false; }
}
$("#transferClear").addEventListener("click", async () => { try { await api("/api/v1/files/transfers", { method: "DELETE" }); loadTransfers(); } catch (error) { toastError(error); } });

/* Terminal handoff from Files */
function openTerminalHere(directory) {
  if (!requireSession()) return;
  const device = currentDevice();
  if (!device) return;
  createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), directory: directory || panes.remote.path });
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

/* Keyboard */
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
