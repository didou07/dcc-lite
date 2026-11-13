"use strict";
const devices = { list: [], status: new Map(), currentID: store.get("device", "") };
const currentDevice = () => devices.list.find((p) => p.id === devices.currentID) || null;
const deviceByID = (id) => devices.list.find((p) => p.id === id) || null;
const SITE_COLORS = ["red", "orange", "amber", "green", "teal", "blue", "violet", "pink"];
const sites = { query: "", mode: store.get("sitesMode", "grid") === "list" ? "list" : "grid" };
const lastUsed = () => store.get("siteLastUsed", {});
function touchSite(id) { if (!id) return; const m = lastUsed(); m[id] = Date.now(); const keep = Object.entries(m).sort((a, b) => b[1] - a[1]).slice(0, 200); store.set("siteLastUsed", Object.fromEntries(keep)); }
/* Pinned sites first, then by group, then by name: the same order everywhere (Sites, terminal launcher, Files picker). */
function sortedSites(list = devices.list) {
  return [...list].sort((a, b) => (b.favorite ? 1 : 0) - (a.favorite ? 1 : 0)
    || (a.group || "￿").localeCompare(b.group || "￿", undefined, { sensitivity: "base" })
    || a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true }));
}
const siteMatches = (site, q) => !q || [site.name, site.host, site.group, site.note, site.username].some((v) => (v || "").toLowerCase().includes(q));
async function saveSiteMeta(site, patch) {
  try {
    const { profile } = await api(profileURL(site.id, "meta"), { method: "PATCH", body: JSON.stringify(patch) });
    Object.assign(site, profile);
    renderDeviceSelect(); renderDeviceGrid(); renderTabs?.();
  } catch (error) { toastError(error); }
}
const defaultPort = (profile, service) => {
  if (service === "ftp") return profile?.ftp_port || 21;
  return profile?.telnet_port || 23;
};

async function loadDevices() {
  const { profiles } = await api("/api/v1/profiles");
  devices.list = profiles || [];
  const liveIDs = new Set(devices.list.map((profile) => profile.id));
  for (const id of devices.status.keys()) if (!liveIDs.has(id)) devices.status.delete(id);
  for (const id of deviceChecks.keys()) if (!liveIDs.has(id)) deviceChecks.delete(id);
  for (const id of deviceInfoState.keys()) if (!liveIDs.has(id)) deviceInfoState.delete(id);
  if (!deviceByID(devices.currentID)) {
    devices.currentID = devices.list[0]?.id || "";
    store.set("device", devices.currentID);
  }
  renderDeviceSelect();
  renderDeviceGrid();
}

function renderDeviceSelect() {
  const select = $("#deviceSelect");
  select.replaceChildren();
  if (!devices.list.length) select.append(h("option", { value: "", text: "No devices yet" }));
  for (const profile of devices.list) select.append(h("option", { value: profile.id, text: `${profile.name} · ${profile.host}` }));
  select.value = devices.currentID;
  select.disabled = !devices.list.length;
  updateDeviceDot();
}
function updateDeviceDot() {
  const status = devices.status.get(devices.currentID);
  $("#deviceDot").dataset.state = status?.state || "unknown";
  $("#deviceDot").title = status ? status.message : "Device status not checked yet";
}

function statusChips(profile, status) {
  if (!status) return [h("span", { class: "chip plain", text: "Not checked" })];
  if (status.state === "checking") return [h("span", { class: "chip plain", text: "Checking…" })];
  if (!status.services?.length) return [h("span", { class: "chip off", text: status.message || "Offline" })];
  return status.services.map((service) => h("span", { class: `chip ${service.open ? "on" : "off"}`, title: service.open ? `${service.name} port ${service.port} open` : `${service.name} port ${service.port}: ${service.error || "closed"}` }, `${service.name} ${service.port}`));
}

