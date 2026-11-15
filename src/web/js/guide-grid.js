"use strict";

// ---- Programme grid: every channel of a bouquet against a time axis ----
const GRID_PX_HOUR = 260;
const grid = { from: 0, hours: Number(store.get("guide.gridHours", 3)) || 3, bouquetRef: "", data: null, key: "", gen: 0, retry: 0, tick: 0 };
if (![2, 3, 6].includes(grid.hours)) grid.hours = 3;

const gridFloor = (ts) => Math.floor(ts / 1800) * 1800;
function gridReset() { grid.data = null; grid.key = ""; grid.bouquetRef = ""; grid.from = 0; }
const gridKey = () => `${grid.bouquetRef}|${grid.from}|${grid.hours}`;

function gridFillBouquets() {
  const select = $("#gridBouquet");
  const usable = ch.bouquets.filter((b) => (b.channels || []).some((c) => !c.marker));
  if (!usable.some((b) => b.ref === grid.bouquetRef)) {
    grid.bouquetRef = (usable.find((b) => b.ref === guide.bouquetRef) || usable.find((b) => b.ref === ch.bouquetRef) || usable[0] || {}).ref || "";
  }
  select.replaceChildren(...usable.map((b) => h("option", { value: b.ref, text: b.name })));
  select.value = grid.bouquetRef;
  return usable.length > 0;
}

function gridEnter() {
  window.clearTimeout(grid.retry);
  window.clearInterval(grid.tick);
  if (!grid.from) grid.from = gridFloor(Date.now() / 1000);
  $("#gridSpan").value = String(grid.hours);
  if (!ch.bouquets.length || !gridFillBouquets()) {
    gdStatus("Loading the channel lists…");
    gdPaint();
    grid.retry = window.setTimeout(() => { if ($("#guideDialog").open && guide.tab === "grid") gridEnter(); }, 700);
    return;
  }
  grid.tick = window.setInterval(() => {
    if (!$("#guideDialog").open || guide.tab !== "grid") { window.clearInterval(grid.tick); return; }
    gdPaint();
  }, 60000);
  if (grid.key !== gridKey()) gridLoad(); else gdPaint();
}

async function gridLoad() {
  const id = guide.profileID, gen = ++grid.gen, key = gridKey();
  if (!grid.bouquetRef) return;
  gdError("");
  gdStatus("Loading the programme grid…");
  const to = grid.from + grid.hours * 3600;
  try {
    const data = await api(guideURL(`epggrid?bouquet=${encodeURIComponent(grid.bouquetRef)}&from=${grid.from}&to=${to}`));
    if (gen !== grid.gen || id !== guide.profileID) return;
    grid.data = data;
    grid.key = key;
    guide.now = data.now || guide.now;
    guide.nowAt = Date.now();
    gdPaint();
  } catch (error) {
    if (gen !== grid.gen || id !== guide.profileID) return;
    grid.data = null;
    grid.key = "";
    gdPaint();
    gdStatus("");
    gdError(error.message);
  }
}

function gridEventMenu(event, channel, e) {
  const now = gdNow(), timer = gdTimerFor({ ref: channel.ref, id: e.id, begin: e.begin, duration: e.duration });
  const full = { ...e, ref: channel.ref, channel: channel.name };
  const items = [{ heading: `${gdClock(e.begin)} · ${e.title}` },
    { label: "Details", icon: "info", run: () => gridDetails(full) },
    { label: "Switch the receiver to this channel", icon: "play", run: () => gdZap(channel.ref, channel.name) }];
  if (e.begin + e.duration > now) {
    if (timer) items.push({ label: "Already has a timer (open Timers)", icon: "bell", run: () => guideSetTab("timers") });
    else {
      items.push({ label: "Record this programme", icon: "rec", run: () => gdAddTimer(full, false) });
      if (e.begin > now) items.push({ label: "Remind me: switch to this channel", icon: "bell", run: () => gdAddTimer(full, true) });
    }
  }
  if (e.id) items.push({ label: "Find similar programmes", icon: "search", run: () => { guideSetTab("epg"); gdLoadSimilar(full); } });
  items.push({ label: "All programmes of this channel", icon: "calendar", run: () => {
    guide.ref = channel.ref; guide.name = channel.name; guide.search = ""; $("#guideSearch").value = ""; guide.loaded = ""; guideSetTab("epg");
  } });
  openMenu(items, event.clientX, event.clientY);
}

