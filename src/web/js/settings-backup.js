"use strict";
const stState = { busy: false };
const stNames = { channels: "Channels", settings: "Settings", softcam: "Softcam" };
const stIsZip = (file) => /\.zip$/i.test(file?.name || "");
function stMessage(text, bad = false) {
  const n = $("#stResult"), parts = [].concat(text || []).filter(Boolean);
  n.hidden = !parts.length;
  n.replaceChildren(...parts.flatMap((p, i) => [i ? " " : "", h("span", { text: p })]).filter((x) => x !== ""));
  n.classList.toggle("is-error", Boolean(bad));
}
function stScopes() { return $$("#stScopes input:checked").map((i) => i.value); }
function stPick(file) {
  $("#stDropName").textContent = file ? file.name : "Choose a backup file";
  $("#stDrop").classList.toggle("has-file", Boolean(file));
  $("#stAfterField").hidden = stIsZip(file);
}
function stBusy(busy) { stState.busy = busy; for (const id of ["#stBackup", "#stRestore"]) $(id).disabled = busy; }

async function stDownloadBackup(device, scopes) {
  const response = await fetchChecked(profileURL(device.id, "settings/backup"), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ scopes }) });
  const blob = await response.blob();
  const name = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") || "")?.[1] || "backup.tar.gz";
  downloadBlob(blob, name);
  const left = Number(response.headers.get("X-Settings-Skipped") || 0);
  return [`Saved ${name}.`, "Keep it private: it can hold passwords.", left && (left === 1 ? "1 unsupported item skipped." : `${left} unsupported items skipped.`)];
}

async function stBackup() {
  const device = currentDevice();
  if (!device) { toast("Choose a device first"); return; }
  const scopes = stScopes();
  if (!scopes.length) { toast("Choose what to back up", "error"); return; }
  stBusy(true); stMessage("Backing up…");
  try { stMessage(await stDownloadBackup(device, scopes)); } catch (error) { stMessage(error.message, true); }
  stBusy(false);
}

async function stRestore() {
  const device = currentDevice();
  const file = $("#stFile").files[0];
  if (!device) { toast("Choose a device first"); return; }
  if (!file) { toast("Choose a backup file first"); return; }
  if (stIsZip(file)) { await toolsRestoreZip(file); return; }
  stBusy(true); stMessage("Checking the backup…");
  let preview;
  try {
    const form = new FormData(); form.append("file", file, file.name);
    preview = await (await fetchChecked(profileURL(device.id, "settings/preview"), { method: "POST", body: form })).json();
  } catch (error) { stMessage(error.message, true); stBusy(false); return; }
  const parts = Object.entries(preview.scopes || {}).map(([k, n]) => `${DCC_I18N.t(stNames[k] || k)}: ${n}`).join(" · ");
  const reboot = $("#stAfter").value === "reboot", T = DCC_I18N.t;
  const ok = await ask({
    title: "Restore backup",
    message: [T(`Restore “${file.name}” to ${device.name}?`), `${parts}.`, T("Your current files are saved on this computer first."), T(reboot ? "The receiver reboots." : "Enigma2 restarts.")].join(" "),
    input: false, ok: "Restore", danger: true,
  });
  if (!ok) { stMessage(""); stBusy(false); return; }
  stMessage("Restoring…");
  try {
    const form = new FormData(); form.append("file", file, file.name);
    const r = await (await fetchChecked(profileURL(device.id, `settings/restore?after=${reboot ? "reboot" : "restart"}`), { method: "POST", body: form })).json();
    stMessage([r.restored === 1 ? "Restored 1 file." : `Restored ${r.restored} files.`, r.note, r.safety_copy && `Previous files saved in ${r.safety_dir}.`]);
    $("#stFile").value = ""; stPick(null);
  } catch (error) { stMessage(error.message, true); }
  stBusy(false);
}
$("#stBackup").addEventListener("click", stBackup);
$("#stRestore").addEventListener("click", stRestore);
$("#stFile").addEventListener("change", () => stPick($("#stFile").files[0]));
{
  const drop = $("#stDrop");
  for (const type of ["dragenter", "dragover"]) drop.addEventListener(type, (e) => { e.preventDefault(); drop.classList.add("drag"); });
  for (const type of ["dragleave", "drop"]) drop.addEventListener(type, (e) => { e.preventDefault(); drop.classList.remove("drag"); });
  drop.addEventListener("drop", (e) => {
    const file = e.dataTransfer?.files?.[0];
    if (!file) return;
    try { const dt = new DataTransfer(); dt.items.add(file); $("#stFile").files = dt.files; } catch (_) { return; }
    stPick(file);
  });
}
