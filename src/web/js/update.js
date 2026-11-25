"use strict";
let updState = null;
let updManage = true;
function updError(message) {
  const node = $("#updError");
  node.hidden = !message;
  node.textContent = message || "";
}
function updCleanNotes(value) {
  return String(value || "")
    .replace(/^\s*\*{0,2}Full Changelog\*{0,2}\s*:\s*https?:\/\/\S+.*$/gim, "")
    .replace(/\n[ \t]*\n(?:[ \t]*\n)+/g, "\n\n")
    .trim();
}
function updAnnounce(status) {
  if (!status.available || !status.supported || !status.notify) return;
  try {
    if (localStorage.getItem("dcc-update-seen") === status.latest) return;
    localStorage.setItem("dcc-update-seen", status.latest);
  } catch { }
  toast("A new DCC Lite version is available. Open Software update to install it when you are ready.");
}
function updRender(status) {
  if (!status) return;
  updState = status;
  const badge = $("#updateBadge");
  badge.hidden = !(status.available && status.supported && status.notify);
  if (status.available) $("#updateBadge span").textContent = `Update to v${status.latest}`;
  $("#updNotify").checked = Boolean(status.notify);
  $("#updNotify").disabled = !status.supported || !updManage;
  updAnnounce(status);
  const busy = status.busy;
  $("#updCheck").disabled = Boolean(busy) || !status.supported || !updManage;
  $("#updInstall").hidden = !status.available || !status.supported || !updManage;
  $("#updInstall").disabled = Boolean(busy);
  $("#updState").textContent = busy === "restarting" ? "Restarting…" : busy === "installing" ? "Installing…" : busy === "checking" ? "Checking…" : "";
  let text;
  if (!status.supported) text = status.reason || "Updates are not available for this build.";
  else if (status.available) text = `Version ${status.latest} is available.`;
  else if (status.checked_at && !status.checked_at.startsWith("0001")) text = `You have the latest version (${status.current}).`;
  else text = `Installed version: ${status.current}.`;
  $("#updSummary").textContent = text;
  const notes = $("#updNotes");
  const cleanNotes = updCleanNotes(status.notes);
  notes.hidden = !(status.available && cleanNotes);
  notes.textContent = cleanNotes;
  if (status.error) updError(status.error);
}
async function updLoad() {
  try {
    const data = await api("/api/v1/update");
    updManage = data.can_manage !== false;
    updRender(data.update);
  } catch { /* updates unavailable; keep the UI hidden */ }
}
async function updOpen() {
  updError("");
  await updLoad();
  openDialog($("#updDialog"));
}
$("#updateBadge").addEventListener("click", updOpen);
$("#sbVersion").addEventListener("click", updOpen);
$("#sbVersion").classList.add("clickable");
$("#updClose").addEventListener("click", () => $("#updDialog").close());
$("#updCheck").addEventListener("click", async () => {
  updError("");
  $("#updCheck").disabled = true;
  try {
    const data = await post("/api/v1/update/check", {});
    updRender(data.update);
    if (data.error) updError(data.error);
  } catch (error) { updError(error.message); }
  finally { $("#updCheck").disabled = !updState?.supported; }
});
$("#updNotify").addEventListener("change", async () => {
  try {
    const data = await api("/api/v1/update/settings", { method: "PUT", body: JSON.stringify({ notify: $("#updNotify").checked }) });
    updRender(data.update);
  } catch (error) { updError(error.message); await updLoad(); }
});
$("#updInstall").addEventListener("click", async () => {
  updError("");
  $("#updInstall").disabled = true;
  $("#updState").textContent = "Installing…";
  try {
    await post("/api/v1/update/install", {});
    $("#updState").textContent = "Restarting…";
    toast("Update installed. DCC Lite is restarting…");
    waitForRestart();
  } catch (error) { updError(error.message); await updLoad(); }
});
function waitForRestart() {
  let tries = 0;
  const timer = window.setInterval(async () => {
    tries += 1;
    try {
      const response = await fetch("/api/v1/health", { cache: "no-store" });
      if (response.ok && tries > 2) { window.clearInterval(timer); window.location.reload(); }
    } catch { /* still restarting */ }
    if (tries > 60) { window.clearInterval(timer); updError("DCC Lite did not come back. Start it again manually."); }
  }, 1000);
}
window.addEventListener("load", () => {
  updLoad();
  window.setInterval(updLoad, 30 * 60 * 1000);
});
