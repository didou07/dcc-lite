"use strict";
/* ───────────── Live screen ─────────────
   The receiver can take a picture of what it shows on the TV (/grab in its web interface).
   DCC Lite asks for one picture after the other, so the dialog behaves like a live view
   without installing anything on the receiver. Only one picture is in flight at a time,
   so a slow receiver is never flooded. */
const screenView = { profileID: "", running: false, gen: 0, paused: false, controller: null, wake: null, objectURL: "", blob: null, stamps: [], failures: 0 };
const screenSleep = (ms) => new Promise((resolve) => { const timer = window.setTimeout(resolve, ms); screenView.wake = () => { window.clearTimeout(timer); resolve(); }; });

function screenQuery() {
  const params = new URLSearchParams({ mode: $("#screenMode").value, width: $("#screenWidth").value });
  return `/api/v1/profiles/${encodeURIComponent(screenView.profileID)}/screen?${params}`;
}
function screenShowError(message) {
  const node = $("#screenError");
  node.hidden = !message;
  node.textContent = message || "";
}
function screenShowStats(extra = "") {
  const now = performance.now();
  screenView.stamps = screenView.stamps.filter((t) => now - t < 5000);
  const rate = screenView.stamps.length > 1 ? (screenView.stamps.length - 1) / ((screenView.stamps[screenView.stamps.length - 1] - screenView.stamps[0]) / 1000) : 0;
  const img = $("#screenImg");
  const parts = [];
  if (!img.hidden && img.naturalWidth) parts.push(`${rate ? rate.toFixed(1) : "0.0"} pictures/s`, `${img.naturalWidth}×${img.naturalHeight}`, formatBytes(screenView.blob?.size || 0));
  if (extra) parts.push(extra);
  $("#screenStats").textContent = parts.join(" · ");
}
async function screenSetPicture(blob, gen) {
  // Decode the new picture off screen first, so the visible one is replaced in one
  // step and never flashes blank between two frames.
  const url = URL.createObjectURL(blob);
  try {
    const probe = new Image();
    probe.src = url;
    if (probe.decode) await probe.decode();
  } catch { /* show it anyway; the load handler reports a broken picture */ }
  if (gen !== screenView.gen) { URL.revokeObjectURL(url); return false; }
  const previous = screenView.objectURL;
  screenView.objectURL = url;
  screenView.blob = blob;
  const img = $("#screenImg");
  img.src = url;
  img.hidden = false;
  $("#screenWait").hidden = true;
  if (previous) window.setTimeout(() => URL.revokeObjectURL(previous), 1000);
  screenView.stamps.push(performance.now());
  return true;
}

async function screenLoop(gen) {
  while (gen === screenView.gen) {
    if (screenView.paused || document.hidden) { await screenSleep(500); continue; }
    const began = performance.now();
    const controller = new AbortController();
    screenView.controller = controller;
    let delay = Number($("#screenSpeed").value) || 1000;
    try {
      const response = await fetch(screenQuery(), { signal: controller.signal, cache: "no-store" });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        if (response.status === 401 && data.code === "auth_required") { window.location.assign("/login"); return; }
        const error = new Error(data.error || `Request failed (${response.status})`);
        error.fatal = response.status === 404 || response.status === 409;
        throw error;
      }
      const blob = await response.blob();
      if (gen !== screenView.gen) return;
      if (!(await screenSetPicture(blob, gen))) return;
      screenShowError("");
      screenView.failures = 0;
      screenShowStats();
    } catch (error) {
      if (gen !== screenView.gen) return;
      if (error.name === "AbortError") continue; // the mode or size changed: ask again at once
      screenView.failures += 1;
      screenShowError(error.message);
      screenShowStats("retrying…");
      if (error.fatal) { screenView.running = false; return; } // entering the tab again starts over
      delay = Math.min(8000, 1000 * screenView.failures);
    }
    await screenSleep(Math.max(100, delay - (performance.now() - began)));
  }
}

function setPauseLabel(paused) {
  $("#screenPause span").textContent = paused ? "Resume" : "Pause";
  $("#screenPause use").setAttribute("href", paused ? "#i-play" : "#i-pause");
}
function screenStop() {
  channelsStop();
  screenView.running = false;
  screenView.gen += 1;
  screenView.controller?.abort();
  screenView.wake?.();
  if (screenView.objectURL) { URL.revokeObjectURL(screenView.objectURL); screenView.objectURL = ""; }
  screenView.blob = null;
  const img = $("#screenImg");
  img.removeAttribute("src");
  img.hidden = true;
}
function screenRestart() {
  // A new mode or size should show up at once instead of after the current wait.
  screenView.controller?.abort();
  screenView.wake?.();
}

/** Called when the Screen tab is shown or the selected device changes while it is shown. */
function screenEnter() {
  const device = currentDevice();
  if (!device) {
    screenStop();
    screenView.profileID = "";
    channelsReset();
    $("#screenSub").textContent = "No device selected";
    $("#screenWait").hidden = false;
    $("#screenWait").textContent = "Choose a device in the list at the top.";
    return;
  }
  if (device.id === screenView.profileID && screenView.running) return;
  screenStop();
  Object.assign(screenView, { profileID: device.id, paused: false, stamps: [], failures: 0, running: true });
  $("#screenSub").textContent = `${device.name} · ${device.host}`;
  for (const [key, select, fallback] of [["screen.mode", "#screenMode", "all"], ["screen.width", "#screenWidth", "720"], ["screen.speed", "#screenSpeed", "1000"]]) {
    const saved = String(store.get(key, fallback));
    $(select).value = [...$(select).options].some((o) => o.value === saved) ? saved : fallback;
  }
  setPauseLabel(false);
  setRemoteOpen(store.get("remote.open", true) !== false);
  setChannelsOpen(store.get("channels.open", true) !== false);
  $("#remoteRefresh").checked = store.get("remote.refresh", true) !== false;
  remoteError("");
  $("#screenSave").disabled = true;
  $("#screenWait").hidden = false;
  $("#screenWait").textContent = "Waiting for the first picture…";
  screenShowError("");
  $("#screenStats").textContent = "";
  screenLoop(screenView.gen);
  channelsEnter();
}

for (const [select, key] of [["#screenMode", "screen.mode"], ["#screenWidth", "screen.width"], ["#screenSpeed", "screen.speed"]]) {
  $(select).addEventListener("change", () => { store.set(key, $(select).value); screenRestart(); });
}
$("#screenPause").addEventListener("click", () => {
  screenView.paused = !screenView.paused;
  setPauseLabel(screenView.paused);
  if (screenView.paused) screenShowStats("paused"); else screenRestart();
});
$("#screenSave").addEventListener("click", () => {
  if (!screenView.blob) return;
  const d = new Date(), pad = (n) => String(n).padStart(2, "0");
  const name = `screen-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.jpg`;
  const link = h("a", { href: URL.createObjectURL(screenView.blob), download: name });
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(link.href), 2000);
});
$("#screenImg").addEventListener("load", () => { $("#screenSave").disabled = false; });
document.addEventListener("visibilitychange", () => { if (!document.hidden && view.current === "screen") screenRestart(); });
