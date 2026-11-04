"use strict";
/* ───────────── Start ───────────── */
(async function start() {
  applyTerminalLook();
  api("/api/v1/health").then((health) => { $(".brand").title = `DCC Lite ${health.version}`; }).catch(() => {});
  try { await loadDevices(); } catch (error) { toastError(error); }
  const saved = store.get("view", "devices");
  const first = devices.list.length ? saved : "devices";
  showView(first);
  if (first === "devices") testAllDevices();
  else if (devices.currentID && !devices.status.has(devices.currentID)) testDevice(devices.currentID);
})();

/* Remote (HTTPS) access: show Sign out only when served by the authenticated listener. */
api("/api/v1/system").then((info) => {
  if (info?.remote !== "true") { $("#remoteAccessBtn").hidden = false; return; } // only the local page manages remote access
  const button = $("#logoutButton");
  button.hidden = false;
  button.addEventListener("click", async () => {
    try { await post("/api/v1/auth/logout", {}); } catch { /* already signed out */ }
    window.location.assign("/login");
  });
}).catch(() => {});
