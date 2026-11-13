"use strict";
const screenView = { profileID: "", running: false, gen: 0, paused: false, controller: null, wake: null, objectURL: "", blob: null, stamps: [], failures: 0, lastStart: -1e9, once: false };
const screenSleep = (ms) => new Promise((resolve) => { const timer = window.setTimeout(resolve, ms); screenView.wake = () => { window.clearTimeout(timer); resolve(); }; });

const screenIntervalMs = () => {
  const seconds = Number($("#screenSpeed").value);
  return Math.min(3600, Math.max(1, Number.isFinite(seconds) && seconds > 0 ? seconds : 30)) * 1000;
};
function screenQuery() {
  const params = new URLSearchParams({ mode: $("#screenMode").value, width: $("#screenWidth").value });
  return profileURL(screenView.profileID, `screen?${params}`);
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
  if (!img.hidden && img.naturalWidth) parts.push(`updated ${new Date(screenView.shownAt || Date.now()).toLocaleTimeString()}`, `${img.naturalWidth}×${img.naturalHeight}`, formatBytes(screenView.blob?.size || 0));
  if (extra) parts.push(extra);
  $("#screenStats").textContent = parts.join(" · ");
}
async function screenSetPicture(blob, gen) {
  const url = URL.createObjectURL(blob);
  try {
    const probe = new Image();
    probe.src = url;
    if (probe.decode) await probe.decode();
  } catch { }
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
  screenView.shownAt = Date.now();
  return true;
}

async function screenLoop(gen) {
  while (gen === screenView.gen) {
    const auto = $("#screenAuto").checked;
    if ((screenView.paused && !screenView.once) || document.hidden) { await screenSleep(500); continue; }
    if (!auto && !screenView.once) { await screenSleep(60000); continue; }
    const floor = auto ? screenIntervalMs() : 1000;
    const since = performance.now() - screenView.lastStart;
    if (since < floor) {
      await sleep(floor - since);
      if (gen !== screenView.gen) return;
      if (screenView.paused || document.hidden) continue;
    }
    const began = performance.now();
    screenView.lastStart = began;
    const controller = new AbortController();
    screenView.controller = controller;
    let delay = screenIntervalMs();
    screenView.once = false;
    try {
      const response = await fetchChecked(screenQuery(), { signal: controller.signal, cache: "no-store" });
      const blob = await response.blob();
      if (gen !== screenView.gen) return;
      if (!(await screenSetPicture(blob, gen))) return;
      screenShowError("");
      screenView.failures = 0;
      screenShowStats();
    } catch (error) {
      if (gen !== screenView.gen) return;
      if (error.name === "AbortError") continue;
      if (error.auth) return;
      screenView.failures += 1;
      screenShowError(error.message);
      screenShowStats("retrying…");
      if (error.fatal) { screenView.running = false; return; }
      delay = Math.min(20000, 2000 * screenView.failures);
    }
    if (!$("#screenAuto").checked) continue;
    await screenSleep(Math.max(250, delay - (performance.now() - began)));
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
  screenView.once = true;
  screenView.wake?.();
}
function screenRestartNow() {
  screenView.once = true;
  screenView.lastStart = -1e9;
  screenView.controller?.abort();
  screenView.wake?.();
}

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
  Object.assign(screenView, { profileID: device.id, paused: false, stamps: [], failures: 0, lastStart: -1e9, running: true, once: true });
  $("#screenSub").textContent = `${device.name} · ${device.host}`;
  for (const [key, select, fallback] of [["screen.mode", "#screenMode", "all"], ["screen.width", "#screenWidth", "720"]]) {
    const saved = String(store.get(key, fallback));
    $(select).value = [...$(select).options].some((o) => o.value === saved) ? saved : fallback;
  }
  $("#screenAuto").checked = store.get("screen.auto", false) === true;
  $("#screenSpeed").value = String(Math.min(3600, Math.max(1, Number(store.get("screen.seconds", 30)) || 30)));
  $("#screenSpeed").disabled = !$("#screenAuto").checked;
  setPauseLabel(false);
  setRemoteOpen(store.get("remote.open", true) !== false);
  setChannelsOpen(store.get("channels.open", true) !== false);
  remoteError("");
  $("#screenSave").disabled = true;
  $("#screenWait").hidden = false;
  $("#screenWait").textContent = "Waiting for the first picture…";
  screenShowError("");
  $("#screenStats").textContent = "";
  screenLoop(screenView.gen);
  channelsEnter();
}

for (const [select, key] of [["#screenMode", "screen.mode"], ["#screenWidth", "screen.width"]]) {
  $(select).addEventListener("change", () => { store.set(key, $(select).value); screenRestartNow(); });
}
$("#screenSpeed").addEventListener("change", () => {
  const seconds = Math.min(3600, Math.max(1, Math.round(Number($("#screenSpeed").value)) || 30));
  $("#screenSpeed").value = String(seconds);
  store.set("screen.seconds", seconds);
  screenRestartNow();
});
$("#screenAuto").addEventListener("change", () => {
  const on = $("#screenAuto").checked;
  store.set("screen.auto", on);
  $("#screenSpeed").disabled = !on;
  if (on) screenRestartNow(); else screenShowStats();
});
$("#screenRefresh").addEventListener("click", () => screenRestartNow());
$("#screenPause").addEventListener("click", () => {
  screenView.paused = !screenView.paused;
  setPauseLabel(screenView.paused);
  if (screenView.paused) screenShowStats("paused"); else screenRestartNow();
});
$("#screenSave").addEventListener("click", () => {
  if (!screenView.blob) return;
  const d = new Date();
  downloadBlob(screenView.blob, `screen-${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}-${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}.jpg`);
});
$("#screenImg").addEventListener("load", () => { $("#screenSave").disabled = false; });
document.addEventListener("visibilitychange", () => { if (!document.hidden && view.current === "screen") screenRestart(); });
