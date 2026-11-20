"use strict";

const auto = { kind: null, rules: null, gen: 0, missing: false, preview: null, previewTitle: "", editing: null, keep: {}, where: "all", picks: new Map(), retry: 0, busy: false };
const AT_TYPES = { partial: "Title contains", exact: "Title is exactly", start: "Title starts with", description: "Description contains" };
const AT_AFTER = { default: "", nothing: "Do nothing when over", standby: "Standby when over", deepstandby: "Deep standby when over", auto: "Automatic when over" };

function autoReset() { auto.rules = null; auto.preview = null; auto.missing = false; }

async function autoLoad(force) {
  const id = guide.profileID, gen = ++auto.gen;
  if (!id || (!force && auto.rules)) return;
  gdError("");
  gdStatus("Loading the AutoTimer rules…");
  try {
    const data = await api(guideURL("autotimers"));
    if (gen !== auto.gen || id !== guide.profileID) return;
    auto.rules = data.rules || [];
    auto.missing = false;
  } catch (error) {
    if (gen !== auto.gen || id !== guide.profileID) return;
    auto.rules = [];
    auto.missing = error.code === "no_autotimer";
    if (!auto.missing) gdError(error.message);
  }
  gdStatus("");
  if (guide.tab === "auto") gdPaint();
}

function autoChannelSummary(r) {
  const bouquets = r.services.filter((s) => s.bouquet), channels = r.services.filter((s) => !s.bouquet);
  const parts = [];
  if (bouquets.length) parts.push(bouquets.length === 1 ? bouquets[0].name || "1 bouquet" : `${bouquets.length} bouquets`);
  if (channels.length) parts.push(channels.length === 1 ? channels[0].name || "1 channel" : `${channels.length} channels`);
  return parts.length ? parts.join(" + ") : "All channels";
}
function autoSummary(r) {
  const days = r.exclude.days.length ? `skips ${r.exclude.days.map((d) => ({ weekend: "weekends", weekday: "weekdays" })[d] || GD_WEEK[Number(d)]).join(", ")}` : "";
  return [
    `${AT_TYPES[r.search_type] || AT_TYPES.partial} “${r.match}”`, autoChannelSummary(r),
    r.span_from && r.span_to ? `${r.span_from}–${r.span_to}` : "", days,
    r.counter ? `${r.left || 0} of ${r.counter} left` : "", AT_AFTER[r.afterevent] || "",
  ].filter(Boolean).join(" · ");
}

function autoNode(r) {
  const acts = h("div", { class: "gd-acts" },
    iconButton("eye", "Preview what this rule would record", () => autoShowPreview(r)),
    iconButton("edit", "Edit this rule", () => openAutoEditor(r)),
    iconButton(r.enabled ? "pause" : "play", r.enabled ? "Turn this rule off" : "Turn this rule on", () => autoToggle(r)),
    iconButton("trash", "Delete this rule", () => autoDelete(r)));
  return h("div", { class: `gd-item gd-timer${r.enabled ? "" : " dim"}` },
    h("div", { class: "gd-time" }, h("span", { class: "gd-kind" }, icon("bolt"))),
    h("div", { class: "gd-main" },
      h("div", { class: "gd-title" }, r.name || r.match,
        h("span", { class: `badge ${r.enabled ? "ok" : "warn"}`, text: r.enabled ? "On" : "Off" }),
        h("span", { class: `badge ${r.justplay ? "accent" : "bad"}`, text: r.justplay ? "Reminder" : "Record" })),
      h("div", { class: "gd-short", text: autoSummary(r) }),
      r.location ? h("div", { class: "gd-short ellipsis", title: r.location, text: r.location }) : null),
    acts);
}

function autoPaint(list) {
  if (auto.preview) { autoPaintPreview(list); return; }
  if (auto.rules === null) { list.replaceChildren(gdEmpty("Loading the AutoTimer rules…")); return; }
  $("#autoNew").disabled = $("#autoRun").disabled = $("#autoPreview").disabled = auto.missing;
  if (auto.missing) {
    list.replaceChildren(gdEmpty("The AutoTimer plugin is not installed."));
    gdStatus("");
    return;
  }
  $("#autoRun").disabled = $("#autoPreview").disabled = !auto.rules.some((r) => r.enabled);
  const on = auto.rules.filter((r) => r.enabled), off = auto.rules.filter((r) => !r.enabled);
  const section = (title, items) => (items.length ? [h("div", { class: "gd-day", role: "presentation", text: title }), ...items.map(autoNode)] : []);
  const nodes = [...section("Active rules", on), ...section("Turned off", off)];
  list.replaceChildren(...(nodes.length ? nodes : [gdEmpty("No rules yet.")]));
  gdStatus(auto.rules.length ? `${on.length} active rule${on.length === 1 ? "" : "s"}${off.length ? ` · ${off.length} off` : ""}` : "");
}

