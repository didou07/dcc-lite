"use strict";
/* ───────────── Channels, now playing, keyboard ─────────────
   The channel list comes from the receiver's own web interface (OpenWebif, through
   DCC Lite). Clicking a channel zaps the receiver, then the picture is refreshed. */
const ch = {
  profileID: "", kind: store.get("channels.kind", "tv") === "radio" ? "radio" : "tv", cache: new Map(), // kind -> bouquets
  bouquets: [], bouquetRef: store.get("channels.bouquet", ""), filter: "", currentRef: "", limit: 300, loading: 0, standby: null, timer: 0, tick: 0, event: null, eventAt: 0, zapping: 0,
  epg: new Map(), epgTimer: 0, // bouquet ref -> { at, now, events: { serviceRef: { title, begin, duration } } }
};
// The name lets the server find name based picons (bbcone.png), like OpenWebif does.
/* Watch on the computer: the receiver's playlist for a channel, opened with VLC or any player */
function watchOnPC(entry) {
  if (!entry || !entry.ref || !ch.profileID) { toast("Pick a channel first"); return; }
  const url = `/api/v1/profiles/${encodeURIComponent(ch.profileID)}/stream.m3u?ref=${encodeURIComponent(entry.ref)}&name=${encodeURIComponent(entry.name || "")}`;
  const link = h("a", { href: url, download: "" });
  document.body.append(link); link.click(); link.remove();
  toast("Playlist saved: open it with VLC or your media player");
}
const piconURL = (ref, name = "") => `/api/v1/profiles/${encodeURIComponent(ch.profileID)}/picon?ref=${encodeURIComponent(ref)}&name=${encodeURIComponent(name)}`;

function setChannelsOpen(open) {
  $("#channelPane").hidden = !open;
  $("#channelShow").hidden = open;
  store.set("channels.open", open);
}
function channelStatus(message, bad = false) {
  const node = $("#channelStatus");
  node.textContent = message || "";
  node.classList.toggle("bad", bad);
}
function channelsReset() {
  ch.profileID = "";
  ch.cache.clear();
  ch.epg.clear();
  ch.bouquets = [];
  ch.currentRef = "";
  ch.standby = null;
  $("#channelList").replaceChildren();
  $("#channelBouquet").replaceChildren();
  $("#nowBar").hidden = true;
  channelStatus("");
}
$("#channelWatch").addEventListener("click", () => {
  const entry = ch.bouquets.flatMap((b) => b.channels || []).find((c) => !c.marker && c.ref === ch.currentRef);
  watchOnPC(entry);
});
function channelsStop() {
  window.clearTimeout(ch.timer);
  window.clearInterval(ch.tick);
  window.clearInterval(ch.epgTimer);
  ch.timer = 0;
  ch.tick = 0;
  ch.epgTimer = 0;
}
function channelsEnter() {
  const id = screenView.profileID;
  if (!id) return;
  if (ch.profileID !== id) { channelsReset(); ch.profileID = id; }
  for (const button of $$("#channelKind button")) button.setAttribute("aria-checked", String(button.dataset.kind === ch.kind));
  if (!ch.bouquets.length) loadChannels(); else renderBouquets();
  channelsStop();
  pollCurrent(true);
  ch.tick = window.setInterval(renderNow, 1000);
  ch.epgTimer = window.setInterval(() => loadEPG(true), 60000);
}

async function loadChannels(force = false) {
  const id = ch.profileID, kind = ch.kind;
  if (!id) return;
  if (!force && ch.cache.has(kind)) { ch.bouquets = ch.cache.get(kind); renderBouquets(); return; }
  const token = ++ch.loading;
  channelStatus("Loading channels…");
  $("#channelReload").disabled = true;
  try {
    const data = await api(`/api/v1/profiles/${encodeURIComponent(id)}/channels?type=${kind}`);
    if (token !== ch.loading || id !== ch.profileID || kind !== ch.kind) return;
    ch.cache.set(kind, data.bouquets || []);
    ch.epg.clear();
    ch.bouquets = data.bouquets || [];
    channelStatus("");
    renderBouquets();
  } catch (error) {
    if (token !== ch.loading) return;
    ch.bouquets = [];
    renderBouquets();
    channelStatus(error.message, true);
  } finally { if (token === ch.loading) $("#channelReload").disabled = false; }
}

