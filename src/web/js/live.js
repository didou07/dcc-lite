"use strict";

const liveNative = typeof window.dccLive === "function";
const liveGui = typeof window.__dccGui === "string";
const liveChoice = (value, allowed, fallback) => (allowed.includes(value) ? value : fallback);
const live = {
  on: false, ref: "", name: "", state: "idle", hasPicture: false, paused: false,
  volume: Math.min(100, Math.max(0, Number(store.get("live.volume", 100)))), muted: store.get("live.muted", false) === true,
  audio: [], audioCur: -1, audioLang: store.get("live.audioLang", ""), video: null,
  retries: 0, timer: 0, pingTimer: 0, ecmTimer: 0, ecmGen: 0, retryTimer: 0, stableTimer: 0, followSince: 0, polling: false,
  popup: false, boundsKey: "", ticks: 0, syncBusy: false, syncDirty: false,
  cw: [], cwKey: "", cwHistory: [], cwChangedAt: 0, cwRenderKey: "", ecmRaw: "", rx: [],
  deint: liveChoice(store.get("live.deint", "auto"), ["auto", "yadif", "off"], "auto"),
  caching: liveChoice(Number(store.get("live.caching", 1500)), [500, 1500, 3000], 1500),
  hw: store.get("live.hw", true) !== false, settingsDirty: false, popKind: "",
  fit: liveChoice(store.get("live.fit", "fit"), ["fit", "fill", "stretch", "16:9", "4:3"], "fit"),
  panelOn: store.get("live.panel", true) !== false,
  fs: false, fsTimer: 0,
  cover: false,
  waitSince: 0, waitTimer: 0,
};
const liveIsOn = () => live.on;
const liveChannelName = () => $("#nowChannel").textContent || live.name || "channel";

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