async function autoShowPreview(rule) {
  const id = guide.profileID;
  gdError("");
  gdStatus("Asking the receiver what would be recorded…");
  try {
    const data = await api(guideURL(`autotimers/preview${rule ? `?id=${rule.id}` : ""}`));
    if (id !== guide.profileID) return;
    auto.preview = (data.timers || []).slice().sort((a, b) => a.begin - b.begin);
    auto.previewTitle = rule ? `Preview of “${rule.name || rule.match}”` : "Preview of all active rules";
    gdPaint();
    $("#guideList").scrollTop = 0;
  } catch (error) { gdStatus(""); gdError(error.message); }
}
function autoPaintPreview(list) {
  const back = h("button", { class: "gd-item gd-folder", type: "button", onClick: () => { auto.preview = null; gdPaint(); } },
    h("div", { class: "gd-time" }, h("span", { class: "gd-kind folder" }, icon("left"))),
    h("div", { class: "gd-main" }, h("div", { class: "gd-title ellipsis", text: `${auto.previewTitle}: back to the rules` })));
  const rows = auto.preview.map((t) => ({ t, node: h("div", { class: `gd-item${t.skipped ? " dim" : ""}` },
    h("div", { class: "gd-time" }, h("b", { text: gdClock(t.begin) }), h("span", { text: gdDuration(t.end - t.begin) })),
    h("div", { class: "gd-main" },
      h("div", { class: "gd-ch ellipsis", text: `${t.channel || "Channel"} · ${t.rule}` }),
      h("div", { class: "gd-title" }, t.title, t.skipped ? h("span", { class: "badge warn", text: "Skipped" }) : null),
      t.message ? h("div", { class: "gd-short", text: t.message }) : null)) }));
  const nodes = rows.length ? gdGrouped(rows, (r) => gdDay(r.t.begin), (r) => gdDayHeader(r.t.begin)) : [gdEmpty("Nothing in the guide matches the rules right now.")];
  list.replaceChildren(back, ...nodes);
  const added = auto.preview.filter((t) => !t.skipped).length;
  gdStatus(`${added} programme${added === 1 ? "" : "s"} would be recorded${auto.preview.length > added ? ` · ${auto.preview.length - added} skipped` : ""}`);
}

function autoRequest(r, over = {}) {
  return {
    id: r.id, name: r.name, match: r.match, enabled: r.enabled, search_type: r.search_type, case_sensitive: r.case_sensitive,
    services: r.services.filter((s) => !s.bouquet).map((s) => s.ref), bouquets: r.services.filter((s) => s.bouquet).map((s) => s.ref),
    justplay: r.justplay, always_zap: r.always_zap, location: r.location || "",
    offset_before: r.offset_before ?? null, offset_after: r.offset_after ?? null, max_duration: r.max_duration || 0,
    span_from: r.span_from || "", span_to: r.span_to || "", counter: r.counter || 0, avoid_duplicates: r.avoid_duplicates || 0,
    afterevent: r.afterevent, include: r.include, exclude: r.exclude, tags: r.tags, ...over,
  };
}
async function autoToggle(r) {
  gdError("");
  try {
    await post(guideURL("autotimers"), autoRequest(r, { enabled: !r.enabled }));
    toast(r.enabled ? "Rule turned off" : "Rule turned on");
    await autoLoad(true);
  } catch (error) { gdError(error.message); }
}
async function autoDelete(r) {
  const ok = await ask({ title: "Delete this rule?", message: `${r.name || r.match}`, input: false, ok: "Delete", danger: true });
  if (!ok) return;
  gdError("");
  try {
    await post(guideURL("autotimers/delete"), { id: r.id });
    toast("Rule deleted");
    await autoLoad(true);
  } catch (error) { gdError(error.message); }
}
async function autoRunNow() {
  const ok = await ask({ title: "Run the rules now?", message: "Creates timers for every match of an active rule.", input: false, ok: "Run now" });
  if (!ok) return;
  gdError("");
  gdStatus("The receiver is searching its guide…");
  $("#autoRun").disabled = true;
  try {
    const data = await post(guideURL("autotimers/run"), {});
    toast(data.message || "Done");
    gdStatus(data.message || "");
    await gdLoadTimers(true);
    await autoLoad(true);
  } catch (error) { gdStatus(""); gdError(error.message); } finally { $("#autoRun").disabled = false; }
}

