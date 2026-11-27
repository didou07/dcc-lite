"use strict";

const logs = {
  entries: [], lastID: 0, timer: 0, busy: false,
  tab: "log", minLevel: store.get("logMinLevel", "info"), component: "", text: "",
  follow: true, paused: false, showAcked: false, config: null, components: new Set(),
};
const LOG_LEVELS = ["trace", "debug", "info", "warn", "error"];
const LOG_MAX = 3000;
const logRank = (level) => Math.max(0, LOG_LEVELS.indexOf(level));

function logTime(iso) {
  const d = new Date(iso);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}.${pad2(d.getMilliseconds(), 3)}`;
}
function logClock(iso) { return logTime(iso).slice(0, 8); }
function logDateTime(iso) { return new Date(iso).toLocaleString(); }

function logVisible(entry) {
  if (logRank(entry.level) < logRank(logs.minLevel)) return false;
  if (logs.component && entry.component !== logs.component) return false;
  if (logs.text) {
    const hay = `${entry.component} ${entry.message} ${(entry.fields || []).map((f) => `${f.k}=${f.v}`).join(" ")}`.toLowerCase();
    if (!hay.includes(logs.text)) return false;
  }
  return true;
}

const LOG_ID = /^[0-9a-f]{16,}$/i;
const LOG_QUIET_FIELDS = new Set(["req"]);
const LOG_VOLATILE = new Set(["req", "ms", "bytes", "took"]);

function logStamp(iso) {
  const d = new Date(iso);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${logTime(iso)}`;
}
function logDuration(ms) { return ms >= 1000 ? `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)} s` : `${ms} ms`; }

function logMessage(entry) {
  if (entry.component !== "http") return entry.message;
  const m = /^([A-Z]+) \/api\/v1\/(.*)$/.exec(entry.message);
  const text = m ? `${m[1]} ${m[2]}` : entry.message;
  return text
    .replace(/profiles\/([0-9a-f]{32})(\/|$)/g, (_, id, tail) => `${deviceByID(id)?.name || `${id.slice(0, 8)}…`}${tail ? " · " : ""}`)
    .replace(/\b[0-9a-f]{32}\b/g, (id) => `${id.slice(0, 8)}…`);
}

const LOG_PLAIN = new Set(["device", "host", "url", "route", "path", "error", "client", "detail", "dir"]);

function logValue(key, raw, all = false) {
  const value = String(raw);
  const num = /^\d+$/.test(value) ? Number(value) : NaN;
  if (key === "req") return { text: `#${value}`, label: false, cls: "lg-dim" };
  if (LOG_ID.test(value)) {
    const short = value.slice(0, 8);
    return { text: key === "session" ? `#${short}` : `${short}…`, label: key !== "session", copy: value, cls: "lg-id" };
  }
  switch (key) {
    case "status":
      if (num >= 200 && num < 300 && !all) return null;
      return { text: value, label: false, cls: num >= 500 ? "lg-err" : num >= 400 ? "lg-slow" : "" };
    case "ms": return Number.isNaN(num) ? null : { text: logDuration(num), label: false, cls: num >= 2000 ? "lg-slow" : "", title: `${value} ms` };
    case "bytes": return Number.isNaN(num) ? null : { text: formatBytes(num), label: false, title: `${value} bytes` };
    case "port": return { text: `:${value}`, label: false };
    case "version": return { text: `v${value}`, label: false };
    case "error": return { text: value, label: false, cls: "lg-err" };
  }
  return { text: value, label: !LOG_PLAIN.has(key) };
}

function logFieldNode(key, raw, all = false) {
  const v = logValue(key, raw, all);
  if (!v) return null;
  const node = h("b", { class: v.cls || "", text: v.text });
  if (v.copy) { node.title = v.copy; node.addEventListener("click", async () => { if (await clipboardWrite(v.copy)) toast("Copied"); }); }
  else if (v.title) node.title = v.title;
  return h("span", { class: "lg-field" }, v.label ? h("i", { text: `${key}=` }) : null, node);
}

function logFieldNodes(entry) {
  const quiet = logRank(entry.level) < logRank("warn");
  return (entry.fields || [])
    .filter((f) => !(quiet && LOG_QUIET_FIELDS.has(f.k)))
    .map((f) => logFieldNode(f.k, f.v))
    .filter(Boolean);
}