const liveBusyStates = ["connecting", "buffering", "reconnecting"];
function liveWaitTick() {
  const node = $("#liveWait");
  if (!live.on || !liveBusyStates.includes(live.state)) { liveWaitStop(); return; }
  const seconds = Math.floor((Date.now() - live.waitSince) / 1000);
  node.hidden = seconds < 4;
  node.textContent = `${seconds} s`;
  if (live.state === "buffering" && !live.cover && seconds >= 2) { live.cover = true; liveSyncNative(); }
}
function liveWaitStop() {
  window.clearInterval(live.waitTimer); live.waitTimer = 0;
  const node = $("#liveWait"); node.hidden = true; node.textContent = "";
}
function liveSetState(state, message = "", actions = false) {
  live.state = state;
  $("#liveBadge").dataset.state = state;
  $("#liveCenter").dataset.state = state;
  const busy = liveBusyStates.includes(state);
  if (busy && !live.waitTimer) { live.waitSince = Date.now(); live.waitTimer = window.setInterval(liveWaitTick, 1000); }
  else if (!busy) liveWaitStop();
  if (state !== "buffering") live.cover = false;
  $("#liveCenter").hidden = !(busy || state === "error");
  $("#liveSpin").toggleAttribute("hidden", !busy); // an SVG element has no .hidden property
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

function liveBox() {
  const r = $("#liveVideo").getBoundingClientRect();
  let left = r.left, top = r.top, right = r.right, bottom = r.bottom;
  if (window.dccLiveGtk && !live.fs) {
    const view = $("#liveVideo").closest(".view");
    if (view) {
      const c = view.getBoundingClientRect();
      left = Math.max(left, c.left); top = Math.max(top, c.top); right = Math.min(right, c.right); bottom = Math.min(bottom, c.bottom);
    }
  }
  return { x: left, y: top, w: Math.max(0, right - left), h: Math.max(0, bottom - top), vw: window.innerWidth, vh: window.innerHeight };
}
function liveMayShow() {
  return live.on && live.hasPicture && (live.state === "playing" || live.state === "buffering" || live.state === "paused")
    && !live.cover && !live.popup && !document.hidden && view.current === "screen" && !document.querySelector("dialog[open]");
}
function liveSyncNative(force = false) {
  if (!liveNative || !live.on) return;
  const box = liveBox(), visible = liveMayShow();
  const key = [visible, Math.round(box.x), Math.round(box.y), Math.round(box.w), Math.round(box.h), box.vw, box.vh].join(",");
  if (!force && key === live.boundsKey) return;
  if (live.syncBusy) { live.syncDirty = true; return; }
  live.boundsKey = key;
  live.syncBusy = true;
  liveCall("bounds", { ...box, visible }).catch(() => { }).finally(() => {
    live.syncBusy = false;
    if (live.syncDirty) { live.syncDirty = false; liveSyncNative(); }
  });
}

function liveShowUI(on) {
  if (on && !live.autoHidRemote && !$("#remotePad").hidden) {
    live.autoHidRemote = true;
    $("#remotePad").hidden = true;
    $("#remoteShow").hidden = false;
  } else if (!on && live.autoHidRemote) {
    live.autoHidRemote = false;
    setRemoteOpen(store.get("remote.open", true) !== false);
  }
  $("#liveOverlay").hidden = !on;
  $("#livePanel").hidden = !on || !live.panelOn;
  liveSyncPanelBtn();
  $("#screenStage").classList.toggle("is-live", on);
  const toggle = $("#liveToggle");
  toggle.setAttribute("aria-pressed", String(on));
  toggle.querySelector("span").textContent = on ? "Stop live" : "Watch live";
  toggle.querySelector("use").setAttribute("href", on ? "#i-stop" : "#i-live");
  for (const id of ["#screenPause", "#screenRefresh"]) $(id).disabled = on;
  $("#screenSave").disabled = !on && !screenView.blob;
  if (on) { $("#screenImg").hidden = true; $("#screenWait").hidden = true; }
  else { $("#screenImg").hidden = !screenView.blob; $("#screenWait").hidden = !!screenView.blob; }
  liveSyncVolume();
  liveSyncPlay();
  liveTabMark();
}
function liveTabMark() {
  const tab = document.querySelector('#mainTabs .tab[data-view="screen"]');
  if (!tab) return;
  tab.classList.toggle("is-live", live.on);
  tab.title = live.on ? "Watching live" : "";
}
function liveResume() {
  if (!live.on) return;
  liveSyncNative(true);
  for (const ms of [120, 450]) window.setTimeout(() => { if (live.on && view.current === "screen") liveSyncNative(true); }, ms);
  liveEcm();
  liveAgo();
}

function liveSyncPanelBtn() {
  const b = $("#livePanelBtn");
  if (!b) return;
  b.setAttribute("aria-pressed", String(live.panelOn));
  b.title = live.panelOn ? "Hide the details panel" : "Show the details panel";
}
function liveTogglePanel() {
  live.panelOn = !live.panelOn;
  store.set("live.panel", live.panelOn);
  $("#livePanel").hidden = !live.on || !live.panelOn;
  liveSyncPanelBtn();
}

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
  live.cover = false;
  live.retries = 0;
  live.audio = [];
  live.tracksKey = "";
  live.boundsKey = "";
  livePopClose();
  if (live.fs) liveFullSet(false);
  if (liveNative && wasOn) liveCall("stop").catch(() => { });
  $("#liveTech").textContent = "";
  $("#liveTracks").replaceChildren();
  $("#liveStreamInfo").replaceChildren();
  delete $("#liveStreamInfo").dataset.key;
  delete $("#liveEcm").dataset.key;
  liveShowUI(false);
  if (wasOn && screenView.running && view.current === "screen") screenRestartNow();
}

function liveStart() {
  const id = screenView.profileID;
  if (!id) { toast("Pick a device first", "error"); return; }
  if (!ch.currentRef) { toast("Pick a channel first"); return; }
  if (live.on) return;
  screenView.controller?.abort();
  live.on = true;
  live.retries = 0;
  live.ref = "";
  liveResetCW();
  liveShowUI(true);
  window.clearInterval(live.timer);
  live.timer = window.setInterval(liveTick, 1000);
  window.clearInterval(live.ecmTimer);
  live.ecmTimer = window.setInterval(liveEcm, 2000);
  liveBegin();
}

async function liveBegin() {
  const id = screenView.profileID;
  if (!id || !live.on) return;
  window.clearTimeout(live.retryTimer);
  const same = live.ref === ch.currentRef;
  live.beginN = (live.beginN || 0) + 1;
  live.ref = ch.currentRef;
  live.name = liveChannelName();
  $("#liveName").textContent = live.name;
  liveTabMark();
  if (!same) {
    liveResetCW(); liveRenderCW();
    live.ecmGen++;
    delete $("#liveEcm").dataset.key;
    $("#liveEcm").replaceChildren(h("p", { class: "muted small live-empty", text: "…" }));
    $("#liveEcm").classList.remove("stale");
    $("#liveEcmStamp").textContent = "";
    window.setTimeout(() => { if (live.on && live.ref === ch.currentRef) liveEcm(); }, 1500);
  }
  live.hasPicture = false;
  live.paused = false;
  liveSyncPlay();
  liveSetState("connecting", "Connecting to the receiver…");
  liveSyncNative(true);
  try {
    await liveCall("play", { profile: id, ref: live.ref, name: live.name, volume: live.volume, muted: live.muted, audioLang: live.audioLang, deinterlace: live.deint, caching: live.caching, hw: live.hw, fit: live.fit, ...liveBox() });
    liveRxAudio();
  } catch (error) {
    if (live.on) liveFail(liveMessage(error));
  }
}

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
    if (now - live.followSince > 300) { live.followSince = 0; live.retries = 0; liveBegin(); return; }
  } else live.followSince = 0;
  liveSyncNative(++live.ticks % 6 === 0);
  liveAgo();
}

