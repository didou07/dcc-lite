"use strict";
// Live TV (Windows app only): VLC draws in a native window over #liveVideo; see window.dccLive.

const liveNative = typeof window.dccLive === "function";
const liveGui = typeof window.__dccGui === "string"; // running inside the DCC Lite window, bridge or not
const liveChoice = (value, allowed, fallback) => (allowed.includes(value) ? value : fallback);
const live = {
  on: false, ref: "", name: "", state: "idle", hasPicture: false, paused: false,
  volume: Math.min(100, Math.max(0, Number(store.get("live.volume", 100)))), muted: store.get("live.muted", false) === true,
  audio: [], audioCur: -1, audioLang: store.get("live.audioLang", ""), video: null,
  retries: 0, timer: 0, pingTimer: 0, ecmTimer: 0, ecmGen: 0, retryTimer: 0, stableTimer: 0, followSince: 0, polling: false,
  popup: false, boundsKey: "", ticks: 0, syncBusy: false, syncDirty: false,
  cw: [], cwKey: "", cwHistory: [], cwChangedAt: 0, cwRenderKey: "", ecmRaw: "", rx: [],
  // The one place for player settings (the gear menu). They apply from the next start of a channel.
  deint: liveChoice(store.get("live.deint", "auto"), ["auto", "yadif", "off"], "auto"),
  caching: liveChoice(Number(store.get("live.caching", 1500)), [500, 1500, 3000], 1500),
  hw: store.get("live.hw", true) !== false, settingsDirty: false, popKind: "",
};
const liveIsOn = () => live.on;
const liveChannelName = () => $("#nowChannel").textContent || live.name || "channel";

// A call that never comes back (the window was busy in a drag, say) must not wedge the polling.
async function liveCall(op, args = {}) {
  let timer = 0;
  const timeout = new Promise((_, reject) => { timer = window.setTimeout(() => reject(new Error("The player did not answer")), 4000); });
  try {
    const text = await Promise.race([window.dccLive(op, JSON.stringify(args)), timeout]);
    return text ? JSON.parse(text) : {};
  } finally { window.clearTimeout(timer); }
}
const liveMessage = (error) => (typeof error === "string" ? error : error?.message) || "The channel could not be played.";
function liveResetCW() { live.cwKey = ""; live.cw = []; live.cwHistory = []; live.cwChangedAt = 0; live.cwRenderKey = ""; live.ecmRaw = ""; }

// ---- State and layout ----

function liveSetState(state, message = "", actions = false) {
  live.state = state;
  $("#liveBadge").dataset.state = state;
  const busy = state === "connecting" || state === "buffering" || state === "reconnecting";
  $("#liveCenter").hidden = !(busy || state === "error");
  $("#liveSpin").hidden = !busy;
  $("#liveMsg").textContent = message;
  $("#liveMsg").classList.toggle("bad", state === "error");
  $("#liveCenterActions").hidden = !actions;
  $("#liveBadge b").textContent = state === "playing" ? "LIVE" : state === "error" ? "ERROR" : state === "idle" ? "LIVE" : "…";
  liveSyncNative();
}
function liveFail(message) {
  window.clearTimeout(live.retryTimer);
  liveCall("stop").catch(() => { });
  live.hasPicture = false;
  liveSetState("error", message, true);
}
function liveReconnect(reason) {
  if (!live.on) return;
  window.clearTimeout(live.retryTimer);
  live.retries += 1;
  if (live.retries > 6) { liveFail(`${reason}. The channel could not be reopened.`); return; }
  liveSetState("reconnecting", `${reason}. Reconnecting…`);
  live.retryTimer = window.setTimeout(() => liveBegin(), Math.min(1200 * live.retries, 6000));
}