const boxImages = new Map();
function deviceAvatar(profile) {
  const avatar = h("span", { class: "avatar" }, icon("tv"));
  const key = `${profile.id}|${profile.host}`;
  const show = (img) => { avatar.classList.add("has-img"); avatar.replaceChildren(img); };
  const known = boxImages.get(key);
  if (known === false) return avatar;
  const img = new Image();
  img.alt = ""; img.draggable = false; img.className = "box-img";
  img.addEventListener("load", () => { boxImages.set(key, true); show(img); });
  img.addEventListener("error", () => { if (devices.status.get(profile.id)?.state === "online") boxImages.set(key, false); });
  img.src = profileURL(profile.id, "boximage");
  return avatar;
}
function siteCard(profile) {
  const status = devices.status.get(profile.id);
  const used = lastUsed()[profile.id];
  return h("article", { class: `device-card${profile.id === devices.currentID ? " current" : ""}`, dataset: { id: profile.id, color: profile.color || "" }, onDblclick: (event) => { if (!event.target.closest("button")) openDevice(profile.id, "files"); } },
    h("div", { class: "device-top" },
      deviceAvatar(profile),
      h("div", { class: "ellipsis site-id" },
        h("div", { class: "device-name", text: profile.name }),
        h("div", { class: "device-sub ellipsis", text: profile.host }),
        profile.note ? h("div", { class: "site-note", text: profile.note }) : null),
      h("div", { class: "device-menu" },
        iconButton("star", profile.favorite ? "Unpin from the top" : "Pin to the top", () => saveSiteMeta(profile, { favorite: !profile.favorite }), profile.favorite ? "on" : ""),
        iconButton("list", "Receiver information (image, signal, tuners, disks)", () => toggleDeviceInfo(profile.id)),
        iconButton("refresh", "Check this device", () => testDevice(profile.id)),
        iconButton("edit", "Edit device", () => openDeviceDialog(profile)),
        iconButton("trash", "Delete device", () => deleteDevice(profile)))),
    h("div", { class: "chips", title: status && status.state !== "checking" ? status.message : "" }, statusChips(profile, status),
      profile.start_dir ? h("span", { class: "chip plain folder", title: "Files and Terminal open in this folder", text: profile.start_dir }) : null,
      null),
    h("div", { class: "device-actions" },
      h("button", { class: "btn", type: "button", onClick: () => openDevice(profile.id, "files") }, icon("files"), "Files"),
      h("button", { class: "btn", type: "button", onClick: () => openDevice(profile.id, "terminal") }, icon("terminal"), "Terminal"),
      h("button", { class: "btn", type: "button", title: "Watch what the receiver shows on the TV", onClick: () => openDevice(profile.id, "screen") }, icon("screen"), "Screen"),
      h("button", { class: "btn", type: "button", title: "Channel editor, backup, .ipk install and signal meter", onClick: () => openDevice(profile.id, "tools") }, icon("gear"), "Tools")),
    deviceInfoState.has(profile.id) ? deviceInfoPanel(profile) : null);
}
function relativeTime(ms) {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}
function renderDeviceGrid() {
  if (typeof renderSitesSide === "function") renderSitesSide();
  const wrap = $("#deviceGrid");
  wrap.replaceChildren();
  wrap.dataset.mode = sites.mode;
  $("#deviceEmpty").hidden = devices.list.length > 0;
  $("#devicesRefresh").disabled = !devices.list.length;
  $(".sites-head").hidden = !devices.list.length;
  const q = sites.query.trim().toLowerCase();
  const shown = sortedSites().filter((site) => siteMatches(site, q));
  if (devices.list.length && !shown.length) { wrap.append(h("p", { class: "muted sites-none", text: "No saved device matches your search." })); return; }
  const sections = [], pinned = shown.filter((x) => x.favorite);
  if (pinned.length && pinned.length !== shown.length) sections.push({ title: "Pinned", items: pinned });
  const rest = pinned.length && pinned.length !== shown.length ? shown.filter((x) => !x.favorite) : shown;
  const named = new Map();
  for (const site of rest) { const k = site.group || ""; if (!named.has(k)) named.set(k, []); named.get(k).push(site); }
  for (const [name, items] of named) if (name) sections.push({ title: name, items });
  if (named.has("")) sections.push({ title: named.size > 1 || sections.length ? "Ungrouped" : "", items: named.get("") });
  for (const section of sections) {
    const key = `siteFold.${section.title}`;
    const folded = section.title && store.get(key, false);
    wrap.append(h("section", { class: "site-group" },
      section.title ? h("button", { class: "site-group-title", type: "button", "aria-expanded": String(!folded), title: folded ? "Show this group" : "Hide this group",
        onClick: () => { store.set(key, !folded); renderDeviceGrid(); } }, icon(folded ? "right" : "chev-down"), h("span", { text: section.title }), h("span", { class: "muted", text: String(section.items.length) })) : null,
      folded ? null : h("div", { class: "device-grid" }, section.items.map(siteCard))));
  }
}
$("#sitesSearch").addEventListener("input", (event) => { sites.query = event.target.value; renderDeviceGrid(); });
for (const button of $$("#sitesView button")) button.addEventListener("click", () => {
  sites.mode = button.dataset.mode; store.set("sitesMode", sites.mode);
  for (const b of $$("#sitesView button")) b.setAttribute("aria-checked", String(b === button));
  renderDeviceGrid();
});
for (const b of $$("#sitesView button")) b.setAttribute("aria-checked", String(b.dataset.mode === sites.mode));

