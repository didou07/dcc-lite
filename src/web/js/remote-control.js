"use strict";
const GLYPHS = {
  rewind: '<path d="M11 6v12L3 12zM21 6v12l-8-6z"/>', play: '<path d="M7 5v14l12-7z"/>', pause: '<path d="M7 5h4v14H7zM13 5h4v14h-4z"/>',
  forward: '<path d="M13 6v12l8-6zM3 6v12l8-6z"/>', skipback: '<path d="M6 5h2.5v14H6zM20 5v14L9 12z"/>', record: '<circle cx="12" cy="12" r="6"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="1"/>', skipnext: '<path d="M15.5 5H18v14h-2.5zM4 5v14l11-7z"/>',
  power: '<path d="M11 2h2v10h-2z"/><path d="M6.3 5.6l1.4 1.4a7 7 0 1 0 8.6 0l1.4-1.4A9 9 0 1 1 6.3 5.6z"/>',
  up: '<path d="M12 7l7 10H5z"/>', down: '<path d="M12 17 5 7h14z"/>', left: '<path d="M7 12l10-7v14z"/>', right: '<path d="M17 12 7 19V5z"/>',
};
const K_POWER = [[["power", "@power", "pwr"]]];
const K_NUM = [
  [["1", "1"], ["2", "2"], ["3", "3"]], [["4", "4"], ["5", "5"], ["6", "6"]], [["7", "7"], ["8", "8"], ["9", "9"]],
  [["prev", "<", "nav"], ["0", "0"], ["next", ">", "nav"]],
];
const K_COLORS = [[["red", "", "red"], ["green", "", "green"], ["yellow", "", "yellow"], ["blue", "", "blue"]]];
const K_NAV = (bottomLeft, bottomRight) => [
  [["menu", "Menu"], ["up", "@up", "nav"], ["exit", "Exit"]],
  [["left", "@left", "nav"], ["ok", "OK", "nav ok"], ["right", "@right", "nav"]],
  [bottomLeft, ["down", "@down", "nav"], bottomRight],
];
const K_SPACER = ["", "", "spacer"];
const K_VOLCH = (middleTop, middleBottom) => [
  [["volup", "Vol+", "vol"], middleTop, ["chup", "CH+", "ch"]],
  [["voldown", "Vol-", "vol"], middleBottom, ["chdown", "CH-", "ch"]],
];
const K_TRANSPORT = [
  [["rewind", "@rewind"], ["play", "@play"], ["pause", "@pause"], ["forward", "@forward"]],
  [["skipback", "@skipback"], ["record", "@record", "rec"], ["stop", "@stop"], ["skipnext", "@skipnext"]],
];
const small = (key, label) => [key, label, "small"];
const K_QUICK = [[small("pvr", "PVR"), small("tv", "TV"), small("text", "Text"), small("help", "Help")]];
const MUTE = ["mute", "Mute"], HISTORY = ["history", "History"], INFO = ["info", "Info"];