// The native window follows the box: position, size and whether it may be seen at all.
function liveBox() {
  const r = $("#liveVideo").getBoundingClientRect();
  return { x: r.left, y: r.top, w: r.width, h: r.height, vw: window.innerWidth, vh: window.innerHeight };
}
function liveMayShow() {
  return live.on && live.hasPicture && (live.state === "playing" || live.state === "buffering" || live.state === "paused")
    && !live.popup && !document.hidden && view.current === "screen" && !document.querySelector("dialog[open]");
}
function liveSyncNative(force = false) {
  if (!liveNative || !live.on) return;
  const box = liveBox(), visible = liveMayShow();
  const key = [visible, Math.round(box.x), Math.round(box.y), Math.round(box.w), Math.round(box.h), box.vw, box.vh].join(",");
  if (!force && key === live.boundsKey) return;
  // One call at a time; changes made meanwhile are sent once, with the newest box.
  if (live.syncBusy) { live.syncDirty = true; return; }
  live.boundsKey = key;
  live.syncBusy = true;
  liveCall("bounds", { ...box, visible }).catch(() => { }).finally(() => {
    live.syncBusy = false;
    if (live.syncDirty) { live.syncDirty = false; liveSyncNative(); }
  });
}

function liveShowUI(on) {
  // The remote makes way for the picture while watching live (the Remote button brings it back).
  if (on && !live.autoHidRemote && !$("#remotePad").hidden) {
    live.autoHidRemote = true;
    $("#remotePad").hidden = true;
    $("#remoteShow").hidden = false;
  } else if (!on && live.autoHidRemote) {
    live.autoHidRemote = false;
    setRemoteOpen(store.get("remote.open", true) !== false);
  }
  $("#liveOverlay").hidden = !on;
  $("#livePanel").hidden = !on;
  $("#screenStage").classList.toggle("is-live", on);
  const toggle = $("#liveToggle");
  toggle.setAttribute("aria-pressed", String(on));
  toggle.querySelector("span").textContent = on ? "Stop live" : "Watch live";
  toggle.querySelector("use").setAttribute("href", on ? "#i-stop" : "#i-live");
  for (const id of ["#screenPause", "#screenRefresh"]) $(id).disabled = on;
  $("#screenSave").disabled = on || !screenView.blob;
  if (on) { $("#screenImg").hidden = true; $("#screenWait").hidden = true; }
  else { $("#screenImg").hidden = !screenView.blob; $("#screenWait").hidden = !!screenView.blob; }
  liveSyncVolume();
  liveSyncPlay();
}

// ---- Starting and stopping ----

function liveStop() {
  const wasOn = live.on;
  window.clearTimeout(live.retryTimer);
  window.clearInterval(live.timer);
  window.clearInterval(live.ecmTimer);
  window.clearInterval(live.pingTimer);
  live.timer = live.ecmTimer = live.pingTimer = 0;
  live.ecmGen++;
  live.on = false;
  live.ref = "";
  live.hasPicture = false;
  live.retries = 0;
  live.audio = [];
  live.boundsKey = "";
  livePopClose();
  if (liveNative && wasOn) liveCall("stop").catch(() => { });
  $("#liveTech").textContent = "";
  $("#liveTracks").replaceChildren();
  $("#liveStreamInfo").replaceChildren();
  liveShowUI(false);
  if (wasOn && screenView.running && view.current === "screen") screenRestartNow();
}

function liveStart() {
  const id = screenView.profileID;
  if (!id) { toast("Pick a device first", "error"); return; }
  if (!ch.currentRef) { toast("Pick a channel first"); return; }
  if (live.on) return;
  live.on = true;
  live.retries = 0;
  live.ref = "";
  liveResetCW();
  liveShowUI(true);
  window.clearInterval(live.timer);
  live.timer = window.setInterval(liveTick, 1000); // every call crosses into the window thread: keep them few
  window.clearInterval(live.ecmTimer);
  live.ecmTimer = window.setInterval(liveEcm, 2000);
  liveBegin();
}