const deviceChecks = new Map();
async function testDevice(id) {
  const token = (deviceChecks.get(id) || 0) + 1;
  deviceChecks.set(id, token);
  devices.status.set(id, { state: "checking", message: "Checking…" });
  renderDeviceGrid(); updateDeviceDot();
  try {
    const { connection } = await post(profileURL(id, "test"));
    if (deviceChecks.get(id) === token) devices.status.set(id, connection);
  } catch (error) {
    if (deviceChecks.get(id) === token) devices.status.set(id, { state: "error", message: error.message });
  }
  if (deviceChecks.get(id) === token) { renderDeviceGrid(); updateDeviceDot(); }
}
const deviceInfoState = new Map();
function toggleDeviceInfo(id) {
  if (deviceInfoState.has(id)) { deviceInfoState.delete(id); renderDeviceGrid(); return; }
  deviceInfoState.set(id, { loading: true, data: null, error: "" });
  renderDeviceGrid();
  loadDeviceInfo(id);
}
async function loadDeviceInfo(id, quiet = false) {
  const state = deviceInfoState.get(id);
  if (!state || state.busy) return;
  state.busy = true;
  if (!quiet) { state.loading = true; }
  try {
    const data = await api(profileURL(id, "info"));
    if (deviceInfoState.get(id) === state) {
      state.data = data;
      state.error = "";
      state.updatedAt = Date.now();
    }
  } catch (error) {
    if (deviceInfoState.get(id) === state) state.error = error.message;
  }
  state.loading = false; state.busy = false;
  if (deviceInfoState.get(id) === state) renderDeviceGrid();
}
window.setInterval(() => {
  if (document.hidden || !deviceInfoState.size || !$("#deviceGrid").offsetParent) return;
  for (const id of deviceInfoState.keys()) loadDeviceInfo(id, true);
}, 15000);
function infoBar(label, value, suffix = "") {
  const level = value < 35 ? "low" : value < 60 ? "mid" : "ok";
  return h("div", { class: "info-bar" }, h("span", { class: "muted", text: label }),
    h("span", { class: "track" }, (() => { const fill = h("span", { dataset: { level } }); fill.style.width = `${Math.max(0, Math.min(100, value))}%`; return fill; })()),
    h("span", { text: `${value} %${suffix}` }));
}
function deviceInfoPanel(profile) {
  const state = deviceInfoState.get(profile.id);
  const panel = h("div", { class: "device-info" });
  const head = h("div", { class: "info-head" }, h("strong", { text: "Receiver information" }), h("span", { class: "spacer" }),
    iconButton("refresh", "Refresh the information", () => { if (!state.busy) { state.loading = true; renderDeviceGrid(); loadDeviceInfo(profile.id); } }));
  panel.append(head);
  if (state.loading && !state.data) { panel.append(h("p", { class: "muted small", text: "Reading the receiver…" })); return panel; }
  if (state.error && !state.data) { panel.append(h("p", { class: "form-error", text: state.error })); return panel; }
  if (state.error && state.data) {
    panel.append(h("p", { class: "muted small", title: state.error, text: `Refresh failed; showing the last successful reading from ${state.updatedAt ? new Date(state.updatedAt).toLocaleString() : "an earlier update"}. ${state.error}` }));
  } else if (state.updatedAt) {
    panel.append(h("p", { class: "muted small", text: `Updated ${new Date(state.updatedAt).toLocaleTimeString()}` }));
  }
  const d = state.data || {};
  const grid = h("dl", { class: "info-grid" });
  const row = (label, value) => { if (value) grid.append(h("dt", { text: label }), h("dd", { text: value })); };
  row("Model", d.model); row("Image", d.image); row("Enigma2", d.enigma); row("Kernel", d.kernel); row("Chipset", d.chipset);
  row("Uptime", d.uptime); row("Memory", d.memory);
  row("Temperature", d.temperature != null ? `${d.temperature} °C` : "");
  panel.append(grid);
  if (d.temperature == null) panel.append(h("p", { class: "muted small", text: "Temperature appears when Files is connected to this device." }));
  if (d.signal) {
    const bars = [d.signal.tuner ? h("span", { class: "muted small", text: `Signal · ${d.signal.tuner}${d.signal.standby ? " (standby)" : ""}` }) : null,
      infoBar("SNR", d.signal.snr, d.signal.snr_db ? ` · ${d.signal.snr_db} dB` : "")];
    if (d.signal.has_agc) bars.push(infoBar("AGC", d.signal.agc));
    if (d.signal.has_ber) bars.push(h("span", { class: "muted small", text: `BER · ${d.signal.ber}` }));
    panel.append(h("div", { class: "info-bars", "aria-label": "Tuner signal" }, ...bars));
  } else {
    panel.append(h("p", { class: "muted small", text: "No tuner signal now (the receiver may be in standby)." }));
  }
  if ((d.tuners || []).length) {
    panel.append(h("ul", { class: "info-list", "aria-label": "Tuners" }, ...d.tuners.map((t) => {
      const using = [t.live && `live: ${t.live}`, t.rec && `recording: ${t.rec}`, t.stream && `stream: ${t.stream}`].filter(Boolean).join(" · ");
      return h("li", {}, h("strong", { text: t.name }), ` ${t.type || ""}`, using ? h("span", { class: "muted", text: ` — ${using}` }) : null);
    })));
  }
  if ((d.disks || []).length) {
    panel.append(h("ul", { class: "info-list", "aria-label": "Disks" }, ...d.disks.map((k) => h("li", {}, h("strong", { text: k.name || "Disk" }), ` ${k.text}`))));
  }
  return panel;
}
async function testAllDevices() { await Promise.all(devices.list.map((profile) => testDevice(profile.id))); }

