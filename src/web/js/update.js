"use strict";
let updState = null;
let updManage = true;
function updError(message) {
  const node = $("#updError");
  node.hidden = !message;
  node.textContent = message || "";
}
function updRender(status) {
  if (!status) return;
  updState = status;
  const badge = $("#updateBadge");
  badge.hidden = !(status.available && status.supported);
  if (status.available) $("#updateBadge span").textContent = `Update to v${status.latest}`;
  $("#updAuto").checked = Boolean(status.auto);
  $("#updAuto").disabled = !status.supported || !updManage;
  const busy = status.busy;
  $("#updCheck").disabled = Boolean(busy) || !status.supported || !updManage;
  $("#updInstall").hidden = !status.available || !status.supported || !updManage;
  $("#updInstall").disabled = Boolean(busy);
  $("#updState").textContent = busy === "restarting" ? "Restarting…" : busy === "installing" ? "Installing…" : busy === "checking" ? "Checking…" : "";
  let text;
  if (!status.supported) text = status.reason || "Updates are not available for this build.";
  else if (status.available) text = `Version ${status.latest} is available (you have ${status.current}).`;
  else if (status.checked_at && !status.checked_at.startsWith("0001")) text = `You have the latest version (${status.current}).`;
  else text = `Installed version: ${status.current}.`;
  $("#updSummary").textContent = text;
  const notes = $("#updNotes");
  notes.hidden = !(status.available && status.notes);
  notes.textContent = status.notes || "";
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
$("#updAuto").addEventListener("change", async () => {
  try {
    const data = await api("/api/v1/update/settings", { method: "PUT", body: JSON.stringify({ auto: $("#updAuto").checked }) });
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