const liveCell = (k, v, { wide = false, dim = false } = {}) =>
  h("div", { class: `live-cell${wide ? " wide" : ""}${dim ? " dim" : ""}` }, h("span", { class: "k", text: k }), h("span", { class: "v" }, v));
const liveFieldDim = (v) => /^(0x0+|0)$/i.test(String(v).trim());
const liveQuality = (height) => (height >= 2000 ? "4K" : height >= 1000 ? "FHD" : height >= 700 ? "HD" : height > 0 ? "SD" : "");

const liveFill = (node, key, build) => {
  if (node.dataset.key === key) return false;
  node.dataset.key = key;
  node.replaceChildren(...build());
  return true;
};
const liveRatio = (w, h) => {
  if (!w || !h) return "";
  const r = w / h;
  for (const [name, v] of [["16:9", 16 / 9], ["4:3", 4 / 3], ["21:9", 21 / 9], ["3:2", 1.5], ["1:1", 1]]) if (Math.abs(r - v) < 0.03) return name;
  return r.toFixed(2);
};
const LIVE_STATUS = { playing: "Live", paused: "Paused", buffering: "Buffering", connecting: "Connecting", reconnecting: "Reconnecting", error: "Error", idle: "—" };

function liveRenderStats() {
  const active = live.audio.find((t) => t.id === live.audioCur);
  const parts = [];
  if (live.video) parts.push(`${live.video.w}×${live.video.h}`);
  if (active) parts.push(active.name);
  $("#liveTech").textContent = parts.join(" · ");
  const quality = live.video ? liveQuality(live.video.h) : "";
  const key = [live.video?.w, live.video?.h, active?.name, live.audio.length, live.state].join("|");
  liveFill($("#liveStreamInfo"), key, () => [
    liveCell("Video", live.video ? `${live.video.w}×${live.video.h}` : "Radio", { dim: !live.video }),
    liveCell("Format", live.video ? [quality && h("span", { class: "live-chip ok", text: quality }), liveRatio(live.video.w, live.video.h)] : "—", { dim: !live.video }),
    liveCell("Audio", active ? active.name : live.audio.length ? "…" : "none", { dim: !active }),
    liveCell("Status", LIVE_STATUS[live.state] || "—", { dim: live.state !== "playing" }),
  ]);
  $("#liveTracks").hidden = live.audio.length < 2;
}

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
  live.setPage = "";
  if (live.popup) { live.popup = false; liveSyncNative(); }
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
  live.popup = true;
  liveSyncNative();
  pop.querySelector('[aria-checked="true"]')?.focus();
}

const LIVE_DEINT = [["auto", "Auto", "VLC decides (default)"], ["yadif", "Yadif", "Always smooth interlaced channels"], ["off", "Off", "Never deinterlace"]];
const LIVE_CACHE = [[500, "Low", "500 ms · faster zapping"], [1500, "Normal", "1.5 s"], [3000, "High", "3 s · for a weak network"]];

const LIVE_FIT = [
  ["fit", "Original shape", "The broadcast's own proportions (default)"],
  ["fill", "Fill the window", "Zoom in; the edges are cut off"],
  ["stretch", "Stretch to the window", "Widen the picture until it touches every side"],
  ["16:9", "Force 16:9", "A wide picture"],
  ["4:3", "Force 4:3", "A squarer picture"],
];
const livePopTitle = (text) => h("div", { class: "pop-title", text });
const livePopItem = (checked, label, note, onClick, tip = "") => h("button", {
  type: "button", role: "menuitemradio", "aria-checked": String(checked), title: tip, onClick,
}, h("span", { class: "tick", text: checked ? "✓" : "" }), h("span", { class: "grow" }, h("b", { text: label }), note && h("small", { text: note })));
const livePopAction = (label, onClick, hint = "") => h("button", { type: "button", role: "menuitem", onClick },
  h("span", { class: "tick", "aria-hidden": "true" }), h("span", { class: "grow" }, h("b", { text: label })), hint && h("span", { class: "hint", text: hint }));

