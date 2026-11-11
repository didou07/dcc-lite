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
  const p = (n, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}
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

function logRow(entry) {
  const fields = (entry.fields || []).map((f) => h("span", { class: "lg-field" },
    h("i", { text: `${f.k}=` }), h("b", { class: f.k === "error" ? "lg-err" : "", text: f.v })));
  const row = h("div", { class: "lg-row", dataset: { level: entry.level, id: entry.id } },
    h("span", { class: "lg-time", title: logDateTime(entry.time), text: logTime(entry.time) }),
    h("span", { class: "lg-level", text: entry.level.toUpperCase() }),
    h("span", { class: "lg-comp", text: entry.component }),
    h("div", { class: "lg-msg" }, h("span", { class: "lg-text", text: entry.message }), fields.length ? h("span", { class: "lg-fields" }, fields) : null));
  if (!entry.stack) return row;
  const box = h("details", { class: "lg-stack-box" }, h("summary", { text: "Stack trace" }), h("pre", { class: "lg-stack", text: entry.stack }));
  return h("div", { class: "lg-group" }, row, box);
}

function logsRender(full = true) {
  const list = $("#logsList");
  const visible = logs.entries.filter(logVisible);
  const stick = logs.follow;
  if (full) {
    list.replaceChildren(...visible.map(logRow));
  }
  $("#logsEmpty").hidden = visible.length > 0;
  $("#logsShown").textContent = `${visible.length} of ${logs.entries.length}`;
  if (stick) list.scrollTop = list.scrollHeight;
}

function logsAppend(entries) {
  const list = $("#logsList");
  const fresh = entries.filter(logVisible);
  if (fresh.length) {
    list.append(...fresh.map(logRow));
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
  $("#logsLevel").value = level;
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
  b.replaceChildren(icon(logs.paused ? "play" : "pause"), h("span", { text: logs.paused ? "Resume" : "Pause" }));
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
  const ctx = (f.context || []).map((c) => h("span", { class: "lg-field" }, h("i", { text: `${c.k}=` }), h("b", { text: c.v })));
  const head = h("div", { class: "ft-head" },
    h("span", { class: "lg-level", dataset: { level: f.level }, text: f.panic ? "PANIC" : f.level.toUpperCase() }),
    h("span", { class: "lg-comp", text: f.component }),
    h("b", { class: "ft-msg", text: f.message }),
    h("span", { class: "badge", title: "Times this happened", text: `×${f.count}` }));
  const when = h("div", { class: "ft-when muted small" },
    `Last ${logDateTime(f.last_seen)}`, " · ", `first ${logDateTime(f.first_seen)}`, f.route ? ` · ${f.route}` : "");
  const buttons = h("div", { class: "ft-actions" },
    h("button", { class: "btn sm", type: "button", title: "Filter the log by this component and message", onClick: () => faultShowInLog(f) }, icon("search"), "Show in log"),
    f.acked ? null : h("button", { class: "btn sm", type: "button", onClick: () => faultAck(f.id) }, icon("check"), "Acknowledge"));
  return h("div", { class: `ft-card ${f.acked ? "acked" : ""}`, dataset: { level: f.level } },
    head,
    f.error ? h("div", { class: "ft-error", text: f.error }) : null,
    ctx.length ? h("div", { class: "lg-fields" }, ctx) : null,
    when,
    f.stack ? h("details", { class: "lg-stack-box" }, h("summary", { text: "Stack trace" }), h("pre", { class: "lg-stack", text: f.stack })) : null,
    buttons);
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
  const text = logs.entries.filter(logVisible).map((e) => {
    const parts = [logDateTime(e.time), e.level.toUpperCase().padEnd(5), e.component.padEnd(8), e.message.replace(/\s*\n\s*/g, " ")];
    for (const x of e.fields || []) parts.push(`${x.k}=${String(x.v).replace(/\s*\n\s*/g, " ")}`);
    if (e.stack) parts.push(`stack=${e.stack.split("\n").map((l) => l.trim()).filter(Boolean).join(" > ")}`);
    return parts.join(" ");
  }).join("\n");
  try { await navigator.clipboard.writeText(text); toast("Log copied"); } catch { toast("Could not copy", "error"); }
}

$("#logsLevel").value = logs.minLevel;
$("#logsLevel").addEventListener("change", (e) => setMinLevel(e.target.value));
$("#logsComp").addEventListener("change", (e) => { logs.component = e.target.value; logsRender(true); });
$("#logsSearch").addEventListener("input", (e) => { logs.text = e.target.value.trim().toLowerCase(); logsRender(true); });
$("#logsFollow").addEventListener("change", (e) => { logs.follow = e.target.checked; if (logs.follow) logsRender(false); });
$("#logsList").addEventListener("wheel", () => {
  window.setTimeout(() => {
    const list = $("#logsList");
    const atEnd = list.scrollHeight - list.scrollTop - list.clientHeight < 24;
    if (!atEnd && logs.follow) { logs.follow = false; $("#logsFollow").checked = false; }
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
$("#faultsShowAcked").addEventListener("change", (e) => { logs.showAcked = e.target.checked; faultsLoad(); });
