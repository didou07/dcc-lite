"use strict";

const sc = { profileID: "", gen: 0, busy: false, info: null, timer: 0 };
const SC_POLL_MS = 12000;

function scError(message) {
  const node = $("#scError");
  node.hidden = !message;
  node.textContent = message || "";
}

function scResult(message, bad = false) {
  const node = $("#scResult");
  const parts = [].concat(message || []).filter(Boolean);
  node.hidden = !parts.length;
  node.replaceChildren(...parts.flatMap((part, index) => [index ? " " : "", h("span", { text: part })]).filter(Boolean));
  node.classList.toggle("is-error", Boolean(bad));
}

function scStatus(text) {
  $("#scSpinText").textContent = text || "";
}

function scSetBusy(busy) {
  sc.busy = busy;
  const root = $("#view-softcam");
  root.classList.toggle("is-busy", busy);
  root.querySelector(".sc-cams-card")?.setAttribute("aria-busy", String(busy));
  $("#scReload").classList.toggle("busy", busy);
  $("#scSpin").hidden = !busy;
  if (!busy) scStatus("");
  $("#view-softcam .sc-wait")?.classList.toggle("on", busy);
  // The Tools tab gear turns while this page works.
  $('#mainTabs .tab[data-view="tools"]')?.classList.toggle("busy", busy);
  for (const button of $$("#view-softcam button[data-sc]")) button.disabled = busy;
  $("#scKillAll").disabled = busy || !(sc.info?.procs || []).length;
}

const scNorm = (value) => String(value || "").toLowerCase().replace(/[^a-z0-9]/g, "");
const scLabel = (name) => String(name || "").replace(/^softcam\./, "") || name;
const scIsNone = (name) => scLabel(name).toLowerCase() === "none";
const scSize = (bytes) => bytes < 1024 ? `${bytes} B` : bytes < 1048576 ? `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KB` : `${(bytes / 1048576).toFixed(1)} MB`;

const scDir = (value) => String(value || "").replace(/\/+$/, "");
const scActiveName = (info) => String(info.link || "").split("/").pop();

function scScore(info, name, process) {
  const real = name === "softcam" && info.link ? scActiveName(info) : name;
  const own = scNorm(real === "softcam" ? "" : String(real).replace(/^softcam\./, ""));
  const proc = scNorm(process.name);
  let score = 0;
  if (own && own !== "none" && proc && (proc === own || proc.startsWith(own) || own.startsWith(proc))) score += 3;
  const conf = scDir((info.confs || {})[real]);
  const procConf = scDir(process.conf);
  const confKnown = Boolean(conf && procConf);
  if (confKnown && conf === procConf) score += 3;
  const bin = (info.bins || {})[real];
  if (bin && !(confKnown && conf !== procConf)) {
    const base = scNorm(bin.split("/").pop());
    if (base && (proc === base || proc.startsWith(base) || base.startsWith(proc) || scNorm(String(process.bin || "").split("/").pop()) === base)) score += 1;
  }
  return score;
}

function scAssign(info) {
  const active = scActiveName(info);
  const result = new Map((info.cams || []).map((name) => [name, []]));
  for (const process of info.procs || []) {
    let best = null;
    let top = 0;
    for (const name of info.cams || []) {
      if (name === "softcam" && active && (info.cams || []).includes(active)) continue;
      const score = scScore(info, name, process) + (name === active && name !== "softcam" ? 0.5 : 0);
      if (score > top) { top = score; best = name; }
    }
    if (best && top >= 1) result.get(best).push(process);
  }
  return result;
}

function scProcsOf(info, name) {
  const real = name === "softcam" && info.link ? scActiveName(info) : name;
  return scAssign(info).get(real) || [];
}