async function liveBegin() {
  const id = screenView.profileID;
  if (!id || !live.on) return;
  window.clearTimeout(live.retryTimer);
  const same = live.ref === ch.currentRef;
  live.ref = ch.currentRef;
  live.name = liveChannelName();
  $("#liveName").textContent = live.name;
  if (!same) { liveResetCW(); liveRenderCW(); }
  live.hasPicture = false;
  live.paused = false;
  liveSyncPlay();
  liveSetState("connecting", "Connecting to the receiver…");
  liveSyncNative(true);
  try {
    await liveCall("play", { profile: id, ref: live.ref, name: live.name, volume: live.volume, muted: live.muted, audioLang: live.audioLang, deinterlace: live.deint, caching: live.caching, hw: live.hw, ...liveBox() });
    liveRxAudio();
  } catch (error) {
    if (live.on) liveFail(liveMessage(error));
  }
}

// ---- Polling the player ----

function liveApply(s) {
  live.audio = s.audio || [];
  live.audioCur = typeof s.audioCurrent === "number" ? s.audioCurrent : -1;
  live.video = s.width ? { w: s.width, h: s.height } : null;
  switch (s.state) {
    case "playing":
    case "paused":
      live.hasPicture = true;
      live.paused = s.state === "paused";
      liveSyncPlay();
      if (live.state !== "playing" && live.state !== "paused") {
        liveSetState(s.state);
        window.clearTimeout(live.stableTimer);
        live.stableTimer = window.setTimeout(() => { live.retries = 0; }, 8000);
      } else if (live.state !== s.state) liveSetState(s.state);
      break;
    case "buffering":
      liveSetState(live.hasPicture ? "buffering" : "connecting", live.hasPicture ? "Buffering…" : "Connecting to the receiver…");
      break;
    case "opening":
      if (live.state !== "reconnecting") liveSetState("connecting", "Connecting to the receiver…");
      break;
    case "error":
      if (s.error) liveFail(s.error); else liveReconnect("The player stopped");
      break;
    case "ended":
    case "stopped":
      liveReconnect("The stream ended");
      break;
    case "stalled":
      liveReconnect("The picture froze");
      break;
  }
  liveRenderTracks();
  liveRenderStats();
}

async function liveTick() {
  if (!live.on || live.polling) return;
  live.polling = true;
  try {
    const s = await liveCall("state");
    if (live.on && live.state !== "error" && live.state !== "reconnecting") liveApply(s);
  } catch { /* the next tick tries again */ }
  finally { live.polling = false; }
  if (!live.on) return;
  const now = performance.now();
  if (ch.currentRef && ch.currentRef !== live.ref && live.state !== "connecting") {
    if (!live.followSince) live.followSince = now;
    if (now - live.followSince > 800) { live.followSince = 0; live.retries = 0; liveBegin(); return; }
  } else live.followSince = 0;
  // Re-raise the picture window every few seconds in case the page window moved above it.
  liveSyncNative(++live.ticks % 6 === 0);
  liveAgo();
}

const liveCell = (k, v, { wide = false, dim = false } = {}) =>
  h("div", { class: `live-cell${wide ? " wide" : ""}${dim ? " dim" : ""}` }, h("span", { class: "k", text: k }), h("span", { class: "v" }, v));
const liveFieldDim = (v) => /^(0x0+|0)$/i.test(String(v).trim());
const liveQuality = (height) => (height >= 2000 ? "4K" : height >= 1000 ? "FHD" : height >= 700 ? "HD" : height > 0 ? "SD" : "");

function liveRenderStats() {
  const active = live.audio.find((t) => t.id === live.audioCur);
  const parts = [];
  if (live.video) parts.push(`${live.video.w}×${live.video.h}`);
  if (active) parts.push(active.name);
  $("#liveTech").textContent = parts.join(" · ");
  const quality = live.video ? liveQuality(live.video.h) : "";
  const choice = live.audio.length > 1;
  $("#liveStreamInfo").replaceChildren(
    liveCell("Video", live.video ? [`${live.video.w}×${live.video.h}`, quality && h("span", { class: "live-chip ok", text: quality })] : "Radio / no video"),
    // One track: a plain line; several: the list below carries it.
    !choice && liveCell("Audio", active ? active.name : live.audio.length ? "…" : "none", { dim: !active }),
  );
  $("#liveTracks").hidden = !choice;
}

