"use strict";
/* ───────────── Power dialog ─────────────
   Standby, wake up, deep standby, reboot and restart of the receiver's interface go through
   the receiver's web interface (powerstate); the risky ones ask for confirmation. The same
   dialog lists the receiver's MultiBoot slots so it can restart into another image. */
async function powerAction(action, doing, confirm = null, report = remoteError) {
  const id = screenView.profileID;
  if (!id) return false;
  if (confirm && !(await ask({ title: confirm.title, message: confirm.message, input: false, ok: confirm.ok, danger: true }))) return false;
  report("");
  try {
    const data = await post(`/api/v1/profiles/${encodeURIComponent(id)}/power`, { action });
    // OpenWebif answers with the standby state from before the action ran, so it is not shown;
    // the poll below reads the real state once the receiver has switched.
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
/** Runs one power-dialog action; the dialog closes only when the action really went through
    (a cancelled confirmation leaves it open). */
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
  openDialog($("#powerDialog"));
  $("#powerActions").querySelector("button")?.focus({ preventScroll: true });
}

async function powerLoadSlots(id) {
  const gen = ++powerView.gen;
  const box = $("#powerSlots"), note = $("#powerSlotsNote");
  box.replaceChildren();
  note.textContent = "Looking for MultiBoot slots…";
  try {
    const data = await api(`/api/v1/profiles/${encodeURIComponent(id)}/multiboot`);
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
    await post(`/api/v1/profiles/${encodeURIComponent(id)}/multiboot`, { slot: slot.slot, code: slot.code });
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