function logPlain(entry) {
  const parts = [logStamp(entry.time), entry.level.toUpperCase().padEnd(5), entry.component.padEnd(8), logMessage(entry).replace(/\s*\n\s*/g, " ")];
  for (const f of entry.fields || []) {
    const v = logValue(f.k, f.v, true);
    if (v) parts.push(v.label ? `${f.k}=${v.copy || v.text}` : (v.copy || v.text).replace(/\s*\n\s*/g, " "));
  }
  if (entry.stack) parts.push(`stack=${entry.stack.split("\n").map((l) => l.trim()).filter(Boolean).join(" > ")}`);
  return parts.join("  ");
}

function logKey(entry) {
  if (entry.stack) return "";
  const rest = (entry.fields || []).filter((f) => !LOG_VOLATILE.has(f.k)).map((f) => `${f.k}=${f.v}`).join("|");
  return `${entry.level}|${entry.component}|${entry.message}|${rest}`;
}

function logMerge(row, entry) {
  const n = (Number(row.dataset.count) || 1) + 1;
  row.dataset.count = String(n);
  const time = row.querySelector(".lg-time");
  time.textContent = logClock(entry.time);
  time.title = `${row.dataset.first} → ${logStamp(entry.time)}`;
  const rep = row.querySelector(".lg-rep");
  rep.hidden = false;
  rep.textContent = `×${n}`;
  row.querySelector(".lg-fields")?.replaceChildren(...logFieldNodes(entry));
}

function logRow(entry) {
  const fields = logFieldNodes(entry);
  const stack = entry.stack ? stackBlock(entry.stack) : null;
  const req = entry.fields?.find((f) => f.k === "req")?.v;
  const row = h("div", { class: "lg-row", dataset: { level: entry.level, id: entry.id, key: logKey(entry), first: logStamp(entry.time) } },
    h("span", { class: "lg-time", title: logStamp(entry.time), text: logClock(entry.time) }),
    h("span", { class: "lg-level", text: entry.level.toUpperCase() }),
    h("span", { class: "lg-comp", text: entry.component }),
    h("div", { class: "lg-msg" },
      h("span", { class: "lg-text", title: req ? `${entry.message}\nreq=${req}` : entry.message, text: logMessage(entry) }),
      h("span", { class: "badge lg-rep", hidden: true }),
      fields.length ? h("span", { class: "lg-fields" }, fields) : null,
      stack?.button));
  if (!stack) return row;
  return h("div", { class: "lg-group" }, row, stack.pre);
}

function logPush(list, entries) {
  let prev = list.lastElementChild;
  const frag = document.createDocumentFragment();
  for (const entry of entries) {
    const key = logKey(entry);
    if (key && prev?.dataset?.key === key) { logMerge(prev, entry); continue; }
    const node = logRow(entry);
    frag.append(node);
    prev = node;
  }
  list.append(frag);
}

function stackBlock(text, buttonClass = "lg-stack-btn") {
  const pre = h("pre", { class: "lg-stack", text });
  pre.hidden = true;
  const button = h("button", { class: buttonClass, type: "button", title: "Show the stack trace", "aria-label": "Show the stack trace", "aria-expanded": "false" }, icon("code"));
  button.addEventListener("click", () => {
    pre.hidden = !pre.hidden;
    button.setAttribute("aria-expanded", String(!pre.hidden));
    button.classList.toggle("on", !pre.hidden);
  });
  return { button, pre };
}

function logsRender(full = true) {
  const list = $("#logsList");
  const visible = logs.entries.filter(logVisible);
  const stick = logs.follow;
  if (full) {
    list.replaceChildren();
    logPush(list, visible);
  }
  $("#logsEmpty").hidden = visible.length > 0;
  $("#logsShown").textContent = `${visible.length} of ${logs.entries.length}`;
  if (stick) list.scrollTop = list.scrollHeight;
}

function logsAppend(entries) {
  const list = $("#logsList");
  const fresh = entries.filter(logVisible);
  if (fresh.length) {
    logPush(list, fresh);
    while (list.childElementCount > LOG_MAX) list.firstElementChild.remove();
  }
  $("#logsEmpty").hidden = list.childElementCount > 0;
  $("#logsShown").textContent = `${list.childElementCount} of ${logs.entries.length}`;
  if (logs.follow) list.scrollTop = list.scrollHeight;
}

function logsComponents(counts) {
  for (const name of Object.keys(counts || {})) logs.components.add(name);
  const select = $("#logsComp");
  const names = [...logs.components].sort();
  if (select.options.length - 1 === names.length) return;
  select.replaceChildren(h("option", { value: "", text: "All components" }), ...names.map((n) => h("option", { value: n, text: n })));
  select.value = logs.component;
}

