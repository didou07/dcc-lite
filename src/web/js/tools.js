"use strict";
/* Device tools: backup / restore of the channel list, .ipk install */
const toolsState = { scope: "channels", busy: false };
const toolsSessionURL = (tail) => `/api/v1/files/sessions/${encodeURIComponent(files.sessionID)}/${tail}`;
// The settings folder chosen in the bouquet editor, when it is not the default /etc/enigma2.
const toolsDirQuery = (lead) => (bq.dir && bq.dir !== "/etc/enigma2" ? `${lead}dir=${encodeURIComponent(bq.dir)}` : "");
function toolsError(message) { const box = $("#toolsError"); box.textContent = message || ""; box.hidden = !message; }
function toolsBusy(busy, text = "") {
  toolsState.busy = busy;
  for (const id of ["#toolsBackup", "#toolsRestore", "#toolsIpk"]) $(id).disabled = busy;
  $("#toolsProgress").hidden = !busy;
  $("#toolsProgressText").textContent = text;
  $("#toolsProgress .tools-bar span").style.width = "0";
}
function toolsProgress(fraction, text) {
  $("#toolsProgress .tools-bar span").style.width = `${Math.round(Math.max(0, Math.min(1, fraction)) * 100)}%`;
  $("#toolsProgressText").textContent = text;
}
function cmdTools() {
  if (!requireSession()) return;
  toolsError(""); toolsBusy(false);
  $("#toolsDevice").textContent = currentDevice()?.name || "";
  $("#toolsRestoreResult").hidden = true;
  openDialog($("#toolsDialog"));
}
/** Upload one file as multipart with progress; resolves to the JSON answer. */
function uploadFile(url, file, label) {
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
    form.append("file", file, file.name);
    xhr.send(form);
  });
}
async function toolsBackup() {
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
  } catch (error) { toolsError(error.message); }
  toolsBusy(false);
}
async function toolsInstallIPK() {
  toolsError("");
  const file = $("#toolsIpkFile").files[0];
  if (!file) { toast("Choose an .ipk file first"); return; }
  const device = currentDevice();
  const confirmed = await ask({ title: "Install package", message: `“${file.name}” is uploaded to /tmp on ${device?.name || "the receiver"} and installed with opkg in a Terminal tab. Only install packages you trust.`, input: false, ok: "Upload and install" });
  if (!confirmed) return;
  toolsBusy(true, "Uploading the package…");
  try {
    const data = await uploadFile(toolsSessionURL("ipk"), file, "Uploading");
    toolsBusy(false);
    $("#toolsDialog").close();
    createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol), initialCommand: data.command });
    showView("terminal");
    toast("Package uploaded: the installation runs in the terminal");
    return;
  } catch (error) { toolsError(error.message); }
  toolsBusy(false);
}
$("#toolsBackup").addEventListener("click", toolsBackup);
$("#toolsRestore").addEventListener("click", toolsRestore);
$("#toolsIpk").addEventListener("click", toolsInstallIPK);
$("#toolsClose").addEventListener("click", () => $("#toolsDialog").close());
$("#toolsScope").addEventListener("click", (event) => {
  const button = event.target.closest("button[data-scope]");
  if (!button) return;
  toolsState.scope = button.dataset.scope;
  for (const b of $$("#toolsScope button")) b.setAttribute("aria-checked", String(b === button));
});