// ---- rule editor ----
const atLines = (text) => text.split(/\r?\n/).map((x) => x.trim()).filter(Boolean);
function atSetWhere(where) {
  auto.where = where;
  for (const b of $$("#atWhereSeg button")) b.setAttribute("aria-pressed", String(b.dataset.where === where));
  $("#atPickBox").hidden = where === "all";
  atPaintPick();
}
function atBouquets() { return ch.bouquets.filter((b) => (b.channels || []).some((c) => !c.marker)); }
function atPaintPick() {
  const box = $("#atPick"), needle = $("#atPickFilter").value.trim().toLowerCase();
  window.clearTimeout(auto.retry);
  if (auto.where === "all") { box.replaceChildren(); return; }
  if (!ch.bouquets.length) {
    box.replaceChildren(h("p", { class: "muted small", text: "Loading the channel lists…" }));
    $("#atPickCount").textContent = "";
    auto.retry = window.setTimeout(() => { if ($("#atDialog").open) atPaintPick(); }, 700);
    return;
  }
  const check = (ref, label, sub) => h("label", { class: "check at-opt" },
    h("input", { type: "checkbox", checked: auto.picks.has(ref), onChange: (event) => { if (event.target.checked) auto.picks.set(ref, label); else auto.picks.delete(ref); atCount(); } }),
    h("span", { class: "ellipsis", text: label }), sub ? h("span", { class: "muted small", text: sub }) : null);
  let nodes;
  if (auto.where === "bouquets") {
    nodes = atBouquets().filter((b) => !needle || b.name.toLowerCase().includes(needle)).map((b) => check(b.ref, b.name, `${b.channels.filter((c) => !c.marker).length} channels`));
  } else {
    const seen = new Set(), all = [];
    for (const b of ch.bouquets) for (const c of b.channels || []) if (!c.marker && !seen.has(c.ref)) { seen.add(c.ref); all.push(c); }
    const chosen = [...auto.picks].filter(([ref]) => !needle || (auto.picks.get(ref) || "").toLowerCase().includes(needle)).map(([ref, name]) => check(ref, name));
    const matches = all.filter((c) => !auto.picks.has(c.ref) && (!needle || c.name.toLowerCase().includes(needle)));
    nodes = [...chosen, ...matches.slice(0, 120).map((c) => check(c.ref, c.name))];
    if (matches.length > 120) nodes.push(h("p", { class: "muted small", text: `${matches.length - 120} more: narrow the filter.` }));
  }
  box.replaceChildren(...(nodes.length ? nodes : [h("p", { class: "muted small", text: "Nothing matches." })]));
  atCount();
}
function atCount() {
  const n = auto.picks.size;
  $("#atPickCount").textContent = n ? `${n} selected` : auto.where === "all" ? "" : "Nothing selected yet: the rule would match nothing.";
}
function atFillDays(excluded) {
  const set = new Set(excluded);
  const has = (i) => set.has(String(i)) || (set.has("weekend") && i >= 5) || (set.has("weekday") && i <= 4);
  const days = $("#atDays");
  days.replaceChildren(days.querySelector("legend"), ...GD_WEEK.map((name, i) => h("label", { class: "check" }, h("input", { type: "checkbox", value: String(i), checked: has(i) }), name)));
}
const atTime = (v) => (v && /^\d{1,2}:\d{2}$/.test(v) ? v.padStart(5, "0") : "");
function atError(message) { const n = $("#atError"); n.textContent = message || ""; n.hidden = !message; }

function openAutoEditor(r) {
  auto.editing = r || null;
  const rule = r || { services: [], include: { title: [], short: [], desc: [], days: [] }, exclude: { title: [], short: [], desc: [], days: [] }, tags: [], search_type: "partial", afterevent: "default", enabled: true };
  auto.keep = { includeShort: rule.include.short, includeDays: rule.include.days, excludeShort: rule.exclude.short };
  const bouquets = rule.services.filter((s) => s.bouquet), channels = rule.services.filter((s) => !s.bouquet);
  auto.keep.bouquets = channels.length ? bouquets.map((s) => s.ref) : [];
  auto.picks = new Map((channels.length ? channels : bouquets).map((s) => [s.ref, s.name || s.ref]));
  auto.kind = channels.length ? "channels" : bouquets.length ? "bouquets" : null;
  $("#atDialogTitle").textContent = r ? "Edit rule" : "New rule";
  $("#atMatch").value = rule.match || "";
  $("#atName").value = rule.name && rule.name !== rule.match ? rule.name : "";
  $("#atType").value = rule.search_type;
  $("#atCase").checked = Boolean(rule.case_sensitive);
  $("#atAction").value = rule.justplay ? "play" : "record";
  $("#atZap").checked = Boolean(rule.always_zap);
  $("#atZapRow").hidden = rule.justplay;
  $("#atAfter").value = rule.afterevent;
  $("#atFrom").value = atTime(rule.span_from);
  $("#atTo").value = atTime(rule.span_to);
  $("#atBefore").value = rule.offset_before ?? "";
  $("#atAfterMin").value = rule.offset_after ?? "";
  $("#atMaxDur").value = rule.max_duration || "";
  $("#atCounter").value = rule.counter || "";
  $("#atDup").value = String([0, 1, 2].includes(rule.avoid_duplicates) ? rule.avoid_duplicates : 0);
  $("#atDir").value = rule.location || "";
  $("#atTags").value = (rule.tags || []).join(" ");
  $("#atIncTitle").value = rule.include.title.join("\n");
  $("#atIncDesc").value = rule.include.desc.join("\n");
  $("#atExcTitle").value = rule.exclude.title.join("\n");
  $("#atExcDesc").value = rule.exclude.desc.join("\n");
  $("#atOn").checked = rule.enabled !== false;
  atFillDays(rule.exclude.days);
  $("#atAdv").open = Boolean(r && (rule.span_from || rule.exclude.days.length || rule.offset_before != null || rule.max_duration || rule.counter || rule.avoid_duplicates || rule.location || rule.tags.length || rule.include.title.length || rule.include.desc.length || rule.exclude.title.length || rule.exclude.desc.length));
  $("#atPickFilter").value = "";
  atSetWhere(!r ? "all" : channels.length ? "channels" : bouquets.length ? "bouquets" : "all");
  atError("");
  tmFillLocations();
  openDialog($("#atDialog"));
  $("#atMatch").focus();
}