function logsCounts(levels) {
  const box = $("#logsChips");
  box.replaceChildren(...LOG_LEVELS.map((lv) => h("button", {
    type: "button", class: `lg-chip ${logs.minLevel === lv ? "on" : ""}`, dataset: { level: lv },
    title: `Show ${lv} and above`, onClick: () => { setMinLevel(lv); },
  }, lv, h("b", { text: String(levels?.[lv] || 0) }))));
}

function setMinLevel(level) {
  logs.minLevel = level;
  store.set("logMinLevel", level);
  logsRender(true);
  logsCounts(logs.lastCounts);
}

async function logsPoll(first = false) {
  if (logs.busy || logs.paused) return;
  logs.busy = true;
  try {
    const data = await api(`/api/v1/logs?since=${first ? 0 : logs.lastID}&level=trace&limit=${first ? 1500 : 500}`);
    if (data.last_id < logs.lastID) { logs.entries = []; logs.lastID = 0; return logsPoll(true); }
    logs.lastID = data.last_id;
    logs.lastCounts = data.counts?.levels;
    logsComponents(data.counts?.components);
    logsCounts(data.counts?.levels);
    faultCount(data.faults);
    if (first) {
      logs.entries = data.entries;
      logsRender(true);
    } else if (data.entries.length) {
      logs.entries.push(...data.entries);
      if (logs.entries.length > LOG_MAX) logs.entries.splice(0, logs.entries.length - LOG_MAX);
      logsAppend(data.entries);
    }
    $("#logsLive").dataset.state = "on";
    $("#logsLive").textContent = "Live";
  } catch (error) {
    $("#logsLive").dataset.state = "off";
    $("#logsLive").textContent = "Offline";
  } finally { logs.busy = false; }
}

function logsStart() {
  logsStop();
  logs.paused = false; syncPause();
  logsPoll(true);
  logs.timer = window.setInterval(() => { if (!document.hidden && logs.tab === "log") logsPoll(); }, 1500);
}
function logsStop() { window.clearInterval(logs.timer); logs.timer = 0; }
function syncPause() {
  const b = $("#logsPause");
  const name = logs.paused ? "Resume" : "Pause";
  b.replaceChildren(icon(logs.paused ? "play" : "pause"));
  b.title = name;
  b.setAttribute("aria-label", name);
  if (logs.paused) { $("#logsLive").dataset.state = "paused"; $("#logsLive").textContent = "Paused"; }
}

async function logsLoadConfig() {
  try {
    logs.config = await api("/api/v1/logs/config");
    $("#logsCapture").value = logs.config.level;
    $("#logsFile").textContent = logs.config.file || "file logging is off";
    $("#logsFile").title = logs.config.file || "";
  } catch (error) { toastError(error); }
}

function faultCount(stats) {
  const open = stats?.open || 0;
  $("#faultCount").textContent = open ? String(open) : "";
}

function faultCard(f) {
  const ctx = (f.context || []).filter((c) => c.k !== "req").map((c) => logFieldNode(c.k, c.v)).filter(Boolean);
  const stack = f.stack ? stackBlock(f.stack, "btn sm sq") : null;
  const buttons = h("div", { class: "ft-actions" },
    stack?.button,
    h("button", { class: "btn sm", type: "button", title: "Filter the log by this component and message", onClick: () => faultShowInLog(f) }, icon("search"), "Show in log"),
    f.acked ? null : h("button", { class: "btn sm", type: "button", title: "Mark this fault as seen", onClick: () => faultAck(f.id) }, icon("bell"), "Acknowledge"));
  const head = h("div", { class: "ft-head" },
    h("span", { class: "lg-level", dataset: { level: f.level }, text: f.panic ? "PANIC" : f.level.toUpperCase() }),
    h("span", { class: "lg-comp", text: f.component }),
    h("b", { class: "ft-msg", text: f.message }),
    h("span", { class: "badge", title: "Times this happened", text: `×${f.count}` }),
    buttons);
  const when = h("div", { class: "ft-when muted small" },
    `Last ${logDateTime(f.last_seen)}`, " · ", `first ${logDateTime(f.first_seen)}`, f.route ? ` · ${f.route}` : "");
  return h("div", { class: `ft-card ${f.acked ? "acked" : ""}`, dataset: { level: f.level } },
    head,
    f.error ? h("div", { class: "ft-error", text: f.error }) : null,
    ctx.length ? h("div", { class: "lg-fields" }, ctx) : null,
    when,
    stack?.pre);
}