function scOpenWeb(web) {
  const device = currentDevice();
  if (!device || !web || !web.port) return;
  let host = String(device.host || "").trim().replace(/^https?:\/\//i, "").replace(/\/$/, "");
  if (host.startsWith("[") && host.endsWith("]")) host = host.slice(1, -1);
  if (host.includes(":")) host = `[${host}]`;
  window.open(`${web.tls ? "https" : "http"}://${host}:${web.port}/`, "_blank", "noopener,noreferrer");
}

function scWebFor(info, processes, camName) {
  const web = (info.web || []).filter((item) => Number(item.port) > 0);
  if (!web.length) return null;
  const strip = (dir) => String(dir || "").replace(/\/+$/, "");
  const ports = new Set(processes.flatMap((process) => process.ports || []));
  if (ports.size) {
    const dirs = [...processes.map((process) => process.conf), (info.confs || {})[camName]].filter(Boolean).map(strip);
    const live = web.filter((item) => ports.has(Number(item.port)));
    return live.find((item) => dirs.includes(strip(item.dir))) || live[0] || null;
  }
  const dirs = [...processes.map((process) => process.conf), (info.confs || {})[camName]].filter(Boolean).map(strip);
  const byDir = web.find((item) => dirs.includes(strip(item.dir)));
  if (byDir) return byDir;
  if (processes.some((process) => process.conf)) return null;
  const names = processes.map((process) => scNorm(process.name));
  const byName = web.find((item) => names.includes(scNorm(item.file.split("/").pop().replace(/\.conf$/i, ""))));
  if (byName) return byName;
  return processes.length === 1 && web.length === 1 ? web[0] : null;
}

async function scCopy(value) {
  toast(await clipboardWrite(value) ? "Copied" : "Could not copy", "info");
}

function scButton(iconName, text, title, action, options = {}) {
  const { extra = "" } = options;
  return h("button", {
    class: `btn sm ${extra}`.trim(),
    type: "button",
    title,
    "aria-label": text ? undefined : title,
    dataset: { sc: "1" },
    disabled: sc.busy,
    onClick: action,
  }, icon(iconName), text);
}

function scCamRow({ name, live, active, boot, processes, web, stray = false }) {
  const label = name === "softcam"
    ? (sc.info?.link ? "softcam" : "softcam (start script)")
    : scLabel(name);
  const conf = processes.find((process) => process.conf)?.conf || (sc.info?.confs || {})[name];
  const command = processes[0]?.cmd || processes[0]?.bin || "";
  const pid = processes.length ? processes.map((process) => process.pid).join(", ") : "";
  const subline = live
    ? [`PID ${pid}`, command].filter(Boolean).join(" · ")
    : (scIsNone(name) ? "" : [conf ? `Config: ${conf}` : "", (sc.info?.bins || {})[name] || ""].filter(Boolean).join(" · "));

  const none = scIsNone(name);
  const actions = [];
  if (web) actions.push(scButton("ft-web", "WebIf", `Open WebIf · ${web.port}`, () => scOpenWeb(web), { extra: "sc-web" }));
  if (!stray && !none) {
    if (!live) {
      actions.push(scButton("play", "Start", `Start ${label}`, () => scAction("start", name)));
    } else {
      actions.push(scButton("refresh", "Restart", `Restart ${label}`, () => scAction("restart", name)));
      actions.push(scButton("stop", "Stop", `Stop ${label}`, () => scAction("stop", name), { extra: "danger" }));
    }
  }
  if (live && processes.length) {
    actions.push(scButton("x", stray ? "Kill" : "", `Force kill ${label}`, () => scKill(processes.map((process) => process.pid), label), { extra: stray ? "danger" : "danger sq" }));
  }
  if (!stray && name !== "softcam" && !active) {
    actions.push(scButton("swap", "Make active", `Make ${label} active`, () => scAction("switch", name), { extra: "accent" }));
  }

  return h("article", { class: "sc-cam", dataset: { on: String(live) } },
    h("div", { class: "sc-cam-main" },
      h("span", { class: "sc-dot", dataset: { on: String(live) }, title: live ? "Running" : "Not running", "aria-label": live ? "Running" : "Not running" }),
      h("div", { class: "sc-ident" },
        h("span", { class: "sc-name", text: label }),
        active ? h("span", { class: "sc-tag active", text: "Active" }) : null,
        boot ? h("span", { class: "sc-tag", text: "Starts at boot" }) : null,
        stray ? h("span", { class: "sc-tag", text: "Manual process" }) : null,
        subline ? h("div", { class: "sc-subline", text: subline }) : null),
      h("div", { class: "sc-actions" }, ...actions)));
}

function scRender(info) {
  sc.info = info;
  const processes = info.procs || [];
  const boot = info.boot || [];
  const activeName = (info.link || "").split("/").pop();
  const camNames = (info.cams || []).filter((name) => !(name === "softcam" && activeName && info.cams.includes(activeName)));
  const assigned = scAssign(info);
  const rows = camNames.map((name) => ({ name, processes: assigned.get(name) || [] }));
  const matched = new Set(rows.flatMap((row) => row.processes.map((process) => process.pid)));
  const stray = processes.filter((process) => !matched.has(process.pid));
  const nodes = rows.map((row) => {
    const live = row.processes.length > 0;
    const active = row.name !== "softcam" && row.name === activeName;
    const isBoot = boot.includes(row.name) || (active && boot.includes("softcam"));
    return scCamRow({
      name: row.name,
      live,
      active,
      boot: Boolean(isBoot),
      processes: row.processes,
      web: live ? scWebFor(info, row.processes, row.name) : null,
    });
  });
  for (const process of stray) {
    nodes.push(scCamRow({
      name: process.name,
      live: true,
      active: false,
      boot: false,
      processes: [process],
      web: scWebFor(info, [process], process.name),
      stray: true,
    }));
  }
  $("#scCams").replaceChildren(...nodes);
  $("#scEmpty").hidden = nodes.length > 0;
  const running = rows.filter((row) => row.processes.length).length + new Set(stray.map((process) => scNorm(process.name))).size;
  $("#scProcCount").textContent = running ? `${running} running` : "";
  $("#scKillAll").hidden = running < 2;
  $("#scKillAll").disabled = sc.busy;

  const files = [...(info.files || [])].sort((a, b) => a.path.localeCompare(b.path));
  $("#scFilesCount").textContent = files.length ? String(files.length) : "0";
  $("#scFiles").replaceChildren(...files.map((file) => {
    const split = file.path.lastIndexOf("/");
    return h("div", { class: "sc-file" },
      icon(/key|\.bin$|\.cw$/i.test(file.path.slice(split + 1)) ? "ft-key" : "ft-config"),
      h("span", { class: "sc-path" },
        h("span", { class: "muted", text: file.path.slice(0, split + 1) }),
        h("b", { text: file.path.slice(split + 1) })),
      h("span", { class: "sc-size", text: scSize(file.size || 0) }),
      h("button", { class: "icon-btn xs", type: "button", title: "Copy path", "aria-label": "Copy path", onClick: () => scCopy(file.path) }, icon("copy")),
      h("button", { class: "icon-btn xs", type: "button", title: "Edit", "aria-label": "Edit", dataset: { sc: "1" }, onClick: () => scEdit(file.path) }, icon("edit")));
  }));
}

async function scLoad({ quiet = false } = {}) {
  const device = currentDevice();
  if (!device) {
    scError("Select a receiver first.");
    return null;
  }
  if (quiet && (sc.busy || sc.polling)) return null;
  const generation = ++sc.gen;
  if (!quiet) {
    scError("");
    scSetBusy(true);
    scStatus(sc.info ? "Refreshing…" : "Reading…");
  }
  if (quiet) sc.polling = true;
  try {
    const info = await api(profileURL(device.id, "softcam/info"));
    if (generation === sc.gen) {
      if (!quiet || JSON.stringify(info) !== JSON.stringify(sc.info)) scRender(info);
      return info;
    }
  } catch (error) {
    if (generation === sc.gen && !quiet) scError(error.message);
  } finally {
    if (quiet) sc.polling = false;
    if (generation === sc.gen && !quiet) scSetBusy(false);
  }
  return null;
}

function scWatch() {
  clearInterval(sc.timer);
  sc.timer = setInterval(() => {
    if (view.current !== "softcam" || document.hidden || sc.busy) return;
    scLoad({ quiet: true });
  }, SC_POLL_MS);
}
function scUnwatch() { clearInterval(sc.timer); sc.timer = 0; }
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && view.current === "softcam" && !sc.busy) scLoad({ quiet: true });
});