async function saveAutoRule(event) {
  event.preventDefault();
  if (auto.busy) return;
  const match = $("#atMatch").value.trim();
  if (!match) { atError("Type what the rule should look for."); return; }
  const num = (id) => { const v = $(id).value.trim(); return v === "" ? null : Number(v); };
  const before = num("#atBefore"), after = num("#atAfterMin");
  if ((before === null) !== (after === null)) { atError("Set both the start and the end margin, or leave both empty."); return; }
  const from = $("#atFrom").value, to = $("#atTo").value;
  if (Boolean(from) !== Boolean(to)) { atError("Set both times of the time window, or leave both empty."); return; }
  const picked = [...auto.picks.keys()];
  if (auto.where !== "all" && !picked.length) { atError(auto.where === "bouquets" ? "Pick at least one bouquet, or choose All channels." : "Pick at least one channel, or choose All channels."); return; }
  const justplay = $("#atAction").value === "play";
  const body = {
    id: auto.editing ? auto.editing.id : null, name: $("#atName").value.trim(), match, enabled: $("#atOn").checked,
    search_type: $("#atType").value, case_sensitive: $("#atCase").checked,
    services: auto.where === "channels" ? picked : [],
    bouquets: auto.where === "bouquets" ? picked : auto.where === "channels" ? auto.keep.bouquets : [],
    justplay, always_zap: !justplay && $("#atZap").checked, location: $("#atDir").value.trim(),
    offset_before: before, offset_after: after, max_duration: num("#atMaxDur") || 0,
    span_from: from, span_to: to, counter: num("#atCounter") || 0, avoid_duplicates: Number($("#atDup").value),
    afterevent: $("#atAfter").value,
    include: { title: atLines($("#atIncTitle").value), short: auto.keep.includeShort, desc: atLines($("#atIncDesc").value), days: auto.keep.includeDays },
    exclude: { title: atLines($("#atExcTitle").value), short: auto.keep.excludeShort, desc: atLines($("#atExcDesc").value), days: $$("#atDays input:checked").map((b) => b.value) },
    tags: $("#atTags").value.split(/\s+/).filter(Boolean),
  };
  auto.busy = true;
  $("#atSave").disabled = true;
  atError("");
  try {
    const data = await post(guideURL("autotimers"), body);
    $("#atDialog").close();
    toast(data.message || "Rule saved");
    await autoLoad(true);
  } catch (error) { atError(error.message); } finally { auto.busy = false; $("#atSave").disabled = false; }
}

$("#autoNew").addEventListener("click", () => openAutoEditor(null));
$("#autoPreview").addEventListener("click", () => autoShowPreview(null));
$("#autoRun").addEventListener("click", autoRunNow);
$("#autoRefresh").addEventListener("click", () => { auto.preview = null; autoLoad(true); });
$("#atForm").addEventListener("submit", saveAutoRule);
$("#atCancel").addEventListener("click", () => $("#atDialog").close());
$("#atWhereSeg").addEventListener("click", (event) => {
  const b = event.target.closest("button[data-where]");
  if (!b || b.dataset.where === auto.where) return;
  const where = b.dataset.where;
  if (where !== "all" && where !== auto.kind) { auto.picks = new Map(); auto.kind = where; }
  atSetWhere(where);
});
$("#atPickFilter").addEventListener("input", atPaintPick);
$("#atAction").addEventListener("change", () => { $("#atZapRow").hidden = $("#atAction").value === "play"; });
