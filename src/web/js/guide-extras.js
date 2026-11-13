"use strict";

async function gdLoadSimilar(e) {
  const id = guide.profileID, gen = ++guide.gen.epg;
  gdError("");
  gdStatus("Looking for similar programmes…");
  try {
    const data = await api(guideURL(`epgsimilar?ref=${encodeURIComponent(e.ref)}&id=${encodeURIComponent(e.id)}`));
    if (gen !== guide.gen.epg || id !== guide.profileID) return;
    guide.events = (data.events || []).filter((x) => !(x.ref === e.ref && x.id === e.id)).sort((a, b) => a.begin - b.begin);
    guide.now = data.now || 0;
    guide.nowAt = Date.now();
    guide.similarTitle = e.title;
    guide.loaded = `s:${e.ref}:${e.id}`;
    $("#guideList").scrollTop = 0;
    gdPaint();
  } catch (error) {
    if (gen !== guide.gen.epg || id !== guide.profileID) return;
    gdStatus("");
    gdError(error.message);
  }
}

async function gdRenameMovie(m) {
  const name = await ask({
    title: "Rename this recording", message: m.file, label: "New name", value: m.title, ok: "Rename",
    validate: (text) => (!text ? "Type a name." : /[\\/]/.test(text) ? "The name cannot contain a slash." : null),
  });
  if (!name || name === m.title) return;
  gdError("");
  try {
    await post(guideURL("movies/rename"), { ref: m.ref, name });
    toast("Recording renamed");
    await gdLoadMovies(guide.dir);
  } catch (error) { gdError(error.message); }
}

async function gdMoveMovie(m) {
  let known = [];
  try { known = (await api(guideURL("movie-locations"))).locations || []; } catch { }
  const here = m.file.slice(0, m.file.lastIndexOf("/") + 1);
  const target = await ask({
    title: "Move this recording",
    message: `${m.title}${known.length ? `\nFolders the receiver knows: ${known.join("   ")}` : ""}`,
    label: "Move to this folder", value: guide.dir || here, ok: "Move",
    validate: (text) => (!text.startsWith("/") ? "Type the full path of the folder, starting with /." : /(^|\/)\.\.(\/|$)/.test(text) ? "A folder path cannot contain “..”." : null),
  });
  if (!target) return;
  const dir = target.endsWith("/") ? target : `${target}/`;
  if (dir === here) { toast("The recording is already in that folder"); return; }
  gdError("");
  try {
    await post(guideURL("movies/move"), { ref: m.ref, dir });
    toast("Recording moved");
    await gdLoadMovies(guide.dir);
  } catch (error) { gdError(error.message); }
}

const tmLocal = (ts) => {
  const d = new Date(ts * 1000);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
};
const tmParse = (text) => { const ms = Date.parse(text); return Number.isFinite(ms) ? Math.floor(ms / 1000) : 0; };
const tmState = { timer: null, beginText: "", endText: "", busy: false };

function tmError(message) {
  const node = $("#timerError");
  node.textContent = message || "";
  node.hidden = !message;
}

function openTimerEditor(t) {
  tmState.timer = t;
  tmState.beginText = tmLocal(t.begin);
  tmState.endText = tmLocal(t.end);
  $("#timerChannel").textContent = t.channel || "";
  $("#tmName").value = t.title || "";
  $("#tmDesc").value = t.description || "";
  $("#tmBegin").value = tmState.beginText;
  $("#tmEnd").value = tmState.endText;
  $("#tmKind").value = t.justplay ? "play" : "record";
  $("#tmAfter").value = [0, 1, 2, 3].includes(t.afterevent) ? String(t.afterevent) : "3";
  $("#tmOn").checked = !t.disabled;
  const days = $("#tmDays");
  days.replaceChildren(days.querySelector("legend"), ...GD_WEEK.map((name, i) => h("label", { class: "check" },
    h("input", { type: "checkbox", value: String(1 << i), checked: Boolean((t.repeated || 0) & (1 << i)) }), name)));
  tmError("");
  openDialog($("#timerDialog"));
  $("#tmName").focus();
  $("#tmName").select();
}

async function saveTimerEdit(event) {
  event.preventDefault();
  const t = tmState.timer;
  if (!t || tmState.busy) return;
  const name = $("#tmName").value.trim();
  if (!name) { tmError("The timer needs a name."); return; }
  const begin = $("#tmBegin").value === tmState.beginText ? t.begin : tmParse($("#tmBegin").value);
  const end = $("#tmEnd").value === tmState.endText ? t.end : tmParse($("#tmEnd").value);
  if (!begin || !end || end <= begin) { tmError("The timer must end after it starts."); return; }
  if (end - begin > 48 * 3600) { tmError("A timer can be at most 48 hours long."); return; }
  const repeated = $$("#tmDays input:checked").reduce((mask, box) => mask | Number(box.value), 0);
  tmState.busy = true;
  $("#timerSave").disabled = true;
  tmError("");
  try {
    await post(guideURL("timers/change"), {
      old_ref: t.ref, old_begin: t.begin, old_end: t.end,
      ref: t.ref, name, description: $("#tmDesc").value.trim(), begin, end,
      justplay: $("#tmKind").value === "play", disabled: !$("#tmOn").checked,
      afterevent: Number($("#tmAfter").value), repeated, dir: t.dir || "", tags: t.tags || "",
    });
    $("#timerDialog").close();
    toast("Timer saved");
    await gdLoadTimers(true);
  } catch (error) { tmError(error.message); } finally {
    tmState.busy = false;
    $("#timerSave").disabled = false;
  }
}

$("#timerForm").addEventListener("submit", saveTimerEdit);
$("#timerCancel").addEventListener("click", () => $("#timerDialog").close());