function gridDetails(e) {
  const when = `${gdDay(e.begin, false)} ${gdClock(e.begin)}–${gdClock(e.begin + e.duration)} (${gdDuration(e.duration)})`;
  ask({ title: e.title, message: [e.channel, when, e.short, e.long].filter(Boolean).join("\n\n"), input: false, ok: "Close" });
}

function gridPaint(list) {
  const data = grid.data;
  if (!data) {
    list.replaceChildren(gdEmpty(ch.bouquets.length ? "Loading the programme grid…" : "Loading the channel lists…"));
    return;
  }
  const from = data.from, to = data.to, now = gdNow(), px = (ts) => ((ts - from) / 3600) * GRID_PX_HOUR;
  const width = Math.round(px(to));
  $("#gridWhen").textContent = `${gdDay(from, false)} ${gdClock(from)}–${gdClock(to)}`;
  const ruler = h("div", { class: "gg-ruler" });
  ruler.style.width = `${width}px`;
  for (let t = from; t < to; t += 1800) {
    const tick = h("span", { class: `gg-tick${t % 3600 === 0 ? " hour" : ""}`, text: gdClock(t) });
    tick.style.left = `${Math.round(px(t))}px`;
    ruler.append(tick);
  }
  const nowLeft = now >= from && now <= to ? Math.round(px(now)) : -1;
  const rows = data.channels.map((channel) => {
    const known = gdChannelOf(channel.ref) || { ref: channel.ref, name: channel.name, picon: true };
    const track = h("div", { class: "gg-track" });
    track.style.width = `${width}px`;
    for (const e of channel.events) {
      const start = Math.max(e.begin, from), end = Math.min(e.begin + e.duration, to);
      if (end <= start) continue;
      const timer = gdTimerFor({ ref: channel.ref, id: e.id, begin: e.begin, duration: e.duration });
      const cell = h("button", {
        type: "button", class: `gg-ev${e.begin <= now && now < e.begin + e.duration ? " running" : ""}${e.begin + e.duration <= now ? " past" : ""}${timer ? (timer.justplay ? " remind" : " rec") : ""}`,
        title: `${gdClock(e.begin)} · ${e.title}${e.short ? `\n${e.short}` : ""}`,
        onClick: (event) => gridEventMenu(event, channel, e),
      }, h("b", { class: "ellipsis", text: e.title }), h("span", { class: "ellipsis", text: `${gdClock(e.begin)} · ${gdDuration(e.duration)}` }));
      cell.style.left = `${Math.round(px(start))}px`;
      cell.style.width = `${Math.max(14, Math.round(px(end) - px(start)) - 2)}px`;
      track.append(cell);
    }
    if (nowLeft >= 0) { const line = h("i", { class: "gg-now", "aria-hidden": "true" }); line.style.left = `${nowLeft}px`; track.append(line); }
    const label = h("button", { type: "button", class: "gg-ch", title: `Switch the receiver to ${channel.name}`, onClick: () => gdZap(channel.ref, channel.name) },
      piconNode(known, channel.name), h("span", { class: "ellipsis", text: channel.name }));
    return h("div", { class: "gg-row" }, label, track);
  });
  const inner = h("div", { class: "gg-inner" }, h("div", { class: "gg-row gg-headrow" }, h("div", { class: "gg-corner" }), ruler), ...rows);
  list.replaceChildren(rows.length ? inner : gdEmpty("The receiver has no programme information for this bouquet in this time window."));
  gdStatus(rows.length ? `${rows.length} channel${rows.length === 1 ? "" : "s"}` : "");
}