function liveSetFit(mode) {
  live.fit = mode;
  store.set("live.fit", mode);
  if (live.on) liveCall("fit", { mode }).catch(() => { });
}
const liveFitParts = (again) => [
  livePopTitle("Video size"),
  ...LIVE_FIT.map(([value, label, note]) => livePopItem(live.fit === value, label, note, () => { liveSetFit(value); again(); })),
];
function liveEngineParts(again) {
  const change = (apply) => { apply(); live.settingsDirty = true; again(); };
  return [
    livePopTitle("Deinterlace"),
    ...LIVE_DEINT.map(([value, label, note]) => livePopItem(live.deint === value, label, note, () => change(() => { live.deint = value; store.set("live.deint", value); }))),
    livePopTitle("Buffering"),
    ...LIVE_CACHE.map(([value, label, note]) => livePopItem(live.caching === value, label, note, () => change(() => { live.caching = value; store.set("live.caching", value); }))),
    livePopTitle("Hardware decoding"),
    livePopItem(live.hw, "On", "Uses the graphics card when it can (default)", () => change(() => { live.hw = true; store.set("live.hw", true); })),
    livePopItem(!live.hw, "Off", "Software decoding", () => change(() => { live.hw = false; store.set("live.hw", false); })),
  ];
}

const livePopRow = (label, value, onClick, toggle = null) => h("button", { type: "button", role: toggle === null ? "menuitem" : "menuitemcheckbox", class: "pop-row", "aria-checked": toggle === null ? null : String(toggle), onClick },
  h("span", { class: "pop-lbl", text: label }),
  toggle === null
    ? h("span", { class: "pop-val" }, h("span", { text: value }), h("i", { class: "pop-chev", "aria-hidden": "true", text: "›" }))
    : h("span", { class: "pop-sw", "aria-hidden": "true" }));

