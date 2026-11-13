"use strict";
async function powerAction(action, doing, confirm = null, report = remoteError) {
  const id = screenView.profileID;
  if (!id) return false;
  if (confirm && !(await ask({ title: confirm.title, message: confirm.message, input: false, ok: confirm.ok, danger: true }))) return false;
  report("");
  try {
    const data = await post(profileURL(id, "power"), { action });
    void data;
    toast(doing);
    if (action === "reload") { loadChannels(true); return true; }
    window.setTimeout(() => { if (id === screenView.profileID) { screenRestart(); pollCurrent(true); } }, action === "wakeup" || action === "toggle" ? 2500 : 1200);
    return true;
  } catch (error) { report(error.message); return false; }
}

const powerView = { gen: 0, busy: false };
function powerDialogError(message) {
  const node = $("#powerError");
  node.textContent = message || "";
  node.hidden = !message;
}
function powerSetBusy(busy) {
  powerView.busy = busy;
  for (const button of $("#powerDialog").querySelectorAll(".power-btn, .slot-btn, #powerReload, #powerKey")) {
    if (busy) { button.dataset.wasDisabled = button.disabled ? "1" : ""; button.disabled = true; }
    else { button.disabled = button.dataset.wasDisabled === "1"; delete button.dataset.wasDisabled; }
  }
}
async function powerRun(task) {
  if (powerView.busy) return;
  powerDialogError("");
  powerSetBusy(true);
  let done = false;
  try { done = await task(); } finally { powerSetBusy(false); }
  if (done) $("#powerDialog").close();
}

function openPowerDialog() {
  const id = screenView.profileID;
  if (!id) return;
  const standby = ch.standby === true;
  $("#powerState").textContent = standby ? "In standby" : ch.standby === false ? "On" : "";
  powerDialogError("");

  const act = (action, doing, confirm) => powerAction(action, doing, confirm, powerDialogError);
  const actions = [
    { label: "Toggle standby", run: () => act("toggle", "Standby toggled") },
    standby ? { label: "Wake up", run: () => act("wakeup", "Waking the receiver up…") }
      : { label: "Standby", run: () => act("standby", "Receiver goes to standby") },
    { label: "Restart interface (GUI)", run: () => act("restart_gui", "Restarting the interface…", { title: "Restart the interface?", message: "Enigma2 restarts on the receiver. The picture is gone for about a minute; recordings in progress may be interrupted.", ok: "Restart GUI" }) },
    { label: "Reboot", run: () => act("reboot", "Rebooting the receiver…", { title: "Reboot the receiver?", message: "The whole receiver restarts and will be unreachable for a couple of minutes.", ok: "Reboot" }) },
    { label: "Deep standby", danger: true, run: () => act("deep", "Shutting the receiver down…", { title: "Shut the receiver down?", message: "The receiver switches off completely. It cannot be woken from here; you must start it with its own button or a timer.", ok: "Shut down" }) },
  ];
  $("#powerActions").replaceChildren(...actions.map((item) => h("button", {
    type: "button", class: `power-btn${item.danger ? " danger" : ""}`, text: item.label, onClick: () => powerRun(item.run),
  })));

  $("#powerReload").onclick = () => powerRun(() => powerAction("reload", "Channel lists reloaded on the receiver", null, powerDialogError));
  $("#powerKey").onclick = () => powerRun(async () => { await sendRemoteKey("power", false, $("#remotePower")); return true; });
  $("#powerClose").onclick = () => $("#powerDialog").close();

  powerLoadSlots(id);
  powerLoadSleep(id);
  openDialog($("#powerDialog"));
  $("#powerActions").querySelector("button")?.focus({ preventScroll: true });
}