// ---- Audio tracks ----

function liveRenderTracks() {
  const tracks = live.audio;
  const list = $("#liveTracks");
  const key = tracks.map((t) => `${t.id}:${t.name}`).join("|") + `#${live.audioCur}`;
  if (key === live.tracksKey) return;
  live.tracksKey = key;
  $("#liveAudioLabel").textContent = "";
  if (!tracks.length) {
    list.replaceChildren();
    $("#liveAudioBtn").hidden = true;
    return;
  }
  $("#liveAudioBtn").hidden = false;
  const active = tracks.find((t) => t.id === live.audioCur);
  $("#liveAudioLabel").textContent = active ? active.name : "";
  list.replaceChildren(...tracks.map((t) => h("button", {
    type: "button", class: "live-track", role: "radio", "aria-checked": String(t.id === live.audioCur),
    onClick: () => liveSelectAudio(t.id),
  }, h("b", { text: t.name }), h("span", { class: "live-tick", "aria-hidden": "true", text: t.id === live.audioCur ? "✓" : "" }))));
}
async function liveSelectAudio(id) {
  if (!live.on || id === live.audioCur) return;
  try {
    await liveCall("audio", { id });
    live.audioCur = id;
    const track = live.audio.find((t) => t.id === id);
    live.audioLang = (track?.name || "").toLowerCase();
    store.set("live.audioLang", live.audioLang);
    live.tracksKey = "";
    liveRenderTracks();
  } catch (error) { toast(liveMessage(error), "error"); }
}
function livePopClose() {
  const pop = $("#livePop");
  if (pop) pop.remove();
  live.popKind = "";
  if (live.popup) { live.popup = false; liveSyncNative(); }
  // Settings that VLC reads when a channel opens take effect once the menu is closed.
  if (live.settingsDirty) {
    live.settingsDirty = false;
    if (live.on) { live.retries = 0; liveBegin(); }
  }
}
function liveAudioMenu() {
  if ($("#livePop")) { const was = live.popKind; livePopClose(); if (was === "audio") return; }
  if (!live.audio.length) return;
  const pop = h("div", { class: "live-pop", id: "livePop", role: "menu", "aria-label": "Audio track" },
    ...live.audio.map((t) => h("button", {
      type: "button", role: "menuitemradio", "aria-checked": String(t.id === live.audioCur),
      onClick: () => { livePopClose(); liveSelectAudio(t.id); },
    }, h("span", { class: "tick", text: t.id === live.audioCur ? "✓" : "" }), h("span", { class: "grow" }, h("b", { text: t.name })))));
  $("#liveBar").append(pop);
  live.popKind = "audio";
  live.popup = true;               // the menu would open under VLC's window, so the picture steps aside
  liveSyncNative();
  pop.querySelector('[aria-checked="true"]')?.focus();
}
// ---- Player settings: the one menu where they all live ----

const LIVE_DEINT = [["auto", "Auto", "VLC decides (default)"], ["yadif", "Yadif", "Always smooth interlaced channels"], ["off", "Off", "Never deinterlace"]];
const LIVE_CACHE = [[500, "Low", "500 ms · faster zapping"], [1500, "Normal", "1.5 s"], [3000, "High", "3 s · for a weak network"]];