function gridShift(seconds) { grid.from = gridFloor(grid.from + seconds); gridLoad(); }
$("#gridBouquet").addEventListener("change", () => { grid.bouquetRef = $("#gridBouquet").value; guide.bouquetRef = grid.bouquetRef; gridLoad(); });
$("#gridPrev").addEventListener("click", () => gridShift(-grid.hours * 1800));
$("#gridNext").addEventListener("click", () => gridShift(grid.hours * 1800));
$("#gridNow").addEventListener("click", () => { grid.from = gridFloor(Date.now() / 1000); gridLoad(); });
$("#gridSpan").addEventListener("change", () => { grid.hours = Number($("#gridSpan").value) || 3; store.set("guide.gridHours", grid.hours); gridLoad(); });
$("#guideDialog").addEventListener("close", () => { window.clearInterval(grid.tick); window.clearTimeout(grid.retry); });

// ---- Channel details: PIDs, card information from the receiver's ecm.info ----
const svc = { gen: 0, timer: 0, text: "" };
function svcSection(title, pairs, empty) {
  const body = pairs.length
    ? h("dl", { class: "svc-list" }, ...pairs.flatMap((p) => [h("dt", { text: p.k }), h("dd", { class: "ellipsis", title: p.v, text: p.v })]))
    : h("p", { class: "muted small", text: empty });
  return h("section", { class: "svc-sec" }, h("h3", { text: title }), body);
}
async function svcLoad() {
  const id = screenView.profileID, gen = ++svc.gen;
  if (!id) return;
  try {
    const data = await api(profileURL(id, "serviceinfo"));
    if (gen !== svc.gen || !$("#svcDialog").open) return;
    $("#svcError").hidden = true;
    const ecm = data.ecm || [];
    $("#svcBody").replaceChildren(
      svcSection("Channel", data.service || [], "The receiver did not report a channel."),
      svcSection("Card information", ecm, "No card information right now. The receiver only has it while a card-sharing emulator is decoding a scrambled channel."),
      svcSection("Technical", data.ids || [], "No technical details available."));
    const lines = [...(data.service || []), ...ecm, ...(data.ids || [])].map((p) => `${p.k}: ${p.v}`);
    svc.text = lines.join("\n");
    $("#svcStamp").textContent = `Updated ${new Date().toLocaleTimeString("en-GB")}`;
    svcAudio(id, gen);
  } catch (error) {
    if (gen !== svc.gen) return;
    $("#svcError").textContent = error.message;
    $("#svcError").hidden = false;
  }
}
async function svcAudio(id, gen) {
  try {
    const data = await api(profileURL(id, "audio"));
    if (gen !== svc.gen || !$("#svcDialog").open) return;
    const tracks = data.tracks || [];
    $("#svcAudio").hidden = tracks.length === 0;
    $("#svcAudioList").replaceChildren(...tracks.map((t) => h("button", {
      type: "button", class: "btn sm", "aria-pressed": String(t.active), title: t.active ? "Playing now" : "Switch to this audio track",
      onClick: async () => {
        try { await post(profileURL(id, "audio"), { index: t.index }); toast("Audio track changed"); svcLoad(); } catch (error) { toast(error.message, "error"); }
      },
    }, t.description || `Track ${t.index + 1}`)));
  } catch { $("#svcAudio").hidden = true; }
}
function openServiceInfo() {
  if (!screenView.profileID) { toast("Pick a device first", "error"); return; }
  $("#svcBody").replaceChildren(h("p", { class: "muted", text: "Loading…" }));
  $("#svcAudio").hidden = true;
  $("#svcError").hidden = true;
  $("#svcStamp").textContent = "";
  openDialog($("#svcDialog"));
  svcLoad();
  window.clearInterval(svc.timer);
  svc.timer = window.setInterval(() => { if ($("#svcDialog").open) svcLoad(); else window.clearInterval(svc.timer); }, 4000);
}
$("#nowInfo").addEventListener("click", openServiceInfo);
$("#svcClose").addEventListener("click", () => $("#svcDialog").close());
$("#svcDialog").addEventListener("close", () => { window.clearInterval(svc.timer); svc.gen++; });
$("#svcCopy").addEventListener("click", async () => {
  try { await navigator.clipboard.writeText(svc.text); toast("Details copied"); } catch { toast("Could not copy", "error"); }
});