const REMOTE_LAYOUTS = {
  standard: {
    label: "Standard",
    main: [...K_POWER, ...K_NUM, ...K_COLORS, ...K_NAV(INFO, ["back", "Back"]), ...K_QUICK],
    extra: [...K_VOLCH(MUTE, HISTORY), ...K_TRANSPORT],
    more: [[small("text", "Text"), small("audio", "Audio"), small("video", "Video")], [small("pvr", "PVR"), small("tv", "TV"), small("radio", "Radio"), small("help", "Help")],
      [small("setup", "Setup"), small("aspect", "Aspect"), small("pip", "PiP")], [small("epg", "EPG"), small("subtitle", "Subs"), small("fav", "Fav"), small("timer", "Timer")]],
  },
  dreambox: {
    label: "Dreambox",
    main: [...K_POWER, ...K_NUM, ...K_COLORS, ...K_NAV(INFO, small("help", "Help")), ...K_QUICK],
    extra: [...K_VOLCH(MUTE, HISTORY), ...K_TRANSPORT],
    more: [[small("tv", "TV"), small("radio", "Radio"), small("epg", "EPG")], [small("audio", "Audio"), small("video", "Video"), small("text", "Text")],
      [small("back", "Back"), small("fav", "Fav"), small("subtitle", "Subs")], [small("pvr", "PVR"), small("timer", "Timer"), small("setup", "Setup")]],
  },
  vu: {
    label: "Vu+",
    main: [...K_POWER, ...K_NUM, ...K_COLORS, ...K_NAV(INFO, ["epg", "EPG"]), ...K_QUICK],
    extra: [...K_VOLCH(MUTE, HISTORY), ...K_TRANSPORT],
    more: [[small("tv", "TV"), small("radio", "Radio"), small("fav", "Fav")], [small("audio", "Audio"), small("subtitle", "Subs"), small("text", "Text")],
      [small("pvr", "PVR"), small("timer", "Timer"), small("help", "Help")], [small("video", "Video"), small("setup", "Setup"), small("back", "Back")]],
  },
  gigablue: {
    label: "GigaBlue",
    main: [...K_POWER, ...K_NUM, ...K_COLORS, ...K_NAV(INFO, ["back", "Back"]), ...K_QUICK],
    extra: [...K_VOLCH(MUTE, ["epg", "EPG"]), ...K_TRANSPORT],
    more: [[small("tv", "TV"), small("radio", "Radio"), small("history", "History")], [small("audio", "Audio"), small("subtitle", "Subs"), small("text", "Text")],
      [small("fav", "Fav"), small("pvr", "PVR"), small("timer", "Timer")], [small("video", "Video"), small("help", "Help"), small("setup", "Setup")]],
  },
  compact: {
    label: "Compact",
    main: [...K_POWER, ...K_NUM, ...K_COLORS, ...K_NAV(INFO, ["back", "Back"]), ...K_QUICK],
    extra: [...K_VOLCH(MUTE, HISTORY), ...K_TRANSPORT],
    more: [[small("tv", "TV"), small("radio", "Radio"), small("epg", "EPG"), small("text", "Text")]],
  },
};
const KEY_TITLES = { power: "Power (standby menu, hold for instant standby)", aspect: "Aspect ratio", pip: "Picture in picture", prev: "Previous", next: "Next", volup: "Volume up", voldown: "Volume down", chup: "Channel up", chdown: "Channel down", up: "Up", down: "Down", left: "Left", right: "Right",
  rewind: "Rewind", play: "Play", pause: "Pause", forward: "Fast forward", skipback: "Previous chapter", record: "Record", stop: "Stop", skipnext: "Next chapter", subtitle: "Subtitles", red: "Red", green: "Green", yellow: "Yellow", blue: "Blue" };

const RC_GLYPH = { UP: "up", DOWN: "down", LEFT: "left", RIGHT: "right", REWIND: "rewind", FASTFORWARD: "forward", PLAY: "play", PLAYPAUSE: "play", PAUSE: "pause", STOP: "stop", RECORD: "record", POWER: "power", PREVIOUSSONG: "skipback", NEXTSONG: "skipnext" };
const RC_TEXT = { OK: "OK", PREVIOUS: "<", NEXT: ">", ARROWLEFT: "<", ARROWRIGHT: ">", VOLUMEUP: "Vol+", VOLUMEDOWN: "Vol-", CHANNELUP: "CH+", CHANNELDOWN: "CH-", CONTEXT_MENU: "Menu", EPG: "EPG", PVR: "PVR", TV: "TV", FAV: "Fav", FAVORITES: "Fav", SUBTITLE: "Subs", PAGEUP: "Pg+", PAGEDOWN: "Pg-", LAST: "History" };
const RC_CLASS = { UP: "nav", DOWN: "nav", LEFT: "nav", RIGHT: "nav", OK: "nav ok", RED: "red", GREEN: "green", YELLOW: "yellow", BLUE: "blue", VOLUMEUP: "vol", VOLUMEDOWN: "vol", CHANNELUP: "ch", CHANNELDOWN: "ch" };
const RC_MAIN = new Set(["POWER", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "UP", "DOWN", "LEFT", "RIGHT", "OK", "RED", "GREEN", "YELLOW", "BLUE", "MENU", "EXIT", "INFO", "VOLUMEUP", "VOLUMEDOWN", "CHANNELUP", "CHANNELDOWN", "MUTE"]);
const rcNorm = (value) => String(value || "").toLowerCase().replace(/[^a-z0-9]/g, "");