async function deleteDevice(profile) {
  const confirmed = await ask({ title: "Delete device", message: `Delete “${profile.name}” (${profile.host})? Its saved password is removed too. Open sessions are closed.`, input: false, ok: "Delete", danger: true });
  if (!confirmed) return;
  try {
    await api(profileURL(profile.id), { method: "DELETE" });
    devices.status.delete(profile.id);
    if (devices.currentID === profile.id) { await fileDisconnect(true); closeTabsOfDevice(profile.id); devices.currentID = ""; }
    await loadDevices();
    store.set("device", devices.currentID);
    toast(`${profile.name} deleted`);
  } catch (error) { toastError(error); }
}

const deviceDialog = { editing: null };
function openDeviceDialog(profile = null) {
  deviceDialog.editing = profile;
  $("#deviceDialogTitle").textContent = profile ? "Edit device" : "Add device";
  $("#devName").value = profile?.name || "";
  $("#devHost").value = profile?.host || "";
  $("#devUser").value = profile?.username || "root";
  $("#devPass").value = "";
  $("#devPass").type = "password";
  $("#devPass").placeholder = profile ? (profile.credential_configured ? "Saved" : "No password") : "dreambox";
  $("#devFtp").value = profile?.ftp_port || "";
  $("#devWeb").value = profile?.port || "";
  $("#devTelnet").value = profile?.telnet_port || "";
  $(".advanced", $("#deviceDialog")).open = Boolean(profile && (profile.ftp_port || profile.telnet_port || profile.port || profile.scheme === "https"));
  $("#deviceError").hidden = true;
  openDialog($("#deviceDialog"));
  $("#devName").focus();
}
function portValue(selector) {
  const raw = $(selector).value.trim();
  if (!raw) return 0;
  const n = Number(raw);
  if (!Number.isInteger(n) || n < 1 || n > 65535) throw new Error("Ports must be numbers between 1 and 65535");
  return n;
}
$("#deviceForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = $("#deviceError");
  error.hidden = true;
  let body;
  const old = deviceDialog.editing || {};
  try {
    body = {
      name: $("#devName").value.trim(), host: $("#devHost").value.trim(), username: $("#devUser").value.trim() || "root",
      password: $("#devPass").value, ftp_port: portValue("#devFtp"), telnet_port: portValue("#devTelnet"),
      web_port: portValue("#devWeb"),
      tls_insecure_skip_verify: false,
      group: old.group || "", color: old.color || "", favorite: Boolean(old.favorite),
      start_dir: old.start_dir || "", note: old.note || "",
    };
    body.web_https = body.web_port === 443 || (old.scheme === "https" && body.web_port === (old.port || 0));
  } catch (problem) { error.textContent = problem.message; error.hidden = false; return; }
  const editing = deviceDialog.editing;
  $("#deviceSave").disabled = true;
  try {
    const result = editing
      ? await api(profileURL(editing.id), { method: "PUT", body: JSON.stringify(body) })
      : await post("/api/v1/profiles", body);
    $("#deviceDialog").close();
    const savedID = result.profile.id;
    if (editing && editing.id === devices.currentID) { await fileDisconnect(true); closeTabsOfDevice(editing.id); }
    if (editing) {
      devices.status.delete(editing.id);
      deviceChecks.set(editing.id, (deviceChecks.get(editing.id) || 0) + 1);
      deviceInfoState.delete(editing.id);
    }
    await loadDevices();
    if (!editing || !devices.currentID) devices.currentID = savedID;
    store.set("device", devices.currentID);
    renderDeviceSelect(); renderDeviceGrid();
    toast(editing ? "Device updated" : "Device added");
    testDevice(savedID);
    if (!editing && devices.list.length === 1) setCurrentDevice(savedID);
  } catch (problem) { error.textContent = problem.message; error.hidden = false; }
  finally { $("#deviceSave").disabled = false; }
});
$("#deviceCancel").addEventListener("click", () => $("#deviceDialog").close());
$("#devPassToggle").addEventListener("click", () => { const f = $("#devPass"); f.type = f.type === "password" ? "text" : "password"; });