function renderBouquets() {
  const select = $("#channelBouquet");
  select.replaceChildren(...ch.bouquets.map((b) => h("option", { value: b.ref, text: `${b.name} (${b.channels.filter((c) => !c.marker).length})` })));
  if (!ch.bouquets.some((b) => b.ref === ch.bouquetRef)) {
    // Prefer the bouquet that holds the channel being watched, else the first one.
    const holder = ch.currentRef && ch.bouquets.find((b) => b.channels.some((c) => c.ref === ch.currentRef));
    ch.bouquetRef = (holder || ch.bouquets[0] || {}).ref || "";
  }
  select.value = ch.bouquetRef;
  select.disabled = !ch.bouquets.length;
  renderChannels();
}

function channelRows() {
  const needle = ch.filter.trim().toLowerCase();
  if (!needle) return (ch.bouquets.find((b) => b.ref === ch.bouquetRef)?.channels || []);
  // A search looks in every bouquet and lists each channel once.
  const seen = new Set(), rows = [];
  for (const bouquet of ch.bouquets) for (const c of bouquet.channels) {
    if (c.marker || seen.has(c.ref) || !c.name.toLowerCase().includes(needle)) continue;
    seen.add(c.ref);
    rows.push(c);
  }
  return rows;
}

function piconNode(entry, fallbackName) {
  const holder = h("span", { class: "picon" });
  const letter = h("span", { class: "picon-letter", text: (fallbackName || "?").trim().charAt(0).toUpperCase() || "?" });
  holder.append(letter);
  if (entry && entry.picon) {
    const img = h("img", { src: piconURL(entry.ref, entry.name), alt: "", loading: "lazy", decoding: "async" });
    img.addEventListener("load", () => holder.classList.add("has-img"));
    img.addEventListener("error", () => img.remove());
    holder.append(img);
  }
  return holder;
}

function renderChannels() {
  const list = $("#channelList");
  const rows = channelRows();
  const shown = rows.slice(0, ch.limit);
  const nodes = shown.map((c) => {
    if (c.marker) return h("div", { class: "ch-marker", role: "presentation", text: c.name || "—" });
    const active = c.ref === ch.currentRef;
    return h("button", { class: `ch-item${active ? " current" : ""}`, type: "button", role: "option", "aria-selected": String(active), dataset: { ref: c.ref }, title: c.name, onClick: () => zapTo(c) },
      h("span", { class: "ch-num", text: c.number || "" }), piconNode(c, c.name),
      h("span", { class: "ch-text" }, h("span", { class: "ch-name ellipsis", text: c.name }), h("span", { class: "ch-epg ellipsis", hidden: true }), h("span", { class: "ch-prog", hidden: true }, h("span"))),
      h("span", { class: "ch-acts" },
        h("span", { class: "ch-pc", role: "presentation", title: "Programme guide of this channel", onClick: (event) => { event.stopPropagation(); openGuide("epg", c); } }, icon("calendar")),
        h("span", { class: "ch-pc", role: "presentation", title: "Watch on this computer", onClick: (event) => { event.stopPropagation(); watchOnPC(c); } }, icon("play"))));
  });
  if (rows.length > shown.length) {
    nodes.push(h("button", { class: "btn sm ch-more", type: "button", onClick: () => { ch.limit += 300; renderChannels(); } }, `Show more (${rows.length - shown.length})`));
  }
  if (!rows.length) nodes.push(h("p", { class: "ch-empty muted", text: ch.filter ? "No channel matches your search." : ch.bouquets.length ? "This bouquet is empty." : "No channels." }));
  list.replaceChildren(...nodes);
  const current = list.querySelector(".ch-item.current");
  if (current) current.scrollIntoView({ block: "nearest" });
  paintEPG();
  loadEPG(false);
}

/* What is on now, per channel of the shown bouquet (OpenWebif "epgnow"). The rows
   are painted in place, so the list does not jump or lose the scroll position. */
function epgFor(ref) {
  for (const data of ch.epg.values()) {
    const e = data.events[ref];
    if (e) return { e, nowAt: data.now + (Date.now() - data.at) / 1000 };
  }
  return null;
}
function paintEPG() {
  for (const row of $$("#channelList .ch-item")) {
    const found = epgFor(row.dataset.ref);
    const line = row.querySelector(".ch-epg"), bar = row.querySelector(".ch-prog");
    if (!line || !bar) continue;
    const running = found && found.e.duration > 0 && found.nowAt >= found.e.begin && found.nowAt < found.e.begin + found.e.duration;
    line.hidden = !running;
    bar.hidden = !running;
    if (!running) continue;
    line.textContent = found.e.title;
    bar.firstChild.style.width = `${Math.max(0, Math.min(100, ((found.nowAt - found.e.begin) / found.e.duration) * 100)).toFixed(1)}%`;
  }
}
async function loadEPG(force) {
  const id = ch.profileID, ref = ch.bouquetRef;
  if (!id || !ref || ch.filter.trim() || $("#channelPane").hidden) return;
  const have = ch.epg.get(ref);
  if (!force && have && Date.now() - have.at < 60000) return;
  try {
    const data = await api(`/api/v1/profiles/${encodeURIComponent(id)}/epgnow?bouquet=${encodeURIComponent(ref)}`);
    if (id !== ch.profileID) return;
    ch.epg.set(ref, { at: Date.now(), now: data.now || Math.floor(Date.now() / 1000), events: data.events || {} });
    if (ref === ch.bouquetRef) paintEPG();
  } catch { /* a receiver without the programme guide still lists its channels */ }
}