function liveSettingsRender() {
  const pop = $("#livePop");
  if (!pop || live.popKind !== "settings") return;
  const title = (text) => h("div", { class: "pop-title", text });
  const item = (checked, label, note, onClick, tip = "") => h("button", {
    type: "button", role: "menuitemradio", "aria-checked": String(checked), title: tip, onClick,
  }, h("span", { class: "tick", text: checked ? "✓" : "" }), h("span", { class: "grow" }, h("b", { text: label }), note && h("small", { text: note })));
  const change = (apply) => { apply(); live.settingsDirty = true; liveSettingsRender(); };
  const parts = [
    title("Deinterlace"),
    ...LIVE_DEINT.map(([value, label, note]) => item(live.deint === value, label, note, () => change(() => { live.deint = value; store.set("live.deint", value); }))),
    title("Buffering"),
    ...LIVE_CACHE.map(([value, label, note]) => item(live.caching === value, label, note, () => change(() => { live.caching = value; store.set("live.caching", value); }))),
    title("Hardware decoding"),
    item(live.hw, "On", "Uses the graphics card when it can (default)", () => change(() => { live.hw = true; store.set("live.hw", true); })),
    item(!live.hw, "Off", "Software decoding", () => change(() => { live.hw = false; store.set("live.hw", false); })),
  ];
  if (live.rx.length > 1) {
    parts.push(title("Receiver audio"));
    for (const t of live.rx) {
      parts.push(item(!!t.active, t.description || `Track ${t.index + 1}`, "", async () => {
        try { await post(profileURL(screenView.profileID, "audio"), { index: t.index }); toast("Receiver audio changed"); liveRxAudio(); } catch (error) { toastError(error); }
      }, "Changes the audio on the receiver / TV, not in this player"));
    }
  }
  const keep = pop.scrollTop;
  pop.replaceChildren(...parts);
  pop.scrollTop = keep;
}
function liveSettingsMenu() {
  if ($("#livePop")) { const was = live.popKind; livePopClose(); if (was === "settings") return; }
  const pop = h("div", { class: "live-pop live-pop-settings", id: "livePop", role: "menu", "aria-label": "Player settings" });
  $("#liveBar").append(pop);
  live.popKind = "settings";
  live.popup = true;               // like the audio menu: the picture steps aside while it is open
  liveSyncNative();
  liveSettingsRender();
  pop.querySelector('[aria-checked="true"]')?.focus();
}

async function liveRxAudio() {
  const id = screenView.profileID, ref = live.ref;
  try {
    const data = await api(profileURL(id, "audio"));
    if (!live.on || ref !== live.ref) return;
    live.rx = data.tracks || [];
  } catch { live.rx = []; }
  if (live.popKind === "settings") liveSettingsRender();
}

// ---- Control words and card details ----

const liveBytes = (hex) => hex.split(" ").map((x) => parseInt(x, 16));
function liveChecksum(hex) {
  const b = liveBytes(hex);
  return b.length === 8 && ((b[0] + b[1] + b[2]) & 255) === b[3] && ((b[4] + b[5] + b[6]) & 255) === b[7];
}
const liveZero = (hex) => liveBytes(hex).every((x) => x === 0);