async function scAction(action, cam) {
  const device = currentDevice();
  if (!device || sc.busy) return;
  const target = cam === "softcam" && sc.info?.link ? sc.info.link.split("/").pop() : cam;
  const label = scLabel(target);
  if (action === "stop" || action === "switch") {
    const stop = action === "stop";
    const accepted = await ask({
      title: stop ? "Stop softcam" : "Change active softcam",
      message: stop ? `Stop ${label}? Encrypted channels will stop.` : `Stop the running softcam and start ${label}?`,
      input: false,
      ok: stop ? "Stop" : "Switch",
      danger: stop,
    });
    if (!accepted) return;
  }

  scError("");
  scResult("");
  scSetBusy(true);
  scStatus({ start: "Starting {1}…", stop: "Stopping {1}…", restart: "Restarting {1}…", switch: "Switching to {1}…" }[action].replace("{1}", label));
  let result;
  try {
    result = await post(profileURL(device.id, "softcam/action"), { action, cam: cam === "softcam" ? "" : cam });
  } catch (error) {
    scError(error.message);
    scSetBusy(false);
    return;
  }

  scSetBusy(false);
  const info = await scLoad();
  const state = info || sc.info || {};
  const live = scProcsOf(state, target).length > 0;
  const activeName = String(state.link || "").split("/").pop();
  const isActive = activeName === target;
  const ok = action === "stop" ? !live
    : action === "switch" ? isActive && !result.no_link
    : live;
  let message = {
    start: ok ? `${label} started.` : `${label} start command completed, but the process was not detected.`,
    stop: ok ? `${label} stopped.` : `${label} is still running.`,
    restart: ok ? `${label} restarted.` : `${label} restart command completed, but the process was not detected.`,
    switch: ok ? `${label} is now active.` : result.no_link
      ? `${label} was started, but the receiver's /etc/init.d/softcam is not a symbolic link, so the active cam was not changed.`
      : `${label} did not become the active running cam.`,
  }[action];
  scResult([message, result.output], !ok);
  toast(message, ok ? "info" : "error");
}