async function zapTo(entry) {
  const id = ch.profileID;
  if (!id || entry.marker) return;
  const token = ++ch.zapping;
  ch.currentRef = entry.ref;
  ch.event = null;
  for (const node of $$("#channelList .ch-item")) {
    const on = node.dataset.ref === entry.ref;
    node.classList.toggle("current", on);
    node.setAttribute("aria-selected", String(on));
  }
  showNow({ ref: entry.ref, name: entry.name, now: null, next: null }, entry);
  channelStatus(`Switching to ${entry.name}…`);
  try {
    await post(`/api/v1/profiles/${encodeURIComponent(id)}/zap`, { ref: entry.ref });
    if (token !== ch.zapping || id !== ch.profileID) return;
    channelStatus("");
    // The receiver needs a moment to tune before there is a picture and a programme to show.
    window.setTimeout(() => { if (id === ch.profileID && !screenView.paused) screenRestart(); }, 1200);
    window.setTimeout(() => { if (token === ch.zapping && id === ch.profileID) pollCurrent(true); }, 1800);
  } catch (error) {
    if (token !== ch.zapping) return;
    channelStatus(error.message, true);
    pollCurrent(true);
  }
}

function fmtClock(seconds) {
  const s = Math.max(0, Math.floor(seconds)), m = Math.floor(s / 60), r = s % 60, pad = (n) => String(n).padStart(2, "0");
  return m >= 60 ? `${Math.floor(m / 60)}:${pad(m % 60)}:${pad(r)}` : `${m}:${pad(r)}`;
}
function showNow(info, entry = null) {
  const bar = $("#nowBar");
  if (!info || (!info.name && !info.ref && ch.standby !== true)) { bar.hidden = true; return; }
  bar.hidden = false;
  $("#nowChannel").textContent = info.name || (ch.standby === true ? "Standby" : "—");
  $("#nowTitle").textContent = info.now?.title || "";
  $("#nowNext").textContent = info.next?.title ? `Next: ${info.next.title}` : "";
  const known = entry || ch.bouquets.flatMap((b) => b.channels).find((c) => c.ref === info.ref);
  $("#nowPicon").replaceWith(Object.assign(piconNode(known || { ref: info.ref, picon: Boolean(info.ref) }, info.name), { id: "nowPicon" }));
  ch.event = info.now ? { ...info.now } : null;
  ch.eventAt = Date.now();
  renderNow();
}
function renderNow() {
  const e = ch.event;
  const time = $("#nowTime"), bar = $("#nowProgress");
  if (!e || !e.duration) { time.textContent = ""; bar.style.width = "0"; return; }
  const elapsed = Math.min(e.duration, e.duration - e.remaining + (Date.now() - ch.eventAt) / 1000);
  bar.style.width = `${Math.max(0, Math.min(100, (elapsed / e.duration) * 100)).toFixed(1)}%`;
  time.textContent = `${fmtClock(elapsed)} / ${fmtClock(e.duration)}`;
}

async function pollCurrent(now = false) {
  window.clearTimeout(ch.timer);
  const id = ch.profileID;
  if (!id || view.current !== "screen") return;
  if (now || (!document.hidden && !screenView.paused)) {
    try {
      const info = await api(`/api/v1/profiles/${encodeURIComponent(id)}/current`);
      if (id !== ch.profileID) return;
      if (info.ref && info.ref !== ch.currentRef) {
        ch.currentRef = info.ref;
        if (!ch.filter && !ch.bouquets.find((b) => b.ref === ch.bouquetRef)?.channels.some((c) => c.ref === info.ref)) {
          const holder = ch.bouquets.find((b) => b.channels.some((c) => c.ref === info.ref));
          if (holder) { ch.bouquetRef = holder.ref; $("#channelBouquet").value = holder.ref; }
        }
        renderChannels();
      }
      setStandby(info.standby);
      showNow(info);
    } catch { /* the picture area already reports a receiver that does not answer */ }
  }
  ch.timer = window.setTimeout(() => pollCurrent(false), 10000);
}

