"use strict";
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

api("/api/v1/system").then((info) => {
  if (info?.remote !== "true") { $("#remoteAccessBtn").hidden = false; return; }
  const beat = () => fetch("/api/v1/health", { cache: "no-store" }).then((r) => { if (r.status === 401) window.location.assign("/login"); }).catch(() => {});
  window.setInterval(beat, 4 * 60 * 1000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) beat(); });
  const button = $("#logoutButton");
  button.hidden = false;
  button.addEventListener("click", async () => {
    try { await post("/api/v1/auth/logout", {}); } catch { }
    window.location.assign("/login");
  });
}).catch(() => {});

document.addEventListener("keydown", (event) => {
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  const target = event.target;
  const typing = target.closest?.("input, textarea, select, [contenteditable], .xterm");
  if (event.key === "/" && !typing) {
    const box = view.current === "files" ? panes[files.active].filter
      : view.current === "screen" ? $(".ch-tools .filter-input")
      : view.current === "lists" ? $("#bqFilter") : null;
    if (box && !box.disabled) { event.preventDefault(); box.focus(); box.select(); }
  } else if (event.key === "Escape" && target.matches?.(".filter-input")) {
    if (target.value) { target.value = ""; target.dispatchEvent(new Event("input", { bubbles: true })); }
    else target.blur();
  }
});
