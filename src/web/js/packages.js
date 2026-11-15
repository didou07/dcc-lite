"use strict";

// ---- Packages: browse, install and remove software (opkg via OpenWebif) ----
const pk = { profileID: "", mode: "all", q: "", gen: 0, busy: false, timer: 0, upgradable: 0 };

function pkError(message) {
  const node = $("#pkError");
  node.textContent = message || "";
  node.hidden = !message;
}
function pkSetBusy(busy) {
  pk.busy = busy;
  for (const id of ["#pkUpdate", "#pkUpgrade", "#pkSearch"]) $(id).disabled = busy || (id === "#pkUpgrade" && !pk.upgradable);
  for (const button of $$("#pkList button")) button.disabled = busy;
}

function pkRow(p) {
  const buttons = [];
  if (!p.installed) buttons.push(h("button", { class: "btn sm primary", type: "button", onClick: () => pkRun("install", p) }, icon("download"), "Install"));
  else {
    if (p.upgrade_from) buttons.push(h("button", { class: "btn sm primary", type: "button", title: `Update from ${p.upgrade_from}`, onClick: () => pkRun("install", p) }, icon("upload"), "Update"));
    buttons.push(h("button", { class: "btn sm danger", type: "button", onClick: () => pkRun("remove", p) }, icon("trash"), "Remove"));
  }
  return h("div", { class: "gd-item pk-item" },
    h("div", { class: "gd-main" },
      h("div", { class: "gd-title" }, h("span", { class: "pk-name", text: p.name }),
        h("span", { class: "muted small", text: p.version }),
        p.installed ? h("span", { class: "badge ok", text: p.upgrade_from ? "Update available" : "Installed" }) : null,
        p.section ? h("span", { class: "badge tag", text: p.section }) : null),
      p.description ? h("div", { class: "gd-short", text: p.description }) : null),
    h("div", { class: "gd-acts" }, ...buttons));
}

async function pkLoad(refresh = false) {
  const id = pk.profileID, gen = ++pk.gen;
  if (!id) return;
  pkError("");
  $("#pkStatus").textContent = "Loading the package list…";
  try {
    const params = new URLSearchParams({ view: pk.mode, q: pk.q });
    if (refresh) params.set("refresh", "1");
    const data = await api(profileURL(id, `packages?${params}`));
    if (gen !== pk.gen || id !== pk.profileID) return;
    pk.upgradable = data.upgradable || 0;
    $("#pkUpgrade").disabled = pk.busy || !pk.upgradable;
    const list = $("#pkList");
    const rows = (data.packages || []).map(pkRow);
    list.replaceChildren(...(rows.length ? rows : [h("p", { class: "muted gd-empty", text: pk.mode === "updates" ? "Everything is up to date." : "No package matches." })]));
    const shown = rows.length;
    $("#pkStatus").textContent = `${data.installed} installed · ${data.upgradable} update${data.upgradable === 1 ? "" : "s"} available · ${data.total} in the feeds` + (data.matches > shown ? ` · showing the first ${shown} of ${data.matches}` : "");
  } catch (error) {
    if (gen !== pk.gen || id !== pk.profileID) return;
    $("#pkList").replaceChildren();
    $("#pkStatus").textContent = "";
    pkError(error.message);
  }
}

function pkShowLog(lines, failed) {
  const log = $("#pkLog");
  log.hidden = !lines.length;
  log.textContent = lines.join("\n");
  log.dataset.state = failed ? "bad" : "ok";
  log.scrollTop = log.scrollHeight;
}

async function pkRun(command, p) {
  if (pk.busy) return;
  const target = p ? p.name : "";
  const label = { install: p && p.installed ? `Update ${target}?` : `Install ${target}?`, remove: `Remove ${target}?`, update: "Refresh the package lists?", upgrade: "Upgrade all packages?" }[command];
  const message = {
    install: "The receiver downloads it from its feeds and installs it.",
    remove: "The receiver removes this package.",
    update: "Downloads the newest package lists.",
    upgrade: `Installs all ${pk.upgradable} updates. This can take several minutes.`,
  }[command];
  if (command !== "update") {
    const ok = await ask({ title: label, message, input: false, ok: command === "remove" ? "Remove" : command === "install" ? (p.installed ? "Update" : "Install") : "Upgrade all", danger: command === "remove" });
    if (!ok) return;
  }
  pkError("");
  let problem = "";
  pkSetBusy(true);
  pkShowLog([`${command}${target ? ` ${target}` : ""}: working on the receiver…`], false);
  $("#pkStatus").textContent = "The receiver is working. Keep this page open.";
  try {
    const data = await post(profileURL(pk.profileID, "packages/action"), { command, package: target });
    pkShowLog(data.lines && data.lines.length ? data.lines : ["Done."], !data.ok);
    if (data.ok) toast(command === "remove" ? "Removed" : "Done. Enigma2 may need a restart.");
    else problem = "The receiver reported a problem. See the log below.";
  } catch (error) {
    pkShowLog([], false);
    problem = error.message;
  } finally {
    pkSetBusy(false);
    await pkLoad(command === "update");
    if (problem) pkError(problem);
  }
}

function packagesEnter() {
  const device = currentDevice();
  if (!device) {
    pk.profileID = "";
    pk.gen++;
    $("#pkList").replaceChildren();
    $("#pkStatus").textContent = "";
    pkError("Choose a device in the list at the top.");
    return;
  }
  $("#pkDevice").textContent = `${device.name} · ${device.host}`;
  if (device.id === pk.profileID && $("#pkList").children.length) return;
  pk.profileID = device.id;
  pk.mode = "all";
  pk.q = "";
  $("#pkSearch").value = "";
  for (const b of $$("#pkView button")) b.setAttribute("aria-pressed", String(b.dataset.pk === "all"));
  pkShowLog([], false);
  pkLoad();
}

$("#pkSearch").addEventListener("input", () => {
  window.clearTimeout(pk.timer);
  pk.timer = window.setTimeout(() => { pk.q = $("#pkSearch").value.trim(); pkLoad(); }, 300);
});
$("#pkView").addEventListener("click", (event) => {
  const b = event.target.closest("button[data-pk]");
  if (!b || b.dataset.pk === pk.mode) return;
  pk.mode = b.dataset.pk;
  for (const x of $$("#pkView button")) x.setAttribute("aria-pressed", String(x === b));
  pkLoad();
});
$("#pkUpdate").addEventListener("click", () => pkRun("update"));
$("#pkUpgrade").addEventListener("click", () => pkRun("upgrade"));