// Builds a layout from one OE-Alliance remote definition.
function rcBuildLayout(code) {
  const buttons = RC_DB.codes[code];
  if (!buttons) return null;
  const byId = new Map(buttons.map(([id, label, x, y]) => [id, { id, label, x, y }]));
  const used = new Set();
  const key = (id, cls) => {
    const item = byId.get(id);
    if (!item) return null;
    used.add(id);
    const glyph = RC_GLYPH[id];
    const kind = cls ?? (RC_CLASS[id] || (RC_MAIN.has(id) ? "" : "small"));
    const text = glyph ? "@" + glyph : (/^(RED|GREEN|YELLOW|BLUE)$/.test(id) ? "" : (RC_TEXT[id] || item.label));
    return ["KEY_" + id, text, kind];
  };
  const generic = (name, text, cls) => [name, text, cls];
  const firstOf = (...ids) => { for (const id of ids) if (byId.has(id) && !used.has(id)) return id; return null; };
  const row = (...cells) => { const r = cells.filter(Boolean); return r.length ? r : null; };
  const main = [], extra = [], more = [];
  const push = (list, r) => { if (r) list.push(r); };

  push(main, row(key("POWER")));
  if ("0123456789".split("").every((d) => byId.has(d))) {
    for (const r of [["1", "2", "3"], ["4", "5", "6"], ["7", "8", "9"]]) push(main, row(...r.map((d) => key(d))));
    push(main, row(key("PREVIOUS", "nav"), key("0"), key("NEXT", "nav")));
  }
  push(main, row(key("RED"), key("GREEN"), key("YELLOW"), key("BLUE")));
  if (["UP", "DOWN", "LEFT", "RIGHT", "OK"].every((id) => byId.has(id))) {
    const bl = firstOf("INFO", "HELP", "EPG"), br = firstOf("BACK", "EPG", "TV", "AUDIO");
    push(main, row(key("MENU") || [ "", "", "spacer" ], key("UP"), key("EXIT") || ["", "", "spacer"]));
    push(main, row(key("LEFT"), key("OK"), key("RIGHT")));
    push(main, row(bl ? key(bl, "") : ["", "", "spacer"], key("DOWN"), br ? key(br, "") : ["", "", "spacer"]));
  }
  const quick = ["PVR", "TV", "TEXT", "HELP", "AUDIO", "RADIO"].filter((id) => byId.has(id)).slice(0, 4);
  push(main, row(...quick.map((id) => key(id, "small"))));
  // Expanded part starts with the volume / channel keys.
  const vol = (id, name, text, cls) => key(id, cls) || generic(name, text, cls);
  const mid = firstOf("LAST", "EPG", "FAVORITES");
  push(extra, row(vol("VOLUMEUP", "volup", "Vol+", "vol"), key("MUTE", ""), vol("CHANNELUP", "chup", "CH+", "ch")));
  push(extra, row(vol("VOLUMEDOWN", "voldown", "Vol-", "vol"), mid ? key(mid, "") : ["", "", "spacer"], vol("CHANNELDOWN", "chdown", "CH-", "ch")));

  const transport = ["REWIND", "PLAY", "PAUSE", "FASTFORWARD", "PREVIOUS", "RECORD", "STOP", "NEXT"].filter((id) => byId.has(id) && !used.has(id));
  if (transport.length) {
    const list = transport.map((id) => key(id, id === "RECORD" ? "rec" : ""));
    const rows = Math.ceil(list.length / 4);
    for (let i = 0; i < rows; i += 1) push(extra, list.slice(Math.floor(i * list.length / rows), Math.floor((i + 1) * list.length / rows)));
  }
  const rest = [...byId.values()].filter((item) => !used.has(item.id)).sort((a, b) => a.y - b.y || a.x - b.x);
  const chunks = Math.ceil(rest.length / 4);
  for (let i = 0; i < chunks; i += 1) {
    const items = rest.slice(Math.floor(i * rest.length / chunks), Math.floor((i + 1) * rest.length / chunks));
    push(more, items.map((item) => key(item.id, "small")));
  }
  return { label: RC_DB.names[code] || code, main, extra, more, generated: true };
}
function rcLayout(id) {
  if (REMOTE_LAYOUTS[id]) return REMOTE_LAYOUTS[id];
  if (id.startsWith("rc:")) {
    const code = id.slice(3);
    REMOTE_LAYOUTS_CACHE[code] ||= rcBuildLayout(code);
    return REMOTE_LAYOUTS_CACHE[code];
  }
  return null;
}
const REMOTE_LAYOUTS_CACHE = {};
function remoteMatchCodes(info) {
  if (!info || typeof RC_DB === "undefined") return [];
  for (const value of [info.machinebuild, info.boxtype, info.model]) {
    const found = RC_DB.models[rcNorm(value)];
    if (found?.length) return found;
  }
  return [];
}