async function powerLoadSlots(id) {
  const gen = ++powerView.gen;
  const box = $("#powerSlots"), note = $("#powerSlotsNote");
  box.replaceChildren();
  note.textContent = "Looking for MultiBoot slots…";
  try {
    const data = await api(profileURL(id, "multiboot"));
    if (gen !== powerView.gen) return;
    const slots = data.slots || [];
    if (!slots.length) { note.textContent = "This receiver does not report MultiBoot slots."; return; }
    note.textContent = "";
    box.replaceChildren(...slots.map((slot) => h("button", {
      type: "button", class: "slot-btn", title: slot.label, disabled: slot.current,
      "aria-current": slot.current ? "true" : null,
      onClick: () => powerRun(() => powerBoot(id, slot)),
    }, h("span", { class: "ellipsis", text: slot.label }))));
  } catch (error) {
    if (gen !== powerView.gen) return;
    note.textContent = `MultiBoot slots could not be read: ${error.message}`;
  }
}

async function powerBoot(id, slot) {
  const ok = await ask({
    title: "Restart into this slot?",
    message: `The receiver restarts into “${slot.label}”. It will be unreachable for a couple of minutes.`,
    input: false, ok: "Restart", danger: true,
  });
  if (!ok) return false;
  try {
    await post(profileURL(id, "multiboot"), { slot: slot.slot, code: slot.code });
    toast(`Restarting into ${slot.label}…`);
    return true;
  } catch (error) { powerDialogError(error.message); return false; }
}
function setStandby(value) {
  ch.standby = value === true ? true : value === false ? false : null;
  $("#nowBar").classList.toggle("standby", ch.standby === true);
  $("#nowStandby").hidden = ch.standby !== true;
  if (ch.standby === true) $("#screenWait").textContent = "The receiver is in standby. Use the power button to wake it up.";
}

const sleepURL = (id) => profileURL(id, "sleeptimer");
function sleepShow(state) {
  const note = $("#sleepState"), select = $("#sleepMinutes");
  if (!state) { note.textContent = ""; return; }
  const minutes = Number(state.minutes) || 0;
  if (minutes && ![...select.options].some((o) => Number(o.value) === minutes)) {
    const option = h("option", { value: String(minutes), text: `${minutes} min` });
    select.append(option);
    [...select.options].sort((a, b) => Number(a.value) - Number(b.value)).forEach((o) => select.append(o));
  }
  if (minutes) select.value = String(minutes);
  $("#sleepAction").value = state.action === "shutdown" ? "shutdown" : "standby";
  note.textContent = state.enabled
    ? `The sleep timer is on: ${minutes} min, then ${state.action === "shutdown" ? "deep standby" : "standby"}.`
    : "The sleep timer is off.";
}
async function powerLoadSleep(id) {
  const gen = powerView.gen;
  $("#sleepState").textContent = "";
  try {
    const state = await api(sleepURL(id));
    if (gen === powerView.gen && id === screenView.profileID) sleepShow(state);
  } catch {
    if (gen === powerView.gen) $("#sleepState").textContent = "This receiver does not report a sleep timer.";
  }
}
async function powerSleep(enabled) {
  const id = screenView.profileID;
  if (!id) return;
  powerDialogError("");
  for (const button of [$("#sleepSet"), $("#sleepOff")]) button.disabled = true;
  try {
    const state = await post(sleepURL(id), enabled
      ? { enabled: true, minutes: Number($("#sleepMinutes").value), action: $("#sleepAction").value }
      : { enabled: false });
    sleepShow(state);
    toast(enabled ? "Sleep timer started" : "Sleep timer cancelled");
  } catch (error) { powerDialogError(error.message); } finally {
    for (const button of [$("#sleepSet"), $("#sleepOff")]) button.disabled = false;
  }
}
async function powerRecordNow(infinite) {
  const id = screenView.profileID;
  if (!id) return;
  powerDialogError("");
  for (const button of [$("#recNow"), $("#recInfinite")]) button.disabled = true;
  try {
    await post(profileURL(id, "record-now"), { infinite });
    toast(infinite ? "Recording started: stop it from the Timers list" : "Recording started");
    guide.timersAt = 0;
    $("#powerDialog").close();
  } catch (error) { powerDialogError(error.message); } finally {
    for (const button of [$("#recNow"), $("#recInfinite")]) button.disabled = false;
  }
}
$("#sleepSet").addEventListener("click", () => powerSleep(true));
$("#sleepOff").addEventListener("click", () => powerSleep(false));
$("#recNow").addEventListener("click", () => powerRecordNow(false));
$("#recInfinite").addEventListener("click", () => powerRecordNow(true));
