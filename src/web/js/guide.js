"use strict";
const guide = {
  profileID: "", tab: store.get("guide.tab", "epg"), ref: "", name: "", bouquetRef: "", search: "",
  events: [], now: 0, nowAt: 0, loaded: "",
  timers: [], timersAt: 0,
  dir: "", folders: [], movies: [], movieFilter: "", movieLimit: 200, moviesAt: 0, filled: -1, watch: 0,
  gen: { epg: 0, timers: 0, movies: 0 },
};
const GUIDE_TABS = ["epg", "timers", "movies"];
const guideURL = (path) => profileURL(guide.profileID, path);

const gdClock = (ts) => { const d = new Date(ts * 1000); return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`; };
const gdDayStart = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
function gdDay(ts, long = true) {
  const d = new Date(ts * 1000), diff = Math.round((gdDayStart(d) - gdDayStart(new Date())) / 86400000);
  if (diff === 0) return "Today";
  if (diff === 1) return "Tomorrow";
  if (diff === -1) return "Yesterday";
  return d.toLocaleDateString("en-GB", long ? { weekday: "long", day: "numeric", month: "long" } : { weekday: "short", day: "numeric", month: "short" });
}
function gdDuration(seconds) {
  const minutes = Math.max(1, Math.round(seconds / 60));
  return minutes >= 60 ? `${Math.floor(minutes / 60)} h ${pad2(minutes % 60)} min` : `${minutes} min`;
}
const gdNow = () => (guide.now ? guide.now + (Date.now() - guide.nowAt) / 1000 : Date.now() / 1000);
const gdChannelOf = (ref) => ch.bouquets.flatMap((b) => b.channels || []).find((c) => !c.marker && c.ref === ref);
function gdError(message) {
  const node = $("#guideError");
  node.textContent = message || "";
  node.hidden = !message;
}
function gdStatus(message) { $("#guideStatus").textContent = message || ""; }
function gdEmpty(text) { return h("p", { class: "gd-empty muted", text }); }
function gdDayHeader(ts) { return h("div", { class: "gd-day", role: "presentation", text: gdDay(ts) }); }
function gdGrouped(rows, key, header) {
  const nodes = [];
  let last = null;
  for (const row of rows) {
    const k = key(row);
    if (k !== last) { nodes.push(header(row)); last = k; }
    nodes.push(row.node);
  }
  return nodes;
}

async function gdLoadTimers(force = false) {
  const id = guide.profileID;
  if (!id || (!force && guide.timersAt && Date.now() - guide.timersAt < 30000)) return;
  const gen = ++guide.gen.timers;
  try {
    const data = await api(guideURL("timers"));
    if (gen !== guide.gen.timers || id !== guide.profileID) return;
    guide.timers = data.timers || [];
    guide.timersAt = Date.now();
    if (guide.tab === "timers" || guide.tab === "epg") gdPaint();
  } catch (error) {
    if (gen !== guide.gen.timers || id !== guide.profileID) return;
    if (guide.tab === "timers") { guide.timers = []; gdPaint(); gdError(error.message); }
  }
}
function gdTimerFor(e) {
  return guide.timers.find((t) => {
    if (t.ref !== e.ref || t.state === 3) return false;
    if (t.eit && t.eit === e.id) return true;
    if (t.justplay) return t.begin >= e.begin - 120 && t.begin < e.begin + e.duration;
    return Math.min(t.end, e.begin + e.duration) - Math.max(t.begin, e.begin) > e.duration / 2;
  }) || null;
}
async function gdAddTimer(e, justplay) {
  gdError("");
  try {
    await post(guideURL("timers"), { ref: e.ref, name: e.title, description: e.short || "", begin: e.begin, end: e.begin + e.duration, eventid: e.id, justplay });
    toast(justplay ? "Reminder added: the receiver will switch to this channel" : "Recording timer added");
    await gdLoadTimers(true);
  } catch (error) { gdError(error.message); }
}
async function gdZap(ref, name) {
  gdError("");
  try {
    await post(guideURL("zap"), { ref });
    toast(`Receiver switched to ${name || "the channel"}`);
    ch.currentRef = ref;
    window.setTimeout(() => { if (guide.profileID === screenView.profileID) { if (!screenView.paused) screenRestart(); pollCurrent(true); } }, 1500);
  } catch (error) { gdError(error.message); }
}

function gdFillChannels() {
  guide.filled = ch.bouquets.length;
  const bouquets = $("#guideBouquet"), select = $("#guideChannel");
  if (!ch.bouquets.some((b) => b.ref === guide.bouquetRef)) {
    const holder = guide.ref && ch.bouquets.find((b) => b.channels.some((c) => c.ref === guide.ref));
    guide.bouquetRef = (holder || ch.bouquets.find((b) => b.ref === ch.bouquetRef) || ch.bouquets[0] || {}).ref || "";
  }
  bouquets.replaceChildren(...ch.bouquets.map((b) => h("option", { value: b.ref, text: b.name })));
  bouquets.value = guide.bouquetRef;
  bouquets.hidden = !ch.bouquets.length;
  const list = (ch.bouquets.find((b) => b.ref === guide.bouquetRef)?.channels || []).filter((c) => !c.marker);
  const options = list.map((c) => h("option", { value: c.ref, text: `${c.number ? `${c.number}. ` : ""}${c.name}` }));
  if (guide.ref && !list.some((c) => c.ref === guide.ref)) options.unshift(h("option", { value: guide.ref, text: guide.name || "Current channel" }));
  select.replaceChildren(h("option", { value: "", text: "Choose a channel…" }), ...options);
  select.value = guide.ref || "";
}
async function gdLoadEPG() {
  const id = guide.profileID, gen = ++guide.gen.epg, query = guide.search.trim();
  gdError("");
  if (query && query.length < 2) { guide.events = []; guide.loaded = ""; gdStatus("Type at least two letters to search."); gdPaint(); return; }
  if (!query && !guide.ref) { guide.events = []; guide.loaded = ""; gdStatus(""); gdPaint(); return; }
  const wanted = query ? `q:${query}` : `c:${guide.ref}`;
  gdStatus(query ? "Searching the guide…" : "Loading the programme guide…");
  try {
    const data = await api(guideURL(query ? `epgsearch?q=${encodeURIComponent(query)}` : `epg?ref=${encodeURIComponent(guide.ref)}`));
    if (gen !== guide.gen.epg || id !== guide.profileID) return;
    guide.events = (data.events || []).slice().sort((a, b) => a.begin - b.begin);
    guide.now = data.now || 0;
    guide.nowAt = Date.now();
    guide.loaded = wanted;
    gdPaint();
  } catch (error) {
    if (gen !== guide.gen.epg || id !== guide.profileID) return;
    guide.events = [];
    guide.loaded = "";
    gdPaint();
    gdStatus("");
    gdError(error.message);
  }
}
function gdEventNode(e, withChannel) {
  const now = gdNow(), running = e.begin <= now && now < e.begin + e.duration;
  const timer = gdTimerFor(e), text = e.long || e.short;
  const toggle = () => { const on = row.classList.toggle("open"); row.setAttribute("aria-expanded", String(on)); };
  const stop = (fn) => (event) => { event.stopPropagation(); fn(); };
  const acts = h("div", { class: "gd-acts" },
    iconButton("play", "Switch the receiver to this channel", stop(() => gdZap(e.ref, e.channel))),
    e.id ? iconButton("search", "Find similar programmes", stop(() => gdLoadSimilar(e))) : h("span", { class: "gd-gap", "aria-hidden": "true" }),
    e.begin > now ? iconButton("bell", "Remind me: switch to this channel when it starts", stop(() => gdAddTimer(e, true))) : h("span", { class: "gd-gap", "aria-hidden": "true" }),
    iconButton("rec", timer ? "This programme already has a timer (open Timers)" : "Record this programme", stop(() => (timer ? guideSetTab("timers") : gdAddTimer(e, false))), timer ? "on" : ""));
  const bar = h("div", { class: "ch-prog" }, h("span"));
  bar.firstChild.style.width = `${Math.max(0, Math.min(100, ((now - e.begin) / e.duration) * 100)).toFixed(1)}%`;
  const row = h("div", {
    class: `gd-item${running ? " running" : ""}`, role: "button", tabindex: "0", "aria-expanded": "false", onClick: toggle,
    onKeydown: (event) => { if ((event.key === "Enter" || event.key === " ") && event.target === row) { event.preventDefault(); toggle(); } },
  },
  h("div", { class: "gd-time" }, h("b", { text: gdClock(e.begin) }), h("span", { text: gdDuration(e.duration) })),
  h("div", { class: "gd-main" },
    withChannel ? h("div", { class: "gd-ch ellipsis", text: `${e.channel || "Channel"} · ${gdDay(e.begin, false)}` }) : null,
    h("div", { class: "gd-title" }, e.title, timer ? h("span", { class: `badge ${timer.justplay ? "accent" : "bad"}`, text: timer.justplay ? "Reminder" : "Recording" }) : null),
    e.short ? h("div", { class: "gd-short ellipsis", text: e.short }) : null,
    running ? bar : null,
    text ? h("div", { class: "gd-long", text }) : null),
  acts);
  return row;
}
function gdPaintEPG(list) {
  const similar = guide.loaded.startsWith("s:");
  const searching = similar || guide.loaded.startsWith("q:") || Boolean(guide.search.trim());
  const now = gdNow();
  const events = guide.events.filter((e) => e.begin + e.duration > now);
  const back = similar ? h("button", { class: "gd-item gd-folder", type: "button", onClick: () => { guide.loaded = ""; gdLoadEPG(); } },
    h("div", { class: "gd-time" }, h("span", { class: "gd-kind folder" }, icon("left"))),
    h("div", { class: "gd-main" }, h("div", { class: "gd-title ellipsis", text: `Similar to “${guide.similarTitle}”: back to the programme list` }))) : null;
  if (!events.length) {
    const query = guide.search.trim();
    list.replaceChildren(...(back ? [back] : []), gdEmpty(similar ? "The receiver found no similar programmes." : searching
      ? (query.length < 2 ? "Search the programme guide of every channel: type a title or a word." : `Nothing found for “${query}”.`)
      : guide.ref ? "The receiver has no programme information for this channel." : "Choose a channel to see its programmes, or search the guide."));
    if (guide.loaded) gdStatus("");
    return;
  }
  const rows = events.map((e) => ({ e, node: gdEventNode(e, searching) }));
  list.replaceChildren(...(back ? [back] : []), ...gdGrouped(rows, (r) => gdDay(r.e.begin), (r) => gdDayHeader(r.e.begin)));
  gdStatus(`${events.length} ${similar ? "similar " : ""}programme${events.length === 1 ? "" : "s"}`);
}

const GD_WEEK = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
function gdRepeat(mask) {
  if (!mask) return "";
  if ((mask & 127) === 127) return "Every day";
  return GD_WEEK.filter((_, i) => mask & (1 << i)).join(", ");
}
function gdTimerState(t) {
  if (t.state === 3) return ["Done", ""];
  if (t.state === 2) return [t.justplay ? "Switching" : "Recording", "bad"];
  if (t.disabled) return ["Off", "warn"];
  return t.justplay ? ["Reminder", "accent"] : ["Scheduled", "ok"];
}
async function gdTimerCall(action, t) {
  gdError("");
  try {
    await post(guideURL(`timers/${action}`), { ref: t.ref, begin: t.begin, end: t.end, eit: t.eit || 0 });
    await gdLoadTimers(true);
    return true;
  } catch (error) { gdError(error.message); return false; }
}
async function gdDeleteTimer(t) {
  const ok = await ask({
    title: t.state === 2 ? "Stop and delete this recording timer?" : "Delete this timer?",
    message: `${t.title}\n${t.channel} · ${gdDay(t.begin, false)} ${gdClock(t.begin)}${t.state === 2 ? "\nThe recording in progress is stopped." : ""}`,
    input: false, ok: "Delete", danger: true,
  });
  if (ok && (await gdTimerCall("delete", t))) toast("Timer deleted");
}
function gdTimerNode(t) {
  const [label, kind] = gdTimerState(t);
  const known = gdChannelOf(t.ref) || { ref: t.ref, name: t.channel, picon: true };
  const when = `${gdDay(t.begin, false)} ${gdClock(t.begin)}–${gdClock(t.end)}`;
  const repeat = gdRepeat(t.repeated);
  const acts = h("div", { class: "gd-acts" },
    t.state === 3 ? null : iconButton("edit", "Edit this timer", () => openTimerEditor(t)),
    t.state === 3 ? null : iconButton(t.disabled ? "play" : "pause", t.disabled ? "Turn this timer on" : "Turn this timer off", async () => { if (await gdTimerCall("toggle", t)) toast(t.disabled ? "Timer turned on" : "Timer turned off"); }),
    iconButton("trash", "Delete this timer", () => gdDeleteTimer(t)));
  return h("div", { class: `gd-item gd-timer${t.state === 2 ? " running" : ""}${t.state === 3 || t.disabled ? " dim" : ""}` },
    h("div", { class: "gd-time" }, piconNode(known, t.channel)),
    h("div", { class: "gd-main" },
      h("div", { class: "gd-title" }, t.title || "Timer", h("span", { class: `badge ${kind}`, text: label })),
      h("div", { class: "gd-short ellipsis", text: [t.channel, when, gdDuration(t.end - t.begin), repeat].filter(Boolean).join(" · ") }),
      t.file ? h("div", { class: "gd-short ellipsis", title: t.file, text: t.file }) : null),
    acts);
}
function gdPaintTimers(list) {
  const active = guide.timers.filter((t) => t.state !== 3 && !t.disabled).sort((a, b) => a.begin - b.begin);
  const off = guide.timers.filter((t) => t.state !== 3 && t.disabled).sort((a, b) => a.begin - b.begin);
  const done = guide.timers.filter((t) => t.state === 3).sort((a, b) => b.begin - a.begin);
  const section = (title, items) => (items.length ? [h("div", { class: "gd-day", role: "presentation", text: title }), ...items.map(gdTimerNode)] : []);
  const nodes = [...section("Coming up", active), ...section("Turned off", off), ...section("Finished", done)];
  list.replaceChildren(...(nodes.length ? nodes : [gdEmpty("No timers yet. Open a programme in the guide and press the record button.")]));
  const running = guide.timers.filter((t) => t.state === 2 && !t.justplay).length;
  gdStatus(guide.timers.length ? `${active.length + off.length} timer${active.length + off.length === 1 ? "" : "s"}${running ? ` · ${running} recording now` : ""}${done.length ? ` · ${done.length} finished` : ""}` : "");
  $("#guideCleanup").disabled = !done.length;
}

async function gdLoadMovies(dir = guide.dir) {
  const id = guide.profileID, gen = ++guide.gen.movies;
  gdError("");
  gdStatus("Loading recordings…");
  try {
    const data = await api(guideURL(dir ? `movies?dir=${encodeURIComponent(dir)}` : "movies"));
    if (gen !== guide.gen.movies || id !== guide.profileID) return;
    guide.dir = data.directory || dir || "";
    guide.folders = data.folders || [];
    guide.movies = (data.movies || []).slice().sort((a, b) => (b.time || 0) - (a.time || 0));
    guide.moviesAt = Date.now();
    guide.movieLimit = 200;
    gdPaint();
  } catch (error) {
    if (gen !== guide.gen.movies || id !== guide.profileID) return;
    guide.folders = [];
    guide.movies = [];
    gdPaint();
    gdStatus("");
    gdError(error.message);
  }
}
const gdParent = (dir) => (dir.replace(/\/+$/, "").replace(/[^/]*$/, "") || "/");
function gdMovieDate(time) {
  return time ? new Date(time * 1000).toLocaleString("en-GB", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
}
async function gdDeleteMovie(m) {
  const ok = await ask({ title: "Delete this recording?", message: `${m.title}\n${m.file}\nThe receiver may move it to its trash folder first.`, input: false, ok: "Delete", danger: true });
  if (!ok) return;
  gdError("");
  try {
    await post(guideURL("movies/delete"), { ref: m.ref });
    guide.movies = guide.movies.filter((x) => x.ref !== m.ref);
    toast("Recording deleted");
    gdPaint();
  } catch (error) { gdError(error.message); }
}
function gdMovieNode(m) {
  const meta = [m.channel, gdMovieDate(m.time), m.length && `${m.length} min`, m.size && formatBytes(m.size)].filter(Boolean).join(" · ");
  return h("div", { class: "gd-item gd-movie" },
    h("div", { class: "gd-time" }, h("span", { class: "gd-kind" }, icon("video"))),
    h("div", { class: "gd-main" },
      h("div", { class: "gd-title ellipsis", title: m.title, text: m.title }),
      meta ? h("div", { class: "gd-short ellipsis", text: meta }) : null,
      m.info ? h("div", { class: "gd-short ellipsis", title: m.info, text: m.info }) : null),
    h("div", { class: "gd-acts" },
      iconButton("play", "Watch on this computer (opens a playlist in VLC or your player)", () => {
        downloadLink(guideURL(`movie.m3u?file=${encodeURIComponent(m.file)}&name=${encodeURIComponent(m.title)}`));
        toast("Playlist saved: open it with VLC or your media player");
      }),
      iconButton("edit", "Rename this recording", () => gdRenameMovie(m)),
      iconButton("folder", "Move this recording to another folder", () => gdMoveMovie(m)),
      iconButton("trash", "Delete this recording", () => gdDeleteMovie(m))));
}
function gdPaintMovies(list) {
  $("#guideDir").textContent = guide.dir || "Default folder";
  $("#guideUp").disabled = !guide.dir || guide.dir === "/";
  const needle = guide.movieFilter.trim().toLowerCase();
  const movies = needle ? guide.movies.filter((m) => `${m.title} ${m.channel} ${m.file}`.toLowerCase().includes(needle)) : guide.movies;
  const folders = needle ? guide.folders.filter((f) => f.toLowerCase().includes(needle)) : guide.folders;
  const shown = movies.slice(0, guide.movieLimit);
  const nodes = folders.map((name) => h("button", { class: "gd-item gd-folder", type: "button", onClick: () => gdLoadMovies(`${guide.dir.replace(/\/+$/, "")}/${name}/`) },
    h("div", { class: "gd-time" }, h("span", { class: "gd-kind folder" }, icon("folder"))),
    h("div", { class: "gd-main" }, h("div", { class: "gd-title ellipsis", text: name })),
    h("div", { class: "gd-acts" }, icon("right", "sm"))));
  nodes.push(...shown.map(gdMovieNode));
  if (movies.length > shown.length) nodes.push(h("button", { class: "btn sm gd-more", type: "button", onClick: () => { guide.movieLimit += 200; gdPaint(); } }, `Show more (${movies.length - shown.length})`));
  if (!nodes.length) nodes.push(gdEmpty(needle ? "No recording matches your search." : "No recordings in this folder."));
  list.replaceChildren(...nodes);
  gdStatus(guide.moviesAt ? `${movies.length} recording${movies.length === 1 ? "" : "s"}${folders.length ? ` · ${folders.length} folder${folders.length === 1 ? "" : "s"}` : ""}` : "");
}

function gdPaint() {
  const list = $("#guideList");
  list.dataset.tab = guide.tab;
  if (guide.tab === "epg") gdPaintEPG(list);
  else if (guide.tab === "timers") gdPaintTimers(list);
  else gdPaintMovies(list);
}
function guideSetTab(tab) {
  if (!GUIDE_TABS.includes(tab)) tab = "epg";
  guide.tab = tab;
  store.set("guide.tab", tab);
  for (const button of $$("#guideTabs button")) button.setAttribute("aria-selected", String(button.dataset.tab === tab));
  for (const row of $$("#guideDialog [data-for]")) row.hidden = row.dataset.for !== tab;
  gdError("");
  gdStatus("");
  $("#guideList").scrollTop = 0;
  if (tab === "epg") {
    gdFillChannels();
    if (guide.loaded !== (guide.search.trim() ? `q:${guide.search.trim()}` : `c:${guide.ref}`)) gdLoadEPG(); else gdPaint();
    gdLoadTimers(false);
  } else if (tab === "timers") {
    gdPaint();
    gdLoadTimers(true);
  } else {
    gdPaint();
    if (!guide.moviesAt || Date.now() - guide.moviesAt > 30000) gdLoadMovies();
  }
}
function openGuide(tab = guide.tab, entry = null) {
  const id = screenView.profileID;
  if (!id) { toast("Pick a device first", "error"); return; }
  if (guide.profileID !== id) {
    Object.assign(guide, { profileID: id, ref: "", name: "", bouquetRef: "", search: "", events: [], loaded: "", timers: [], timersAt: 0, dir: "", folders: [], movies: [], movieFilter: "", moviesAt: 0 });
    $("#guideSearch").value = "";
    $("#guideMovieFilter").value = "";
  }
  const target = entry && entry.ref ? entry : (ch.currentRef ? { ref: ch.currentRef, name: gdChannelOf(ch.currentRef)?.name || $("#nowChannel").textContent } : null);
  if (target && (entry || !guide.ref)) {
    if (target.ref !== guide.ref) guide.loaded = "";
    guide.ref = target.ref;
    guide.name = target.name || gdChannelOf(target.ref)?.name || "";
    guide.bouquetRef = "";
    if (entry) { guide.search = ""; $("#guideSearch").value = ""; }
  }
  $("#guideTitle").textContent = "Guide";
  $("#guideSub").textContent = $("#screenSub").textContent || "";
  openDialog($("#guideDialog"));
  guideSetTab(tab);
  window.clearInterval(guide.watch);
  guide.watch = window.setInterval(() => {
    if (!$("#guideDialog").open) { window.clearInterval(guide.watch); return; }
    if (guide.tab !== "epg" || !ch.bouquets.length || guide.filled === ch.bouquets.length) return;
    if (!guide.ref && ch.currentRef) { guide.ref = ch.currentRef; guide.name = gdChannelOf(ch.currentRef)?.name || ""; }
    gdFillChannels();
    if (guide.ref && !guide.search.trim() && !guide.loaded) gdLoadEPG();
  }, 600);
}

$("#guideTabs").addEventListener("click", (event) => {
  const button = event.target.closest("button[data-tab]");
  if (button) guideSetTab(button.dataset.tab);
});
$("#guideBouquet").addEventListener("change", () => { guide.bouquetRef = $("#guideBouquet").value; gdFillChannels(); });
$("#guideChannel").addEventListener("change", () => {
  const ref = $("#guideChannel").value;
  guide.ref = ref;
  guide.name = gdChannelOf(ref)?.name || "";
  guide.search = "";
  $("#guideSearch").value = "";
  $("#guideList").scrollTop = 0;
  gdLoadEPG();
});
let guideSearchTimer = 0;
$("#guideSearch").addEventListener("input", () => {
  window.clearTimeout(guideSearchTimer);
  guideSearchTimer = window.setTimeout(() => { guide.search = $("#guideSearch").value; $("#guideList").scrollTop = 0; gdLoadEPG(); }, 350);
});
$("#guideSearch").addEventListener("keydown", (event) => {
  if (event.key === "Enter") { window.clearTimeout(guideSearchTimer); guide.search = $("#guideSearch").value; gdLoadEPG(); }
  if (event.key === "Escape" && $("#guideSearch").value) {
    event.preventDefault(); event.stopPropagation();
    $("#guideSearch").value = ""; guide.search = ""; gdLoadEPG();
  }
});
let guideMovieTimer = 0;
$("#guideMovieFilter").addEventListener("input", () => {
  window.clearTimeout(guideMovieTimer);
  guideMovieTimer = window.setTimeout(() => { guide.movieFilter = $("#guideMovieFilter").value; guide.movieLimit = 200; gdPaint(); }, 120);
});
$("#guideMovieFilter").addEventListener("keydown", (event) => {
  if (event.key === "Escape" && $("#guideMovieFilter").value) {
    event.preventDefault(); event.stopPropagation();
    $("#guideMovieFilter").value = ""; guide.movieFilter = ""; gdPaint();
  }
});
$("#guideTimersRefresh").addEventListener("click", () => gdLoadTimers(true));
$("#guideCleanup").addEventListener("click", async () => {
  gdError("");
  try { await post(guideURL("timers/cleanup"), {}); toast("Finished timers removed"); await gdLoadTimers(true); } catch (error) { gdError(error.message); }
});
$("#guideUp").addEventListener("click", () => gdLoadMovies(gdParent(guide.dir)));
$("#guideMoviesRefresh").addEventListener("click", () => gdLoadMovies(guide.dir));
$("#guideClose").addEventListener("click", () => $("#guideDialog").close());
$("#guideShow").addEventListener("click", () => openGuide());