async function liveEcm() {
  const id = screenView.profileID, gen = ++live.ecmGen;
  if (!live.on || !id || document.hidden) return;
  try {
    const data = await api(profileURL(id, "live/ecm"));
    if (gen !== live.ecmGen || !live.on) return;
    const words = data.cw || [];
    live.ecmRaw = data.raw || "";
    const key = words.map((w) => w.hex).join("|");
    if (key !== live.cwKey) {
      const before = live.cw;
      const changed = words.filter((w, i) => !before[i] || before[i].hex !== w.hex);
      live.cw = words;
      live.cwKey = key;
      live.cwChangedAt = Date.now();
      if (key && changed.length) live.cwHistory.unshift({ at: Date.now(), first: !before.length, words: changed });
      live.cwHistory = live.cwHistory.slice(0, 8);
    }
    const renderKey = `${key}|${live.ecmRaw}`;
    if (renderKey !== live.cwRenderKey) { live.cwRenderKey = renderKey; liveRenderCW(); }
    const fields = (data.fields || []).slice().sort((a, b) => liveFieldDim(a.v) - liveFieldDim(b.v));
    const cells = fields.map((p) => liveCell(p.k, p.v, { wide: String(p.v).length > 14, dim: liveFieldDim(p.v) }));
    $("#liveEcm").replaceChildren(...(cells.length ? cells : [h("p", { class: "muted small live-empty", text: data.available ? "No details" : "No card information" })]));
    $("#liveEcmStamp").textContent = new Date().toLocaleTimeString((window.DCC_I18N?.locale() || "en-GB"));
  } catch (error) {
    if (gen !== live.ecmGen) return;
    $("#liveEcmStamp").textContent = "not answering";
  }
}
function liveRenderCW() {
  const box = $("#liveCw"), more = $("#liveCwMore"), ago = $("#liveCwAgo");
  if (!live.cw.length) {
    box.replaceChildren(h("p", { class: "muted small live-empty", text: "No control words" }), ...(live.ecmRaw ? [h("details", { class: "live-raw" }, h("summary", { text: "ecm.info" }), h("pre", { text: live.ecmRaw }))] : []));
    $("#liveCwHistory").replaceChildren();
    more.hidden = true;
    ago.textContent = "";
    return;
  }
  box.replaceChildren(...live.cw.map((w) => {
    const ok = liveChecksum(w.hex), zero = liveZero(w.hex);
    const bytes = w.hex.split(" ");
    const named = /^(.*?)\s*\((.*)\)\s*$/.exec(w.name);
    const title = named ? named[1] : w.name.split(" ")[0], kind = named ? named[2] : "";
    const group = (from) => h("span", { class: "grp" }, bytes.slice(from, from + 4).map((b, i) => h("span", { class: i === 3 ? "sum" : "", text: b })));
    const tip = zero ? "All zero: the channel is not being decoded" : ok ? "Checksum OK: bytes 4 and 8 match the standard DVB checksum" : "Checksum does not match.";
    // One line per word: name, the eight bytes, and a small checksum mark.
    return h("div", { class: "live-cw", dataset: { name: w.name } },
      h("div", { class: "live-cw-name" }, h("b", { text: title }), kind && h("span", { class: "live-kind", text: kind })),
      h("div", { class: "live-hex", role: "text", "aria-label": w.hex }, group(0), group(4)),
      h("span", { class: `live-chip ${zero ? "bad" : ok ? "ok" : ""}`, title: tip }, zero ? "empty" : ok ? "✓" : "≠"));
  }));
  // The current words are shown above; history lists earlier ones and starts folded.
  const rows = live.cwHistory.map((e, i) => h("li", {},
    h("time", { text: new Date(e.at).toLocaleTimeString((window.DCC_I18N?.locale() || "en-GB")) }),
    h("span", { text: `${e.first ? "Seen" : "Changed"} ${e.words.map((w) => w.name.split(" ")[0]).join(" + ")}` }),
    i > 0 && e.words.map((w) => h("code", { text: w.hex }))));
  const times = live.cwHistory.filter((e) => !e.first).map((e) => e.at);
  const gaps = times.slice(0, -1).map((t, i) => (t - times[i + 1]) / 1000);
  if (gaps.length >= 2) rows.push(h("li", { class: "live-note" }, `A new key arrives about every ${Math.round(gaps.reduce((a, b) => a + b, 0) / gaps.length)} s`));
  $("#liveCwHistory").replaceChildren(...rows);
  $("#liveCwCount").textContent = rows.length > 1 ? String(live.cwHistory.length) : "";
  more.hidden = !rows.length;
  liveAgo();
}
function liveAgo() {
  const node = $("#liveCwAgo");
  if (!node || !live.cwChangedAt || !live.cw.length) return;
  const seconds = Math.max(0, Math.round((Date.now() - live.cwChangedAt) / 1000));
  node.textContent = seconds < 1 ? "key just changed" : `key ${seconds} s ago`;
  node.title = "Time since the last control word change";
}
$("#liveCwCopy").addEventListener("click", async () => {
  if (!live.cw.length) { toast("No control words to copy", "error"); return; }
  try { await navigator.clipboard.writeText(live.cw.map((w) => `${w.name.split(" ")[0]}: ${w.hex}`).join("\n")); toast("Control words copied"); } catch { toast("Could not copy", "error"); }
});


// ---- Controls ----