const rvol = { level: 0, muted: false, timer: null, busy: false };
function remoteVolumeBar() {
  const mute = h("button", { type: "button", class: "rv-mute", title: "Mute", "aria-label": "Mute" });
  mute.innerHTML = '<svg class="ic" aria-hidden="true"><use href="#i-volume"/></svg>';
  const bar = h("div", { class: "remote-vol", id: "remoteVol" }, mute,
    h("input", { type: "range", class: "rv-range", min: "0", max: "100", step: "1", value: String(rvol.level), "aria-label": "Receiver volume" }),
    h("span", { class: "rv-val", text: String(rvol.level) }));
  return bar;
}
function remoteVolumeShow(level, muted) {
  if (Number.isFinite(level)) rvol.level = Math.max(0, Math.min(100, level));
  if (typeof muted === "boolean") rvol.muted = muted;
  const bar = $("#remoteVol");
  if (!bar) return;
  const range = $(".rv-range", bar);
  if (document.activeElement !== range) range.value = String(rvol.level);
  $(".rv-val", bar).textContent = rvol.muted ? "—" : String(rvol.level);
  $(".rv-mute use", bar).setAttribute("href", rvol.muted || rvol.level === 0 ? "#i-volume-off" : "#i-volume");
  bar.classList.toggle("muted", rvol.muted);
}
async function remoteVolumeSync() {
  const id = screenView.profileID;
  if (!id) return;
  try {
    const r = await api(profileURL(id, "volume"));
    if (id === screenView.profileID) { remoteVolumeShow(Number(r.level), r.muted); $("#remoteVol")?.classList.remove("na"); }
  } catch { $("#remoteVol")?.classList.add("na"); }
}
async function remoteVolumeSend(body) {
  const id = screenView.profileID;
  if (!id) return;
  try {
    const r = await post(profileURL(id, "volume"), body);
    remoteError("");
    if (id === screenView.profileID) remoteVolumeShow(Number(r.level), r.muted);
  } catch (error) { remoteError(error.message); }
}
function remoteVolumeBind() {
  const holder = $("#remoteKeys");
  holder.addEventListener("input", (event) => {
    if (!event.target.matches(".rv-range")) return;
    const level = Number(event.target.value);
    remoteVolumeShow(level, false);
    window.clearTimeout(rvol.timer);
    rvol.timer = window.setTimeout(() => remoteVolumeSend({ level }), 150);
  });
  holder.addEventListener("click", (event) => { if (event.target.closest(".rv-mute")) remoteVolumeSend({ mute: true }); });
}

const remote = { queue: Promise.resolve(), pending: 0, hold: null, holdFired: false, family: "standard", gen: 0, id: "" };