$("#deviceAdd").addEventListener("click", () => openDeviceDialog());
$("#deviceAddEmpty").addEventListener("click", () => openDeviceDialog());
$("#devicesRefresh").addEventListener("click", testAllDevices);

function openDevice(id, targetView) {
  touchSite(id);
  setCurrentDevice(id, Boolean(targetView));
  if (targetView) showView(targetView);
}
function refreshCurrentDeviceView() {
  switch (view.current) {
    case "files": filesEnter(); break;
    case "lists": listsEnter(); break;
    case "ipk": ipkEnter(); break;
    case "terminal": terminalEnter(); break;
    case "screen": screenEnter(); break;
    case "signal": signalEnter(); break;
  }
}
function setCurrentDevice(id, deferViewRefresh = false) {
  const changed = id !== devices.currentID;
  devices.currentID = id;
  store.set("device", id);
  $("#deviceSelect").value = id;
  updateDeviceDot();
  renderDeviceGrid();
  if (changed) {
    fileDisconnect(true).then(() => { if (!deferViewRefresh) refreshCurrentDeviceView(); });
  } else if (!deferViewRefresh) {
    refreshCurrentDeviceView();
  }
  if (id && !devices.status.has(id)) testDevice(id);
}
$("#deviceSelect").addEventListener("change", (event) => setCurrentDevice(event.target.value));

