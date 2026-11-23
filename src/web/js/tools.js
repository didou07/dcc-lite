"use strict";
const toolsState = { scope: "channels", busy: false, ui: "tools", ipkFile: null };
const toolsSessionURL = (tail) => sessionURL(files.sessionID, tail);
const toolsDirValue = () => $("#bqDir").value.trim() || "/etc/enigma2";
const toolsDirQuery = (lead) => (toolsDirValue() !== "/etc/enigma2" ? `${lead}dir=${encodeURIComponent(toolsDirValue())}` : "");
function toolsError(message) { const box = $(`#${toolsState.ui}Error`); box.textContent = message || ""; box.hidden = !message; }
function toolsBusy(busy, text = "") {
  toolsState.busy = busy;
  for (const id of ["#toolsBackup", "#toolsRestore", "#toolsIpk"]) $(id).disabled = busy;
  $(`#${toolsState.ui}Progress`).hidden = !busy;
  $(`#${toolsState.ui}ProgressText`).textContent = text;
  $(`#${toolsState.ui}Progress .tools-bar span`).style.width = "0";
  ipkRefresh();
}
function toolsProgress(fraction, text) {
  $(`#${toolsState.ui}Progress .tools-bar span`).style.width = `${Math.round(Math.max(0, Math.min(1, fraction)) * 100)}%`;
  $(`#${toolsState.ui}ProgressText`).textContent = text;
}
const IPK_MAX = 256 * 1048576;
const ipkSafeName = (name) => {
  let safe = name.replace(/\s+/g, "_").replace(/[^A-Za-z0-9._+~-]/g, "_").replace(/^[^A-Za-z0-9]+/, "").replace(/\.ipk$/i, "");
  safe = safe.slice(0, 146) || "package";
  return `${safe}.ipk`;
};
const ipkSize = (bytes) => (bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`);
function cmdIpk() { showTool("ipk"); }
function ipkEnter() {
  files.ready.then(async () => {
    const device = currentDevice();
    $("#ipkDevice").textContent = device?.name || "";
    if (!device) { toolsState.ui = "ipk"; toolsError("Add or choose a device first (Devices tab)."); ipkRefresh(); return; }
    toolsError("");
    syncFilePort();
    if (!files.sessionID && !files.connecting && !files.manualOff) await fileConnect();
    ipkRefresh();
  });
}
function ipkRefresh() {
  const button = $("#toolsIpk"); if (!button) return;
  const file = toolsState.ipkFile;
  const connected = Boolean(files.sessionID);
  button.disabled = toolsState.busy || !file || !connected;
  $("#ipkHint").textContent = !file ? "" : !connected ? "Connect to the receiver to install." : `Will be installed as /tmp/${ipkSafeName(file.name)}`;
}
function ipkSetFile(file) {
  toolsState.ui = "ipk";
  toolsError("");
  if (file && !/\.ipk$/i.test(file.name)) { toolsError(`“${file.name}” is not an .ipk package.`); file = null; }
  else if (file && file.size > IPK_MAX) { toolsError("The package is larger than 256 MB."); file = null; }
  else if (file && file.size === 0) { toolsError("The file is empty."); file = null; }
  toolsState.ipkFile = file;
  $("#ipkDrop").hidden = Boolean(file);
  $("#ipkFile").hidden = !file;
  if (file) {
    $("#ipkFileName").textContent = file.name;
    const safe = ipkSafeName(file.name);
    $("#ipkFileMeta").textContent = `${ipkSize(file.size)}${safe !== file.name ? ` · saved as ${safe}` : ""}`;
  }
  if (!file) $("#toolsIpkFile").value = "";
  ipkRefresh();
}
function uploadFile(url, file, label, fileName = file.name) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    xhr.responseType = "json";
    xhr.upload.onprogress = (event) => { if (event.lengthComputable) toolsProgress(event.loaded / event.total, `${label} ${Math.round(event.loaded / 1048576 * 10) / 10} of ${Math.round(event.total / 1048576 * 10) / 10} MB`); };
    xhr.upload.onload = () => toolsProgress(1, "Waiting for the receiver…");
    xhr.onerror = () => reject(new Error("The upload failed. Check that DCC Lite is still running."));
    xhr.onload = () => {
      const data = xhr.response || {};
      if (authExpired(xhr.status, data)) { reject(new Error("You were signed out. Opening the sign-in page…")); return; }
      if (xhr.status < 200 || xhr.status >= 300) { reject(new Error(data.error || `The upload was refused (${xhr.status}).`)); return; }
      resolve(data);
    };
    const form = new FormData();
    form.append("file", file, fileName);
    xhr.send(form);
  });
}
async function toolsBackup() {
  if (!requireSession()) return;
  toolsState.ui = "tools";
  toolsError(""); toolsBusy(true, "Reading the files from the receiver…");
  try {
    const response = await fetchChecked(toolsSessionURL(`enigma2/backup?scope=${toolsState.scope}${toolsDirQuery("&")}`));
    const blob = await response.blob();
    const name = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") || "")?.[1] || "enigma2-backup.zip";
    downloadBlob(blob, name);
    toast(`Backup saved as ${name}`);
  } catch (error) { toolsError(error.message); }
  toolsBusy(false);
}
async function toolsRestore() {
  if (!requireSession()) return;
  toolsState.ui = "tools";
  toolsError("");
  const file = $("#toolsRestoreFile").files[0];
  if (!file) { toast("Choose a backup .zip file first"); return; }
  const device = currentDevice();
  const confirmed = await ask({ title: "Restore channel list", message: `The channel list in “${file.name}” replaces the one on ${device?.name || "the receiver"}.`, input: false, ok: "Restore", danger: true });
  if (!confirmed) return;
  const result = $("#toolsRestoreResult"); result.hidden = true;
  toolsBusy(true, "Uploading the backup…");
  try {
    const data = await uploadFile(toolsSessionURL(`enigma2/restore${toolsDirQuery("?")}`), file, "Uploading");
    const skipped = (data.skipped || []).length;
    let text = `Restored ${data.restored.length} file${data.restored.length === 1 ? "" : "s"}.`;
    if (skipped) text += ` ${skipped} other file${skipped === 1 ? " was" : "s were"} ignored (not part of the channel list).`;
    text += data.reloaded ? " The receiver reloaded its channel lists." : ` The receiver could not reload them${data.reload_note ? ` (${data.reload_note})` : ""}.`;
    result.textContent = text; result.hidden = false;
    ch.cache.clear(); ch.epg.clear();
    if (!$("#channelPane").hidden) loadChannels();
    $("#toolsRestoreFile").value = ""; bkDropRefresh();
    bqReset(); bqLoadLists();
  } catch (error) { toolsError(error.message); }
  toolsBusy(false);
  rpRefresh();
}
async function toolsInstallIPK() {
  toolsState.ui = "ipk";
  toolsError("");
  const file = toolsState.ipkFile;
  if (!file) { toast("Choose an .ipk file first"); return; }
  if (!files.sessionID) { files.manualOff = false; await fileConnect(); }
  if (!requireSession()) return;
  const device = currentDevice();
  const name = ipkSafeName(file.name);
  const confirmed = await ask({ title: "Install package", message: `“${name}” is installed on ${device?.name || "the receiver"}. Install only packages you trust.`, input: false, ok: "Upload and install" });
  if (!confirmed) return;
  toolsBusy(true, "Uploading the package…");
  try {
    const flags = [$("#toolsIpkForce").checked && "force=1", $("#toolsIpkRestart").checked && "restart=1"].filter(Boolean);
    const options = flags.length ? `?${flags.join("&")}` : "";
    const data = await uploadFile(toolsSessionURL(`ipk${options}`), file, "Uploading", name);
    toolsBusy(false);
    ipkSetFile(null);
    createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), initialCommand: data.command });
    showView("terminal");
    toast("Package uploaded: the installation runs in the terminal");
    return;
  } catch (error) {
    if (sessionLost(error)) { files.sessionID = ""; toolsError("Connection lost. Press Connect."); }
    else toolsError(error.message);
  }
  toolsBusy(false);
}
$("#toolsBackup").addEventListener("click", toolsBackup);
$("#toolsRestore").addEventListener("click", toolsRestore);
$("#toolsIpk").addEventListener("click", toolsInstallIPK);
{
  const drop = $("#ipkDrop"), input = $("#toolsIpkFile");
  const choose = () => { if (!toolsState.busy) input.click(); };
  drop.addEventListener("click", choose);
  drop.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); choose(); } });
  input.addEventListener("change", () => ipkSetFile(input.files[0] || null));
  $("#ipkFileClear").addEventListener("click", () => { if (!toolsState.busy) ipkSetFile(null); });
  const page = $("#view-ipk");
  for (const type of ["dragenter", "dragover"]) page.addEventListener(type, (event) => { if (event.dataTransfer?.types?.includes("Files")) { event.preventDefault(); drop.classList.add("over"); } });
  for (const type of ["dragleave", "drop"]) page.addEventListener(type, (event) => { if (type === "dragleave" && page.contains(event.relatedTarget)) return; drop.classList.remove("over"); });
  page.addEventListener("drop", (event) => { event.preventDefault(); if (!toolsState.busy) ipkSetFile(event.dataTransfer.files[0] || null); });
  $("#ipkConnect").addEventListener("click", () => { files.manualOff = false; fileConnect().then(ipkRefresh); });
}
$("#toolsScope").addEventListener("click", (event) => {
  const button = event.target.closest("button[data-scope]");
  if (!button) return;
  toolsState.scope = button.dataset.scope;
  for (const b of $$("#toolsScope button")) b.setAttribute("aria-checked", String(b === button));
});

function bkDropRefresh() {
  const file = $("#toolsRestoreFile").files[0];
  $("#toolsDropName").textContent = file ? file.name : "Choose a backup .zip";
  $("#toolsDropHint").textContent = file ? `${(file.size / 1024).toFixed(file.size < 10240 ? 1 : 0)} KB · click to choose another` : "or drop it here";
  $("#toolsDrop").classList.toggle("has-file", !!file);
}
$("#toolsRestoreFile").addEventListener("change", bkDropRefresh);
for (const type of ["dragenter", "dragover"]) $("#toolsDrop").addEventListener(type, (event) => { event.preventDefault(); $("#toolsDrop").classList.add("drag"); });
for (const type of ["dragleave", "drop"]) $("#toolsDrop").addEventListener(type, () => $("#toolsDrop").classList.remove("drag"));
$("#toolsDrop").addEventListener("drop", (event) => {
  event.preventDefault();
  const file = event.dataTransfer.files[0];
  if (!file) return;
  const transfer = new DataTransfer(); transfer.items.add(file);
  $("#toolsRestoreFile").files = transfer.files; bkDropRefresh();
});

const REASONS = { "bouquet-edit": "Before editing the channel lists", "before-restore": "Before a restore" };
async function rpRefresh() {
  const list = $("#rpList"); if (!list) return;
  if (!files.sessionID) { list.replaceChildren(); $("#rpEmpty").hidden = true; return; }
  try {
    const { points } = await api(toolsSessionURL("enigma2/restore-points"));
    list.replaceChildren(...points.map((p) => h("li", { class: "rp-row" },
      h("div", { class: "rp-info" },
        h("strong", { text: new Date(p.created).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) }),
        h("span", { class: "muted small", text: `${REASONS[p.reason] || p.reason} · ${ipkSize(p.size)}` })),
      h("span", { class: "spacer" }),
      h("button", { class: "btn", type: "button", title: "Put this copy of the channel list back on the receiver", onClick: () => rpRestore(p) }, icon("restore"), "Restore"),
      iconButton("trash", "Delete this restore point", () => rpDelete(p)))));
    $("#rpEmpty").hidden = points.length > 0;
  } catch { list.replaceChildren(); $("#rpEmpty").hidden = true; }
}
async function rpRestore(point) {
  if (!requireSession() || toolsState.busy) return;
  toolsState.ui = "tools"; toolsError("");
  const device = currentDevice();
  const when = new Date(point.created).toLocaleString();
  const confirmed = await ask({ title: "Restore this copy", message: `The list saved on ${when} replaces the one on ${device?.name || "the receiver"}. The current list is saved first.`, input: false, ok: "Restore", danger: true });
  if (!confirmed) return;
  const result = $("#toolsRestoreResult"); result.hidden = true;
  toolsBusy(true, "Putting the saved copy back…");
  try {
    const data = await post(toolsSessionURL(`enigma2/restore-points/${encodeURIComponent(point.name)}/restore${toolsDirQuery("?")}`));
    result.textContent = `Restored ${data.restored.length} file${data.restored.length === 1 ? "" : "s"}.` + (data.reloaded ? " The receiver reloaded its channel lists." : ` The receiver could not reload them${data.reload_note ? ` (${data.reload_note})` : ""}.`);
    result.hidden = false;
    ch.cache.clear(); ch.epg.clear();
    if (!$("#channelPane").hidden) loadChannels();
    bqReset(); bqLoadLists();
  } catch (error) { toolsError(error.message); }
  toolsBusy(false);
  rpRefresh();
}
async function rpDelete(point) {
  if (!requireSession()) return;
  const confirmed = await ask({ title: "Delete restore point", message: `Delete the copy saved on ${new Date(point.created).toLocaleString()}? This cannot be undone.`, input: false, ok: "Delete", danger: true });
  if (!confirmed) return;
  try { await api(toolsSessionURL(`enigma2/restore-points/${encodeURIComponent(point.name)}`), { method: "DELETE" }); } catch (error) { toast(error.message, "error"); }
  rpRefresh();
}

// ---- Flash image backup (read-only) --------------------------------------------------------------
const flashState = { busy: false };
const flashSize = (bytes) => (bytes >= 1048576 ? `${(bytes / 1048576).toFixed(bytes % 1048576 ? 1 : 0)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`);
function flashMessage(text, bad = false) {
  const box = $("#flashResult");
  box.textContent = text; box.hidden = !text; box.classList.toggle("bad", bad);
}
function flashSetBusy(busy) {
  flashState.busy = busy;
  $("#flashRead").disabled = busy;
  for (const b of $$("#flashList button")) b.disabled = busy;
}
async function flashRead() {
  const device = currentDevice();
  if (!device) { toast("Add or choose a device first (Devices tab)."); return; }
  flashMessage(""); flashSetBusy(true);
  try {
    const { partitions } = await api(profileURL(device.id, "flash/partitions"));
    $("#flashList").replaceChildren(...partitions.map((p) => h("li", { class: "rp-row" },
      h("div", { class: "rp-info" },
        h("strong", { text: `mtd${p.index} · ${p.name || "(no name)"}` }),
        h("span", { class: "muted small", text: flashSize(p.size) })),
      h("span", { class: "spacer" }),
      h("button", { class: "btn", type: "button", title: `Save mtd${p.index} to this computer`, onClick: () => flashBackup(device, p) }, icon("download"), "Back up"))));
  } catch (error) { $("#flashList").replaceChildren(); flashMessage(error.message, true); }
  flashSetBusy(false);
}
async function flashBackup(device, part) {
  const confirmed = await ask({
    title: "Back up flash partition",
    message: `mtd${part.index} (${part.name || "no name"}, ${flashSize(part.size)}) is copied from ${device.name} into its memory, downloaded, checked, and the copy on the receiver is deleted. Nothing is written to the flash.`,
    input: false, ok: "Back up",
  });
  if (!confirmed) return;
  flashMessage(""); flashSetBusy(true);
  toast(`Backing up mtd${part.index}… this can take a few minutes`);
  try {
    const response = await fetchChecked(profileURL(device.id, `flash/backup?partition=${part.index}`), { method: "POST" });
    const blob = await response.blob();
    const name = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") || "")?.[1] || `flash-mtd${part.index}.bin`;
    downloadBlob(blob, name);
    const verified = response.headers.get("X-Flash-Verified") === "true";
    flashMessage(`Saved ${name} (${flashSize(blob.size)}). MD5 ${response.headers.get("X-Flash-MD5") || ""}${verified ? ", matches the receiver's checksum." : ". The receiver has no md5sum, so only the size was checked."}`);
  } catch (error) { flashMessage(error.message, true); }
  flashSetBusy(false);
}
$("#flashRead").addEventListener("click", flashRead);

// ---- recordings.epl (Enigma1) --------------------------------------------------------------------
const eplState = { report: null };
function eplMessage(text, bad = false) {
  const box = $("#eplResult");
  box.textContent = text; box.hidden = !text; box.classList.toggle("bad", bad);
}
const eplCount = (n, one, many) => `${n} ${n === 1 ? one : many}`;
async function eplCheck() {
  if (!requireSession()) return;
  eplMessage(""); $("#eplRebuild").disabled = true;
  try {
    const r = await api(toolsSessionURL("recordings/epl"));
    eplState.report = r;
    const lost = r.lost.length + (r.more || 0);
    let text = `${eplCount(r.movies, "recording", "recordings")} in ${r.movie_dir}, ${eplCount(r.entries, "entry", "entries")} in the list. `;
    text += lost ? `${eplCount(lost, "recording is", "recordings are")} missing from the list.` : "Every recording is listed.";
    if (r.dangling.length) text += ` ${eplCount(r.dangling.length, "entry points", "entries point")} at a file that is gone.`;
    if (!r.found) text += " There is no recordings.epl yet; it will be created.";
    eplMessage(text);
    $("#eplRebuild").disabled = !(lost || r.dangling.length && $("#eplPrune").checked || !r.found && r.movies);
  } catch (error) { eplMessage(error.message, true); }
}
async function eplRebuild() {
  if (!requireSession()) return;
  const prune = $("#eplPrune").checked;
  const confirmed = await ask({ title: "Rebuild recordings list", message: `Recordings missing from recordings.epl are added${prune ? ", and entries whose file is gone are removed" : ""}. The old list is kept as recordings.bak.`, input: false, ok: "Rebuild" });
  if (!confirmed) return;
  eplMessage(""); $("#eplRebuild").disabled = true;
  try {
    const r = await api(toolsSessionURL(`recordings/epl/rebuild${prune ? "?prune=1" : ""}`), { method: "POST" });
    if (!r.changed) { eplMessage(r.message || "Nothing to change."); return; }
    let text = `Added ${r.added}${r.removed ? `, removed ${r.removed}` : ""}.`;
    text += r.backup ? " The old list was kept as recordings.bak." : "";
    text += r.reloaded ? " The receiver reloaded its recordings." : ` The receiver could not reload them${r.reload_note ? ` (${r.reload_note})` : ""}.`;
    eplMessage(text);
  } catch (error) { eplMessage(error.message, true); }
}
$("#eplCheck").addEventListener("click", eplCheck);
$("#eplRebuild").addEventListener("click", eplRebuild);
$("#eplPrune").addEventListener("change", () => { if (eplState.report) eplCheck(); });