async function scKill(pids, label, all = false) {
  const device = currentDevice();
  if (!device || sc.busy) return;
  const target = all ? "all softcam processes" : label;
  const accepted = await ask({
    title: all ? "Kill all softcams" : "Kill softcam process",
    message: `Force ${target} to end? Encrypted channels will stop until a softcam is started again.`,
    input: false,
    ok: "Kill",
    danger: true,
  });
  if (!accepted) return;
  scError("");
  scResult("");
  scSetBusy(true);
  try {
    const request = all ? { action: "killall" } : { action: "kill", pids };
    const result = await post(profileURL(device.id, "softcam/action"), request);
    const remaining = (result.procs || []).filter((process) => all || pids.includes(process.pid));
    const ok = remaining.length === 0;
    const message = ok
      ? (all ? "All softcams have been stopped." : `${target} has been stopped.`)
      : `Still running: ${remaining.map((process) => `${process.name} (${process.pid})`).join(", ")}.`;
    scResult(message, !ok);
    toast(message, ok ? "info" : "error");
  } catch (error) {
    scError(error.message);
  } finally {
    scSetBusy(false);
  }
  // Reload without wiping the message just shown.
  const node = $("#scResult");
  const keep = node.hidden ? "" : node.textContent;
  const bad = node.classList.contains("is-error");
  await scLoad();
  if (keep) scResult(keep, bad);
}

async function scEdit(file) {
  try {
    if (typeof syncFilePort === "function") syncFilePort();
    if (!files.sessionID && !files.connecting) await fileConnect();
    if (!files.sessionID) {
      scError("Connect to the receiver first in Files.");
      return;
    }
    openEditor("remote", file);
  } catch (error) {
    scError(error.message);
  }
}

function softcamEnter() {
  const device = currentDevice();
  if (!device) {
    sc.profileID = "";
    sc.gen++;
    sc.info = null;
    scUnwatch();
    scSetBusy(false);
    scError("Select a receiver first.");
    scResult("");
    $("#scCams").replaceChildren();
    $("#scFiles").replaceChildren();
    $("#scEmpty").hidden = true;
    $("#scProcCount").textContent = "";
    $("#scKillAll").hidden = true;
    return;
  }
  if (device.id !== sc.profileID) {
    sc.profileID = device.id;
    sc.info = null;
    $("#scCams").replaceChildren();
    $("#scFiles").replaceChildren();
    $("#scProcCount").textContent = "";
    scResult("");
  }
  scWatch();
  scLoad();
}

$("#scReload").addEventListener("click", () => scLoad());
$("#scKillAll").addEventListener("click", () => scKill([], "", true));