function liveSettingsRender() {
  const pop = $("#livePop");
  if (!pop || live.popKind !== "settings") return;
  const page = live.setPage || "";
  const go = (name) => () => { live.setPage = name; liveSettingsRender(); };
  const back = () => { live.setPage = ""; liveSettingsRender(); };
  const pick = (apply) => () => { apply(); live.setPage = ""; liveSettingsRender(); };
  const change = (apply) => pick(() => { apply(); live.settingsDirty = true; });
  const nameOf = (list, value) => (list.find((x) => x[0] === value) || list[0])[1];
  const sub = (title, items) => [
    h("button", { type: "button", role: "menuitem", class: "pop-back", onClick: back }, h("i", { "aria-hidden": "true", text: "‹" }), h("span", { text: title })),
    ...items,
  ];
  let parts;
  if (page === "fit") {
    parts = sub("Video size", LIVE_FIT.map(([v, l]) => livePopItem(live.fit === v, l, "", pick(() => liveSetFit(v)))));
  } else if (page === "deint") {
    parts = sub("Deinterlace", LIVE_DEINT.map(([v, l]) => livePopItem(live.deint === v, l, "", change(() => { live.deint = v; store.set("live.deint", v); }))));
  } else if (page === "cache") {
    parts = sub("Buffering", LIVE_CACHE.map(([v, l]) => livePopItem(live.caching === v, l, "", change(() => { live.caching = v; store.set("live.caching", v); }))));
  } else if (page === "rx") {
    parts = sub("Receiver audio", live.rx.map((t) => livePopItem(!!t.active, t.description || `Track ${t.index + 1}`, "", async () => {
      try { await post(profileURL(screenView.profileID, "audio"), { index: t.index }); toast("Receiver audio changed"); await liveRxAudio(); } catch (error) { toastError(error); }
      back();
    })));
  } else {
    parts = [
      livePopRow("Video size", nameOf(LIVE_FIT, live.fit), go("fit")),
      livePopRow("Deinterlace", nameOf(LIVE_DEINT, live.deint), go("deint")),
      livePopRow("Buffering", nameOf(LIVE_CACHE, live.caching), go("cache")),
      livePopRow("Hardware decoding", "", change(() => { live.hw = !live.hw; store.set("live.hw", live.hw); }), !!live.hw),
    ];
    if (live.rx.length > 1) {
      const cur = live.rx.find((t) => t.active);
      parts.push(livePopRow("Receiver audio", cur ? (cur.description || `Track ${cur.index + 1}`) : "", go("rx")));
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
  live.setPage = "";
  live.popup = true;
  liveSyncNative();
  liveSettingsRender();
  pop.querySelector('[aria-checked="true"]')?.focus();
}

async function liveFullSet(on) {
  if (on === live.fs) return;
  live.fs = on;
  $("#screenStage").classList.toggle("fs-native", on);
  $("#screenStage").classList.remove("fs-idle");
  liveFsWake();
  $("#liveFull use").setAttribute("href", on ? "#i-collapse" : "#i-expand");
  $("#liveFull").setAttribute("aria-label", on ? "Exit full screen" : "Full screen");
  try { await liveCall("fullscreen", { on }); } catch (error) { live.fs = !on; $("#screenStage").classList.toggle("fs-native", !on); toast(liveMessage(error), "error"); return; }
  for (const ms of [120, 450, 900]) window.setTimeout(() => liveSyncNative(true), ms);
}
function liveFsWake() {
  const stage = $("#screenStage");
  window.clearTimeout(live.fsTimer);
  live.fsTimer = 0;
  if (!live.fs) { stage.classList.remove("fs-idle"); return; }
  if (stage.classList.contains("fs-idle")) { stage.classList.remove("fs-idle"); liveCall("cursor", { hide: false }).catch(() => { }); }
  live.fsTimer = window.setTimeout(() => {
    live.fsTimer = 0;
    if (!live.fs || $("#livePop") || stage.querySelector(".live-bar:hover, .live-top:hover")) { liveFsWake(); return; }
    stage.classList.add("fs-idle");
    liveCall("cursor", { hide: true }).catch(() => { });
  }, 3000);
}
function liveFullToggle() {
  if (!liveNative || !live.on) return false;
  liveFullSet(!live.fs);
  return true;
}
function liveEscape() {
  if (!live.on) return false;
  if ($("#livePop")) { livePopClose(); return true; }
  if (live.fs) { liveFullSet(false); return true; }
  return false;
}
function liveToggleMute() {
  live.muted = !live.muted;
  if (!live.muted && live.volume === 0) live.volume = 50;
  livePushVolume();
}
async function liveSnapshot() {
  if (!live.on || !live.hasPicture) { toast("Nothing to capture yet", "error"); return; }
  try {
    const data = await liveCall("snapshot", { name: liveChannelName() });
    toast(data.path ? `Snapshot saved: ${data.path}` : "Snapshot saved");
  } catch (error) { toast(liveMessage(error), "error"); }
}

function liveCtxPlace() {
  const pop = $("#livePop");
  if (!pop || !live.ctxAt) return;
  const gap = 8, box = pop.getBoundingClientRect();
  pop.style.left = `${Math.max(gap, Math.min(live.ctxAt.x, window.innerWidth - box.width - gap))}px`;
  pop.style.top = `${Math.max(gap, Math.min(live.ctxAt.y, window.innerHeight - box.height - gap))}px`;
}
const liveIcon = (id) => {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "ic"); svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", `#${id}`); svg.append(use);
  return svg;
};
function liveCtxRender() {
  const pop = $("#livePop");
  if (!pop || live.popKind !== "ctx") return;
  const page = live.ctxPage || "";
  const then = (fn) => () => { livePopClose(); fn(); };
  const go = (name) => () => { live.ctxPage = name; liveCtxRender(); };
  const back = () => { live.ctxPage = ""; liveCtxRender(); };
  const sub = (title, items) => [
    h("button", { type: "button", role: "menuitem", class: "pop-back", onClick: back }, h("i", { "aria-hidden": "true", text: "‹" }), h("span", { text: title })),
    ...items,
  ];
  let parts;
  if (page === "fit") {
    parts = sub("Video size", LIVE_FIT.map(([v, l]) => livePopItem(live.fit === v, l, "", () => { liveSetFit(v); back(); })));
  } else if (page === "audio") {
    parts = sub("Audio track", live.audio.map((t) => livePopItem(t.id === live.audioCur, t.name, "", async () => { await liveSelectAudio(t.id); back(); })));
  } else {
    const off = live.muted || live.volume === 0;
    const quick = (icon, label, fn) => h("button", { type: "button", role: "menuitem", class: "pop-q", title: label, "aria-label": label, onClick: then(fn) }, liveIcon(icon));
    const vol = h("input", { type: "range", class: "live-vol pop-vol", min: "0", max: "1", step: "0.02", "aria-label": "Volume" });
    vol.value = String(off ? 0 : live.volume / 100);
    vol.addEventListener("input", () => { live.volume = Math.round(Number(vol.value) * 100); live.muted = live.volume === 0; livePushVolume(); });
    parts = [
      h("div", { class: "pop-quick" },
        quick(live.paused ? "i-play" : "i-pause", live.paused ? "Play" : "Pause", liveTogglePlay),
        quick(off ? "i-volume-off" : "i-volume", off ? "Unmute" : "Mute", liveToggleMute),
        quick("i-camera", "Save a snapshot", liveSnapshot),
        quick(live.fs ? "i-collapse" : "i-expand", live.fs ? "Exit full screen" : "Full screen", liveFullToggle)),
      h("div", { class: "pop-volrow" }, liveIcon("i-volume"), vol),
      livePopRow("Video size", (LIVE_FIT.find((x) => x[0] === live.fit) || LIVE_FIT[0])[1], go("fit")),
    ];
    if (live.audio.length > 1) parts.push(livePopRow("Audio track", (live.audio.find((t) => t.id === live.audioCur) || {}).name || "", go("audio")));
    if (typeof liveOsdToggle === "function" && !$("#liveOsdBtn").hidden) parts.push(livePopRow("OSD", "", () => { liveOsdToggle(); liveCtxRender(); }, !!(liveOsd.on && !liveOsd.blocked)));
    parts.push(h("div", { class: "pop-sep", role: "separator" }),
      livePopRow("Player settings", "", then(liveSettingsMenu)),
      h("button", { type: "button", role: "menuitem", class: "pop-row pop-danger", onClick: then(liveStop) }, h("span", { class: "pop-lbl", text: "Stop watching" }), h("span", { class: "pop-val" }, h("span", { text: "L" }))));
  }
  const keep = pop.scrollTop;
  pop.replaceChildren(...parts);
  pop.scrollTop = keep;
  liveCtxPlace();
}
function liveContextMenu(x, y) {
  if ($("#livePop")) livePopClose();
  const pop = h("div", { class: "live-pop live-ctx", id: "livePop", role: "menu", "aria-label": "Video options" });
  pop.addEventListener("contextmenu", (event) => event.preventDefault());
  $("#screenStage").append(pop);
  live.popKind = "ctx";
  live.ctxPage = "";
  live.ctxAt = { x, y };
  live.popup = true;
  liveSyncNative();
  liveCtxRender();
  pop.querySelector('[aria-checked="true"]')?.focus({ preventScroll: true });
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

const liveBytes = (hex) => hex.split(" ").map((x) => parseInt(x, 16));
function liveChecksum(hex) {
  const b = liveBytes(hex);
  return b.length === 8 && ((b[0] + b[1] + b[2]) & 255) === b[3] && ((b[4] + b[5] + b[6]) & 255) === b[7];
}
const liveZero = (hex) => liveBytes(hex).every((x) => x === 0);

async function liveEcm() {
  const id = screenView.profileID, gen = ++live.ecmGen, ref = live.ref;
  if (!live.on || !id || document.hidden || view.current !== "screen") return; // the details are not on show
  try {
    const data = await api(profileURL(id, "live/ecm"));
    if (gen !== live.ecmGen || !live.on || ref !== live.ref) return; // an answer for the channel we just left
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
    liveFill($("#liveEcm"), JSON.stringify([data.available, fields]), () => {
      const cells = fields.map((p) => liveCell(p.k, p.v, { wide: String(p.v).length > 14, dim: liveFieldDim(p.v) }));
      return cells.length ? cells : [h("p", { class: "muted small live-empty", text: data.available ? "No details" : "No card information" })];
    });
    $("#liveEcm").classList.remove("stale");
    $("#liveEcmStamp").textContent = new Date().toLocaleTimeString((window.DCC_I18N?.locale() || "en-GB"));
    $("#liveEcmStamp").title = "Last update";
  } catch (error) {
    if (gen !== live.ecmGen || ref !== live.ref) return;
    $("#liveEcm").classList.add("stale"); // keep the last values, shown dimmed
    $("#liveEcmStamp").textContent = "not answering";
  }
}
function liveRenderCW() {
  const box = $("#liveCw"), more = $("#liveCwMore"), ago = $("#liveCwAgo"), state = $("#liveCwState");
  if (!live.cw.length) {
    box.replaceChildren(h("p", { class: "muted small live-empty", text: "No control words" }), ...(live.ecmRaw ? [h("details", { class: "live-raw" }, h("summary", { text: "ecm.info" }), h("pre", { text: live.ecmRaw }))] : []));
    $("#liveCwHistory").replaceChildren();
    more.hidden = true;
    ago.textContent = "";
    state.hidden = true;
    $("#liveCwCopy").disabled = true;
    return;
  }
  $("#liveCwCopy").disabled = false;
  const zero = live.cw.every((w) => liveZero(w.hex)), bad = live.cw.some((w) => !liveZero(w.hex) && !liveChecksum(w.hex));
  state.hidden = false;
  state.className = `live-chip ${zero || bad ? "bad" : "ok"}`;
  state.textContent = zero ? "Empty" : bad ? "Checksum ≠" : "✓ Checksum";
  state.title = zero ? "All zero: the channel is not being decoded"
    : bad ? "A checksum does not match the standard DVB checksum (bytes 4 and 8)"
    : "Checksum OK: bytes 4 and 8 (highlighted) match the standard DVB checksum";
  box.replaceChildren(...live.cw.map((w) => {
    const bytes = w.hex.split(" ");
    const named = /^(.*?)\s*\((.*)\)\s*$/.exec(w.name);
    const title = named ? named[1] : w.name.split(" ")[0], kind = named ? named[2] : "";
    const group = (from) => h("span", { class: "grp" }, bytes.slice(from, from + 4).map((b, i) => h("span", { class: i === 3 ? "sum" : "", text: b })));
    const rowBad = !liveZero(w.hex) && !liveChecksum(w.hex);
    return h("div", { class: `live-cw${rowBad ? " bad" : ""}`, dataset: { name: w.name } },
      h("div", { class: "live-cw-name" }, h("b", { text: title }), kind && h("span", { class: "live-kind", text: kind })),
      h("div", { class: "live-hex", role: "text", "aria-label": w.hex }, group(0), group(4)));
  }));
  const changes = live.cwHistory.filter((e) => !e.first);
  const rows = changes.map((e, i) => h("li", {},
    h("time", { text: new Date(e.at).toLocaleTimeString((window.DCC_I18N?.locale() || "en-GB")) }),
    h("span", { text: e.words.map((w) => w.name.split(" ")[0]).join(" + ") }),
    i > 0 && e.words.map((w) => h("code", { text: w.hex }))));
  const times = changes.map((e) => e.at);
  const gaps = times.slice(0, -1).map((t, i) => (t - times[i + 1]) / 1000);
  $("#liveCwHistory").replaceChildren(...rows);
  $("#liveCwCount").textContent = gaps.length >= 2 ? `≈ ${Math.max(1, Math.round(gaps.reduce((a, b) => a + b, 0) / gaps.length))} s` : rows.length > 1 ? String(rows.length) : "";
  $("#liveCwCount").title = gaps.length >= 2 ? "A new key arrives about this often" : "";
  more.hidden = rows.length < 2; // a single change is already visible above
  liveAgo();
}
function liveAgo() {
  const node = $("#liveCwAgo");
  if (!node || !live.cwChangedAt || !live.cw.length) return;
  const seconds = Math.max(0, Math.round((Date.now() - live.cwChangedAt) / 1000));
  node.textContent = seconds < 1 ? "now" : seconds < 100 ? `${seconds} s` : `${Math.floor(seconds / 60)} min`;
  node.title = seconds < 1 ? "The key has just changed" : "Time since the last control word change";
}
$("#liveCwCopy").addEventListener("click", async () => {
  if (!live.cw.length) { toast("No control words to copy", "error"); return; }
  try { await navigator.clipboard.writeText(live.cw.map((w) => `${w.name.split(" ")[0]}: ${w.hex}`).join("\n")); toast("Control words copied"); } catch { toast("Could not copy", "error"); }
});


function liveSyncPlay() {
  $("#livePlay").setAttribute("aria-label", live.paused ? "Play" : "Pause");
  $("#livePlay use").setAttribute("href", live.paused ? "#i-play" : "#i-pause");
}
function liveSyncVolume() {
  $("#liveVol").value = String(live.muted ? 0 : live.volume / 100);
  $("#liveVolPct").textContent = `${live.muted ? 0 : live.volume}%`;
  const off = live.muted || live.volume === 0;
  $("#liveMute use").setAttribute("href", off ? "#i-volume-off" : "#i-volume");
  $("#liveMute").setAttribute("aria-label", off ? "Unmute" : "Mute");
}
async function liveTogglePlay() {
  if (!live.on || !live.hasPicture) return;
  const paused = !live.paused;
  try { await liveCall("pause", { paused }); live.paused = paused; liveSyncPlay(); } catch (error) { toast(liveMessage(error), "error"); }
}
function liveVolStep(dir) {
  if (!live.on) return;
  const base = live.muted ? 0 : live.volume;
  live.volume = Math.min(100, Math.max(0, base + dir * 5));
  live.muted = live.volume === 0;
  livePushVolume();
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
  const once = (kind) => { const now = Date.now(); if (now - (live.last?.[kind] || 0) < 700) return false; (live.last ||= {})[kind] = now; return true; };
  window.dccLiveMouse = (kind, x, y) => {
    if (!live.on || view.current !== "screen") return;
    if (kind === "dbl") { if (once("dbl")) liveFullToggle(); }
    else if (kind === "menu") { if (once("menu")) liveContextMenu(x, y); }
    else if (kind === "move") liveFsWake();
    else if (kind === "wheel") liveVolStep(y > 0 ? -1 : 1);
  };
  let mouseBusy = false;
  window.setInterval(async () => {
    if (!live.on || mouseBusy) return;
    mouseBusy = true;
    try {
      const data = await liveCall("mouse");
      for (const e of data.events || []) window.dccLiveMouse(e.k, e.x, e.y);
    } catch (error) { /* the next round asks again */ } finally { mouseBusy = false; }
  }, 100);
  for (const type of ["mousemove", "mousedown", "keydown", "wheel"]) $("#screenStage").addEventListener(type, () => { if (live.fs) liveFsWake(); }, { passive: true });
  $("#liveVideo").addEventListener("dblclick", (event) => { if (!live.on || event.target.closest("button")) return; event.preventDefault(); if (once("dbl")) liveFullToggle(); });
  $("#liveVideo").addEventListener("contextmenu", (event) => { if (!live.on) return; event.preventDefault(); if (once("menu")) liveContextMenu(event.clientX, event.clientY); });
  $("#liveRetry").addEventListener("click", () => { live.retries = 0; liveBegin(); });
  $("#liveAudioBtn").addEventListener("click", (event) => { event.stopPropagation(); liveAudioMenu(); });
  $("#liveSettingsBtn").addEventListener("click", (event) => { event.stopPropagation(); liveSettingsMenu(); });
  $("#liveMute").addEventListener("click", liveToggleMute);
  $("#liveVol").addEventListener("input", (event) => {
    live.volume = Math.round(Number(event.target.value) * 100);
    live.muted = live.volume === 0;
    livePushVolume();
  });
  for (const sel of ["#liveVideo", "#liveVol", "#liveMute"]) $(sel).addEventListener("wheel", (event) => { if (!live.on) return; event.preventDefault(); liveVolStep(event.deltaY > 0 ? -1 : 1); }, { passive: false });
  $("#liveSnap").addEventListener("click", liveSnapshot);
  $("#livePanelBtn").addEventListener("click", liveTogglePanel);
  $("#livePanelClose").addEventListener("click", liveTogglePanel);

  new ResizeObserver(() => liveSyncNative()).observe($("#liveVideo"));
  window.addEventListener("resize", () => liveSyncNative());
  window.addEventListener("scroll", () => liveSyncNative(), true);
  document.addEventListener("visibilitychange", () => liveSyncNative());
  document.addEventListener("fullscreenchange", () => {
    $("#liveFull use").setAttribute("href", document.fullscreenElement || live.fs ? "#i-collapse" : "#i-expand");
    window.setTimeout(() => liveSyncNative(true), 120);
  });
  document.addEventListener("click", (event) => {
    const path = event.composedPath();
    if (path.some((el) => el.id === "livePop" || el.id === "liveAudioBtn" || el.id === "liveSettingsBtn")) return;
    livePopClose();
  });
  document.addEventListener("keydown", (event) => {
    if (view.current !== "screen" || event.ctrlKey || event.metaKey || event.altKey || event.defaultPrevented) return;
    if (document.querySelector("dialog[open]")) return;
    const target = event.target;
    if (target.closest?.("input, select, textarea, #channelPane, [contenteditable]")) return;
    if (event.key === "Escape" && liveEscape()) { event.preventDefault(); return; }
    if (event.key.toLowerCase() === "l") { event.preventDefault(); $("#liveToggle").click(); return; }
    if (event.key === " " && live.on && !target.closest?.("button, a")) { event.preventDefault(); liveTogglePlay(); }
  });
}
else if (liveGui) {
  const why = `The player is not connected to this page. Restart DCC Lite.`;
  console.error(why);
  $("#liveToggle").hidden = false;
  $("#liveToggle").addEventListener("click", () => toast(why, "error"));
}
