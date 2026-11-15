"use strict";

// ---- EPG refresh: the receiver visits channels in the background to keep its guide fresh (needs the EPGRefresh plugin) ----
const er = { picks: new Map(), busy: false, retry: 0, settings: null };

function erError(message) { const n = $("#erError"); n.textContent = message || ""; n.hidden = !message; }
function erCount() {
  const n = er.picks.size;
  $("#erCount").textContent = n ? `${n} selected` : "Nothing selected yet: choose at least one bouquet or channel.";
}
function erPaintPick() {
  const box = $("#erPick"), needle = $("#erFilter").value.trim().toLowerCase();
  window.clearTimeout(er.retry);
  if (!ch.bouquets.length) {
    box.replaceChildren(h("p", { class: "muted small", text: "Loading the channel lists…" }));
    er.retry = window.setTimeout(() => { if ($("#erDialog").open) erPaintPick(); }, 700);
    return;
  }
  const check = (ref, label, sub) => h("label", { class: "check at-opt" },
    h("input", { type: "checkbox", checked: er.picks.has(ref), onChange: (event) => { if (event.target.checked) er.picks.set(ref, label); else er.picks.delete(ref); erCount(); } }),
    h("span", { class: "ellipsis", text: label }), sub ? h("span", { class: "muted small", text: sub }) : null);
  const matches = (name) => !needle || name.toLowerCase().includes(needle);
  const chosen = [...er.picks].filter(([, name]) => matches(name)).map(([ref, name]) => check(ref, name, ref.includes("FROM BOUQUET") ? "bouquet" : ""));
  const bouquets = ch.bouquets.filter((b) => !er.picks.has(b.ref) && matches(b.name) && (b.channels || []).some((c) => !c.marker)).map((b) => check(b.ref, b.name, "bouquet"));
  const seen = new Set(), channels = [];
  for (const b of ch.bouquets) for (const c of b.channels || []) if (!c.marker && !seen.has(c.ref)) { seen.add(c.ref); if (!er.picks.has(c.ref) && matches(c.name) && needle) channels.push(c); }
  const nodes = [...chosen, ...bouquets, ...channels.slice(0, 80).map((c) => check(c.ref, c.name, ""))];
  if (!needle) nodes.push(h("p", { class: "muted small", text: "Type in the filter to pick single channels." }));
  if (channels.length > 80) nodes.push(h("p", { class: "muted small", text: `${channels.length - 80} more: type more to narrow the list.` }));
  box.replaceChildren(...nodes);
  erCount();
}

function erFill(s) {
  er.settings = s;
  er.picks = new Map(s.services.map((x) => [x.ref, x.name || x.ref]));
  $("#erOn").checked = s.enabled;
  $("#erBegin").value = s.begin;
  $("#erEnd").value = s.end;
  $("#erSeconds").value = s.seconds_per_channel || 120;
  $("#erDelay").value = s.delay_standby || 10;
  $("#erAdapter").value = ["main", "pip", "pip_hidden", "record"].includes(s.adapter) ? s.adapter : "main";
  $("#erRunAT").value = s.run_autotimer || "never";
  $("#erSkip").value = s.skip_protected || "bg_only";
  $("#erMessage").checked = s.show_message;
  $("#erForce").checked = s.force;
  $("#erWake").checked = s.wakeup;
  $("#erShutdown").checked = s.shutdown_after;
  $("#erInherit").checked = s.inherit_autotimer;
  $("#erInherit").closest("label").hidden = !s.has_autotimer;
  $("#erLast").textContent = s.last_scan ? `Last refresh: ${new Date(s.last_scan * 1000).toLocaleString((window.DCC_I18N?.locale() || "en-GB"), { dateStyle: "medium", timeStyle: "short" })}` : "The receiver has not reported a refresh yet.";
  $("#erFilter").value = "";
  erPaintPick();
}

async function openEPGRefresh() {
  if (!guide.profileID) return;
  erError("");
  $("#erSave").disabled = $("#erNow").disabled = true;
  openDialog($("#erDialog"));
  $("#erLast").textContent = "Loading the settings from the receiver…";
  try {
    erFill(await api(guideURL("epgrefresh")));
    $("#erSave").disabled = $("#erNow").disabled = false;
  } catch (error) {
    $("#erLast").textContent = "";
    erError(error.code === "no_epgrefresh" ? "The EPGRefresh plugin is not installed or not active on this receiver. Install enigma2-plugin-extensions-epgrefresh from the Packages tool." : error.message);
  }
}

async function saveEPGRefresh(event) {
  event.preventDefault();
  if (er.busy) return;
  const on = $("#erOn").checked;
  if (on && !er.picks.size) { erError("Choose at least one bouquet or channel, or turn EPG refresh off."); return; }
  const body = {
    enabled: on, show_message: $("#erMessage").checked, begin: $("#erBegin").value, end: $("#erEnd").value,
    seconds_per_channel: Number($("#erSeconds").value), delay_standby: Number($("#erDelay").value),
    inherit_autotimer: $("#erInherit").checked, shutdown_after: $("#erShutdown").checked, force: $("#erForce").checked, wakeup: $("#erWake").checked,
    run_autotimer: $("#erRunAT").value, adapter: $("#erAdapter").value, skip_protected: $("#erSkip").value, services: [...er.picks.keys()],
  };
  er.busy = true;
  $("#erSave").disabled = true;
  erError("");
  try {
    const data = await post(guideURL("epgrefresh"), body);
    if (data.notes && data.notes.length) {
      erFill(data.settings);
      erError(data.notes.join(" "));
      toast("Saved, with a note");
    } else {
      $("#erDialog").close();
      toast(on ? "EPG refresh saved" : "EPG refresh turned off");
    }
  } catch (error) { erError(error.message); } finally { er.busy = false; $("#erSave").disabled = false; }
}

async function epgRefreshNow() {
  erError("");
  $("#erNow").disabled = true;
  try {
    const data = await post(guideURL("epgrefresh/now"), {});
    toast(data.message || "Refresh started");
  } catch (error) { erError(error.message); } finally { $("#erNow").disabled = false; }
}

$("#erOpen").addEventListener("click", openEPGRefresh);
$("#erForm").addEventListener("submit", saveEPGRefresh);
$("#erCancel").addEventListener("click", () => $("#erDialog").close());
$("#erNow").addEventListener("click", epgRefreshNow);
$("#erFilter").addEventListener("input", erPaintPick);