async function faultsLoad() {
  try {
    const data = await api("/api/v1/faults");
    faultCount(data.stats);
    const list = data.faults.filter((f) => logs.showAcked || !f.acked);
    $("#faultsList").replaceChildren(...list.map(faultCard));
    $("#faultsEmpty").hidden = list.length > 0;
    const s = data.stats;
    $("#faultsSummary").textContent = s.total
      ? `${s.open} open (${s.errors} errors${s.panics ? `, ${s.panics} crashes` : ""}) · ${s.total} total`
      : "";
  } catch (error) { toastError(error); }
}
async function faultAck(id) { try { await post(`/api/v1/faults/${encodeURIComponent(id)}/ack`); } catch (e) { toastError(e); } faultsLoad(); }
function faultShowInLog(f) {
  logs.component = f.component; logs.text = (f.message || "").toLowerCase().slice(0, 40);
  $("#logsSearch").value = f.message.slice(0, 40);
  setMinLevel("trace");
  logsTab("log");
  $("#logsComp").value = f.component;
}

function logsTab(name) {
  logs.tab = name;
  for (const b of $$("[data-logtab]")) b.setAttribute("aria-selected", String(b.dataset.logtab === name));
  $("#logsPaneLog").hidden = name !== "log";
  $("#logsPaneFaults").hidden = name !== "faults";
  $("#logsLive").hidden = name !== "log";
  if (name === "faults") faultsLoad(); else logsPoll(logs.entries.length === 0);
  if (name === "log") logsRender(true);
}

function logsEnter() {
  logsLoadConfig();
  logsTab(logs.tab);
  logsStart();
}
function logsLeave() { logsStop(); }

function logsDownload() {
  const q = new URLSearchParams({ level: logs.minLevel, limit: "5000" });
  if (logs.component) q.set("component", logs.component);
  if (logs.text) q.set("q", logs.text);
  window.location.assign(`/api/v1/logs/download?${q}`);
}
async function logsCopy() {
  const text = logs.entries.filter(logVisible).map(logPlain).join("\n");
  if (await clipboardWrite(text)) toast("Log copied"); else toast("Could not copy", "error");
}

$("#logsComp").addEventListener("change", (e) => { logs.component = e.target.value; logsRender(true); });
$("#logsSearch").addEventListener("input", (e) => { logs.text = e.target.value.trim().toLowerCase(); logsRender(true); });
function setFollow(on) {
  logs.follow = on;
  const b = $("#logsFollow");
  b.setAttribute("aria-pressed", String(on));
  b.classList.toggle("primary", on);
  if (on) logsRender(false);
}
$("#logsFollow").addEventListener("click", () => setFollow(!logs.follow));
setFollow(true);
$("#logsList").addEventListener("wheel", () => {
  window.setTimeout(() => {
    const list = $("#logsList");
    const atEnd = list.scrollHeight - list.scrollTop - list.clientHeight < 24;
    if (!atEnd && logs.follow) setFollow(false);
  }, 50);
}, { passive: true });
$("#logsPause").addEventListener("click", () => { logs.paused = !logs.paused; syncPause(); if (!logs.paused) logsPoll(); });
$("#logsClear").addEventListener("click", async () => {
  try { await api("/api/v1/logs", { method: "DELETE" }); } catch (e) { toastError(e); return; }
  logs.entries = []; logs.lastID = 0; logsPoll(true);
});
$("#logsDownload").addEventListener("click", logsDownload);
$("#logsCopy").addEventListener("click", logsCopy);
$("#logsCapture").addEventListener("change", async (e) => {
  try {
    logs.config = await api("/api/v1/logs/config", { method: "PUT", body: JSON.stringify({ level: e.target.value }) });
    toast(`Detail level: ${logs.config.level}`);
  } catch (error) { toastError(error); logsLoadConfig(); }
});
for (const b of $$("[data-logtab]")) b.addEventListener("click", () => logsTab(b.dataset.logtab));
$("#faultsAckAll").addEventListener("click", async () => { try { await post("/api/v1/faults/ack-all"); } catch (e) { toastError(e); } faultsLoad(); });
$("#faultsClear").addEventListener("click", async () => {
  if (!window.confirm("Forget all recorded faults?")) return;
  try { await api("/api/v1/faults", { method: "DELETE" }); } catch (e) { toastError(e); } faultsLoad();
});
$("#faultsShowAcked").addEventListener("click", (e) => {
  logs.showAcked = !logs.showAcked;
  const b = e.currentTarget;
  b.setAttribute("aria-pressed", String(logs.showAcked));
  b.classList.toggle("primary", logs.showAcked);
  b.querySelector("use").setAttribute("href", logs.showAcked ? "#i-eye" : "#i-eye-off");
  faultsLoad();
});
