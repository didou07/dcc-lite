"use strict";
const toolsState = { scope: "channels", busy: false, ui: "tools", ipkFile: null };
const toolsSessionURL = (tail) => `/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/${tail}`;
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
      if (xhr.status === 401 && data.code === "auth_required") { window.location.assign("/login"); return; }
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
    const response = await fetch(toolsSessionURL(`enigma2/backup?scope=${toolsState.scope}${toolsDirQuery("&")}`));
    if (!response.ok) { const data = await response.json().catch(() => ({})); throw new Error(data.error || `The backup failed (${response.status}).`); }
    const blob = await response.blob();
    const name = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") || "")?.[1] || "enigma2-backup.zip";
    const link = h("a", { href: URL.createObjectURL(blob), download: name });
    document.body.append(link); link.click(); link.remove();
    window.setTimeout(() => URL.revokeObjectURL(link.href), 10000);
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
  const confirmed = await ask({ title: "Restore channel list", message: `The channel list files in “${file.name}” replace the ones on ${device?.name || "the receiver"}, and the receiver reloads them. Other settings stay as they are. Download a backup of the current list first if you may want it back.`, input: false, ok: "Restore", danger: true });
  if (!confirmed) return;
  const result = $("#toolsRestoreResult"); result.hidden = true;
  toolsBusy(true, "Uploading the backup…");
  try {
    const data = await uploadFile(toolsSessionURL(`enigma2/restore${toolsDirQuery("?")}`), file, "Uploading");
    const skipped = (data.skipped || []).length;
    let text = `Restored ${data.restored.length} file${data.restored.length === 1 ? "" : "s"}.`;
    if (skipped) text += ` ${skipped} other file${skipped === 1 ? " was" : "s were"} ignored (not part of the channel list).`;
    text += data.reloaded ? " The receiver reloaded its channel lists." : ` The files are in place, but the receiver could not be told to reload them${data.reload_note ? ` (${data.reload_note})` : ""}. Use Power → Reload channel lists, or restart Enigma2.`;
    result.textContent = text; result.hidden = false;
    ch.cache.clear(); ch.epg.clear();
    if (!$("#channelPane").hidden) loadChannels();
    $("#toolsRestoreFile").value = "";
    bqReset(); bqLoadLists();
  } catch (error) { toolsError(error.message); }
  toolsBusy(false);
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
  const confirmed = await ask({ title: "Install package", message: `“${name}” is sent to /tmp on ${device?.name || "the receiver"} and installed with opkg in a Terminal tab. Only install packages you trust.`, input: false, ok: "Upload and install" });
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
    if (sessionLost(error)) { files.sessionID = ""; toolsError("The connection to the receiver was lost. Press Connect and try again."); }
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