$("#channelKind").addEventListener("click", (event) => {
  const button = event.target.closest("button[data-kind]");
  if (!button || button.dataset.kind === ch.kind) return;
  ch.kind = button.dataset.kind;
  store.set("channels.kind", ch.kind);
  ch.bouquetRef = "";
  ch.limit = 300;
  for (const b of $$("#channelKind button")) b.setAttribute("aria-checked", String(b === button));
  loadChannels();
});
$("#channelBouquet").addEventListener("change", () => {
  ch.bouquetRef = $("#channelBouquet").value;
  store.set("channels.bouquet", ch.bouquetRef);
  ch.limit = 300;
  $("#channelList").scrollTop = 0;
  renderChannels();
});
let channelFilterTimer = 0;
$("#channelFilter").addEventListener("input", () => {
  window.clearTimeout(channelFilterTimer);
  channelFilterTimer = window.setTimeout(() => { ch.filter = $("#channelFilter").value; ch.limit = 300; $("#channelList").scrollTop = 0; renderChannels(); }, 120);
});
$("#channelFilter").addEventListener("keydown", (event) => {
  if (event.key === "Enter") {
    // Typing a channel number and pressing Enter zaps to it, like the remote does.
    const typed = $("#channelFilter").value.trim();
    const target = /^\d{1,5}$/.test(typed) ? ch.bouquets.find((b) => b.ref === ch.bouquetRef)?.channels.find((c) => !c.marker && c.number === Number(typed)) : null;
    if (target) {
      window.clearTimeout(channelFilterTimer);
      $("#channelFilter").value = ""; ch.filter = "";
      renderChannels();
      zapTo(target);
    } else { const first = $("#channelList .ch-item"); if (first) first.click(); }
  }
  if (event.key === "Escape" && $("#channelFilter").value) { event.stopPropagation(); $("#channelFilter").value = ""; ch.filter = ""; renderChannels(); }
});
$("#channelList").addEventListener("keydown", (event) => {
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  const items = $$("#channelList .ch-item");
  if (!items.length) return;
  event.preventDefault();
  const at = items.indexOf(document.activeElement);
  const next = items[Math.max(0, Math.min(items.length - 1, at < 0 ? 0 : at + (event.key === "ArrowDown" ? 1 : -1)))];
  next.focus();
});
$("#channelReload").addEventListener("click", () => loadChannels(true));
$("#channelHide").addEventListener("click", () => setChannelsOpen(false));
$("#channelShow").addEventListener("click", () => setChannelsOpen(true));

/* Full screen for the picture. */
$("#screenFull").addEventListener("click", async () => {
  const stage = $("#screenStage");
  try { if (document.fullscreenElement === stage) await document.exitFullscreen(); else await stage.requestFullscreen(); }
  catch { toast("Full screen is not available in this window", "error"); }
});
$("#screenStage").addEventListener("dblclick", () => $("#screenFull").click());

/* Keyboard: the arrow keys, Enter, Esc, Page up/down, +/- and M drive the receiver's remote. */
const KEY_MAP = { ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right", Enter: "ok", Escape: "exit", PageUp: "chup", PageDown: "chdown", "+": "volup", "=": "volup", "-": "voldown", m: "mute", i: "info", e: "epg" };
document.addEventListener("keydown", (event) => {
  if (view.current !== "screen" || event.ctrlKey || event.metaKey || event.altKey) return;
  if (document.querySelector("dialog[open]") || !$("#menu").hidden || event.defaultPrevented) return; // a menu or dialog owns the keys
  const target = event.target;
  if (target.closest?.("input, select, textarea, #channelPane, [contenteditable]")) return;
  if (target.closest?.("button") && (event.key === "Enter" || event.key === " ")) return; // the focused button handles it
  if (event.key.toLowerCase() === "f") { event.preventDefault(); $("#screenFull").click(); return; }
  const key = KEY_MAP[event.key] || KEY_MAP[event.key.toLowerCase()];
  if (!key) return;
  event.preventDefault();
  sendRemoteKey(key, false, $(`#remoteKeys .rk[data-key="${key}"]`));
});



$("#nowStandby").addEventListener("click", () => powerAction("wakeup", "Waking the receiver up…"));

/* The running programme opens the guide of that channel. */
const nowText = $(".now-text");
nowText.setAttribute("role", "button");
nowText.setAttribute("tabindex", "0");
nowText.title = "Open the programme guide of this channel";
const openNowGuide = () => { if (ch.currentRef) openGuide("epg", { ref: ch.currentRef, name: $("#nowChannel").textContent }); };
nowText.addEventListener("click", openNowGuide);
nowText.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); openNowGuide(); } });