function liveSyncPlay() {
  $("#livePlay").setAttribute("aria-label", live.paused ? "Play" : "Pause");
  $("#livePlay use").setAttribute("href", live.paused ? "#i-play" : "#i-pause");
}
function liveSyncVolume() {
  $("#liveVol").value = String(live.muted ? 0 : live.volume / 100);
  const off = live.muted || live.volume === 0;
  $("#liveMute use").setAttribute("href", off ? "#i-volume-off" : "#i-volume");
  $("#liveMute").setAttribute("aria-label", off ? "Unmute" : "Mute");
}
async function liveTogglePlay() {
  if (!live.on || !live.hasPicture) return;
  const paused = !live.paused;
  try { await liveCall("pause", { paused }); live.paused = paused; liveSyncPlay(); } catch (error) { toast(liveMessage(error), "error"); }
}
function livePushVolume() {
  store.set("live.volume", live.volume);
  store.set("live.muted", live.muted);
  liveSyncVolume();
  liveCall("volume", { volume: live.volume, muted: live.muted }).catch(() => { });
}

if (liveNative) {
  $("#liveToggle").hidden = false;
  $("#liveToggle").addEventListener("click", () => { if (live.on) liveStop(); else liveStart(); });
  $("#liveStop").addEventListener("click", liveStop);
  $("#livePlay").addEventListener("click", liveTogglePlay);
  $("#liveFull").addEventListener("click", () => $("#screenFull").click());
  $("#liveRetry").addEventListener("click", () => { live.retries = 0; liveBegin(); });
  $("#liveAudioBtn").addEventListener("click", (event) => { event.stopPropagation(); liveAudioMenu(); });
  $("#liveSettingsBtn").addEventListener("click", (event) => { event.stopPropagation(); liveSettingsMenu(); });
  $("#liveMute").addEventListener("click", () => {
    live.muted = !live.muted;
    if (!live.muted && live.volume === 0) live.volume = 50;
    livePushVolume();
  });
  $("#liveVol").addEventListener("input", (event) => {
    live.volume = Math.round(Number(event.target.value) * 100);
    live.muted = live.volume === 0;
    livePushVolume();
  });
  $("#liveSnap").addEventListener("click", async () => {
    if (!live.on || !live.hasPicture) { toast("Nothing to capture yet", "error"); return; }
    try {
      const data = await liveCall("snapshot", { name: liveChannelName() });
      toast(data.path ? `Snapshot saved: ${data.path}` : "Snapshot saved");
    } catch (error) { toast(liveMessage(error), "error"); }
  });

  // Keep VLC's window on its box: whenever the page moves, resizes or covers it.
  new ResizeObserver(() => liveSyncNative()).observe($("#liveVideo"));
  window.addEventListener("resize", () => liveSyncNative());
  window.addEventListener("scroll", () => liveSyncNative(), true);
  document.addEventListener("visibilitychange", () => liveSyncNative());
  document.addEventListener("fullscreenchange", () => {
    $("#liveFull use").setAttribute("href", document.fullscreenElement ? "#i-collapse" : "#i-expand");
    window.setTimeout(() => liveSyncNative(true), 120);
  });
  document.addEventListener("click", (event) => { if (!event.target.closest("#livePop, #liveAudioBtn, #liveSettingsBtn")) livePopClose(); });
  document.addEventListener("keydown", (event) => {
    if (view.current !== "screen" || event.ctrlKey || event.metaKey || event.altKey || event.defaultPrevented) return;
    if (document.querySelector("dialog[open]")) return;
    const target = event.target;
    if (target.closest?.("input, select, textarea, #channelPane, [contenteditable]")) return;
    if (event.key === "Escape" && $("#livePop")) { livePopClose(); event.preventDefault(); return; }
    if (event.key.toLowerCase() === "l") { event.preventDefault(); $("#liveToggle").click(); return; }
    if (event.key === " " && live.on && !target.closest?.("button, a")) { event.preventDefault(); liveTogglePlay(); }
  });
}
else if (liveGui) {
  // The window is there but the player bridge did not arrive: say why instead of hiding the button.
  const why = `The player is not connected to this page. Restart DCC Lite.`;
  console.error(why);
  $("#liveToggle").hidden = false;
  $("#liveToggle").addEventListener("click", () => toast(why, "error"));
}