function remoteError(message) {
  const node = $("#remoteError");
  node.hidden = !message;
  node.textContent = message || "";
}
function remoteRefreshScreen(profileID) {
  window.clearTimeout(remote.shots);
  remote.shotStep = 0;
  const delays = [500, 1500, 3500];
  const next = () => {
    if (profileID !== screenView.profileID || screenView.paused) return;
    screenRestartNow();
    if (++remote.shotStep < delays.length) remote.shots = window.setTimeout(next, delays[remote.shotStep] - delays[remote.shotStep - 1]);
  };
  remote.shots = window.setTimeout(next, delays[0]);
}
// A remote key may have changed the channel: ask the receiver soon, so the live picture follows at once.
function remoteFollowChannel(profileID) {
  if (typeof live === "undefined" || !live.on) return;
  window.clearTimeout(remote.follow);
  const delays = [350, 1000, 2200, 4000];
  let step = 0;
  const next = () => {
    if (profileID !== screenView.profileID || !live.on) return;
    pollCurrent(true);
    if (++step < delays.length) remote.follow = window.setTimeout(next, delays[step] - delays[step - 1]);
  };
  remote.follow = window.setTimeout(next, delays[0]);
}
const remoteMode = () => (store.get(`remote.mode.${remote.id}`, "web") === "telnet" ? "telnet" : "web");
function sendRemoteKey(key, long = false, button = null, hardware = false) {
  if (!screenView.profileID || remote.pending >= 6) return Promise.resolve();
  remote.pending += 1;
  button?.classList.add("sending");
  const profileID = screenView.profileID;
  remote.queue = remote.queue.then(async () => {
    try {
      await post(profileURL(profileID, "remote-key"), { key, ...(long ? { long: true } : {}), ...(hardware || remoteMode() === "telnet" ? { hardware: true } : {}) });
      remoteError("");
      remoteRefreshScreen(profileID);
      remoteFollowChannel(profileID);
      if (/^(volup|voldown|mute|KEY_(VOLUMEUP|VOLUMEDOWN|MUTE))$/.test(key)) window.setTimeout(remoteVolumeSync, 400);
    } catch (error) { remoteError(error.message); }
    finally { remote.pending -= 1; button?.classList.remove("sending"); }
  });
  return remote.queue;
}
function remoteLayoutName(profileID = remote.id) {
  const chosen = String(store.get(`remote.layout.${profileID}`, "auto"));
  if (chosen !== "auto" && rcLayout(chosen)) return chosen;
  const matched = remoteMatchCodes(remote.info);
  if (matched.length && rcLayout("rc:" + matched[0])) return "rc:" + matched[0];
  return REMOTE_LAYOUTS[remote.family] ? remote.family : "standard";
}
function buildRemote() {
  const holder = $("#remoteKeys");
  const layout = rcLayout(remoteLayoutName());
  holder.replaceChildren();
  const rowClass = (group, row) => `remote-row${row[0][2] === "red" ? " colors" : group === "extra" ? " extra" : group === "more" ? " extra extra2" : ""}`;
  const keyTitle = (key, label) => KEY_TITLES[key] || (label && !label.startsWith("@") ? label : key.replace(/^KEY_/, "").replace(/_/g, " ").toLowerCase());
  const seen = new Set();
  for (const group of ["main", "extra", "more"]) {
    if (group === "extra") holder.append(remoteVolumeBar());
    let rows = layout[group];
    if (group === "more") {
      const keys = rows.flat().filter(([key]) => !seen.has(key));
      const count = Math.ceil(keys.length / 4);
      rows = Array.from({ length: count }, (_, i) => keys.slice(Math.floor(i * keys.length / count), Math.floor((i + 1) * keys.length / count)));
    }
    for (const row0 of rows) {
      const row = row0.filter(([key, , extra]) => extra === "spacer" || !seen.has(key));
      if (!row.length || row.every((k) => k[2] === "spacer")) continue;
      row.forEach(([key, , extra]) => { if (extra !== "spacer") seen.add(key); });
      holder.append(h("div", { class: rowClass(group, row), style: `--cols:${row.length}` }, row.map(([key, label, extra = ""]) => {
        if (extra === "spacer") return h("span", { class: "rk-spacer", "aria-hidden": "true" });
        const button = h("button", { class: `rk ${extra}`.trim(), type: "button", title: keyTitle(key, label), dataset: { key }, "aria-label": keyTitle(key, label) });
        if (label.startsWith("@")) {
          button.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${GLYPHS[label.slice(1)]}</svg>`;
        } else button.textContent = label;
        return button;
      })));
    }
  }
  const select = $("#remoteLayout");
  const chosen = String(store.get(`remote.layout.${remote.id}`, "auto"));
  const matched = remoteMatchCodes(remote.info);
  const autoName = matched.length ? (RC_DB.names[matched[0]] || matched[0]) : (REMOTE_LAYOUTS[remote.family]?.label || "Standard");
  const group = (label, options) => h("optgroup", { label }, options);
  const all = Object.keys(typeof RC_DB === "undefined" ? {} : RC_DB.codes).sort((a, b) => String(RC_DB.names[a]).localeCompare(String(RC_DB.names[b])));
  select.replaceChildren(h("option", { value: "auto" }, `Auto (${autoName})`),
    group("Generic", Object.entries(REMOTE_LAYOUTS).map(([id, item]) => h("option", { value: id }, item.label))),
    ...(all.length ? [group("Receiver remotes", all.map((code) => h("option", { value: "rc:" + code }, RC_DB.names[code])))] : []));
  select.value = rcLayout(chosen) ? chosen : "auto";
  const mode = $("#remoteMode");
  mode.value = remoteMode();
  const more = $("#remoteMore");
  more.onclick = () => {
    const open = $("#remotePad").classList.toggle("more");
    more.setAttribute("aria-expanded", String(open));
    more.querySelector("span").textContent = open ? "Fewer keys" : "More keys";
  };
}
async function remoteLoadProfile(device) {
  const id = device?.id || "";
  const gen = ++remote.gen;
  remote.id = id;
  remote.info = null;
  remote.family = String(device?.web_kind || "").toLowerCase() === "dreamos" || String(device?.web_kind || "").toLowerCase() === "webinterface" ? "dreambox" : "standard";
  buildRemote();
  if (!id) return;
  remoteVolumeSync();
  try {
    const info = await api(profileURL(id, "remote-profile"));
    if (gen !== remote.gen || id !== screenView.profileID) return;
    remote.info = info;
    if (REMOTE_LAYOUTS[info?.family]) remote.family = info.family;
    $("#remotePad").dataset.family = remote.family;
    buildRemote();
    remoteVolumeSync();
  } catch { /* keep the guess made from the saved web kind */ }
}
function remoteBind() {
  const holder = $("#remoteKeys");
  holder.addEventListener("pointerdown", (event) => {
    const button = event.target.closest(".rk");
    if (!button || event.button > 0 || button.dataset.key === "power") return;
    remote.holdFired = false;
    window.clearTimeout(remote.hold);
    remote.hold = window.setTimeout(() => { remote.holdFired = true; sendRemoteKey(button.dataset.key, true, button); }, 700);
  });
  for (const name of ["pointerup", "pointerleave", "pointercancel"]) holder.addEventListener(name, () => window.clearTimeout(remote.hold));
  holder.addEventListener("click", (event) => {
    const button = event.target.closest(".rk");
    if (!button) return;
    if (remote.holdFired) { remote.holdFired = false; return; }
    if (button.dataset.key === "history" && !event.shiftKey && typeof zapBack === "function" && zapBack()) return;
    sendRemoteKey(button.dataset.key, event.shiftKey, button, button.dataset.key === "power");
  });
  holder.addEventListener("contextmenu", (event) => { if (event.target.closest(".rk")) event.preventDefault(); });
  $("#remotePower").addEventListener("click", (event) => {
    if (event.shiftKey) { sendRemoteKey("power", true, event.currentTarget); return; }
    openPowerDialog();
  });
  $("#remoteMessage").addEventListener("click", async () => {
    const text = await ask({ title: "Message on the TV", message: "Shown on the TV for 10 seconds.", label: "Message", ok: "Send",
      validate: (value) => (value.length > 300 ? "Use at most 300 characters." : "") });
    if (!text) return;
    try { await post(profileURL(screenView.profileID, "message"), { text, seconds: 10 }); toast("Message sent to the TV"); }
    catch (error) { remoteError(error.message); }
  });
  $("#remoteText").addEventListener("click", async () => {
    const text = await ask({ title: "Type on the receiver", message: "Plain letters, digits and symbols only.", label: "Text", ok: "Type",
      validate: (value) => (!value ? "Type something." : value.length > 200 ? "Use at most 200 characters." : /[^\x20-\x7e]/.test(value) ? "Plain letters, digits and symbols only." : "") });
    if (!text) return;
    try { await post(profileURL(screenView.profileID, "remote-text"), { text }); toast("Text sent to the receiver"); }
    catch (error) { remoteError(error.message); }
  });
  $("#remoteLayout").addEventListener("change", (event) => {
    store.set(`remote.layout.${remote.id}`, event.target.value);
    buildRemote();
  });
  $("#remoteMode").addEventListener("change", (event) => store.set(`remote.mode.${remote.id}`, event.target.value));
  $("#remoteHide").addEventListener("click", () => setRemoteOpen(false));
  $("#remoteShow").addEventListener("click", () => setRemoteOpen(true));
}
function setRemoteOpen(open) {
  $("#remotePad").hidden = !open;
  $("#remoteShow").hidden = open;
  store.set("remote.open", open);
}
buildRemote();
remoteBind();
remoteVolumeBind();