const view = { current: "devices" };
const TOOL_PAGES = [
  { id: "editor", label: "Channel editor", icon: "list", view: "lists" },
  { id: "backup", label: "Backup & restore", icon: "archive", view: "lists" },
  { id: "ipk", label: "Install .ipk", icon: "box", view: "ipk" },
  { id: "signal", label: "Signal meter", icon: "signal", view: "signal" },
];
const tools = { current: store.get("tool", "editor") };
if (!TOOL_PAGES.some((t) => t.id === tools.current)) tools.current = "editor";
const toolViews = ["lists", "ipk", "signal"];
function updateToolsNav() {
  for (const nav of $$("[data-toolsnav]")) {
    nav.replaceChildren(...TOOL_PAGES.map((t) => h("button", {
      type: "button", role: "tab", class: "toolsnav-btn", "aria-selected": String(t.id === tools.current),
      onClick: () => showTool(t.id),
    }, icon(t.icon), t.label)));
  }
}
function showTool(id) {
  const page = TOOL_PAGES.find((t) => t.id === id) || TOOL_PAGES[0];
  tools.current = page.id;
  store.set("tool", page.id);
  if (page.view === "lists" && typeof listsMode === "function") listsMode(page.id);
  showView(page.view);
}
function showView(name) {
  if (name === "tools") name = TOOL_PAGES.find((t) => t.id === tools.current)?.view || "lists";
  if (!["devices", "files", "terminal", "screen", "logs", ...toolViews].includes(name)) name = "devices";
  if (view.current === "screen" && name !== "screen") screenStop();
  if (view.current === "signal" && name !== "signal") signalStop();
  if (view.current === "logs" && name !== "logs") logsLeave();
  view.current = name;
  if (name === "ipk") tools.current = "ipk";
  else if (name === "signal") tools.current = "signal";
  else if (name === "lists" && (tools.current === "ipk" || tools.current === "signal")) tools.current = "editor";
  document.body.dataset.view = name;
  const top = toolViews.includes(name) ? "tools" : name;
  for (const tab of $$("#mainTabs .tab")) tab.setAttribute("aria-selected", String(tab.dataset.view === top));
  for (const section of $$("main > .view")) section.hidden = section.id !== `view-${name}`;
  store.set("view", name);
  if (toolViews.includes(name)) { store.set("tool", tools.current); updateToolsNav(); }
  if (name === "files") filesEnter();
  if (name === "lists") { if (typeof listsMode === "function") listsMode(tools.current === "backup" ? "backup" : "edit"); listsEnter(); }
  if (name === "ipk") ipkEnter();
  if (name === "terminal") terminalEnter();
  if (name === "screen") screenEnter();
  if (name === "signal") signalEnter();
  if (name === "logs") logsEnter();
}
for (const tab of $$("#mainTabs .tab")) tab.addEventListener("click", () => showView(tab.dataset.view));
