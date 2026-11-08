"use strict";
/* ───────────── Devices ───────────── */
const devices = { list: [], status: new Map(), currentID: store.get("device", "") };
const currentDevice = () => devices.list.find((p) => p.id === devices.currentID) || null;
const deviceByID = (id) => devices.list.find((p) => p.id === id) || null;
const defaultPort = (profile, service) => {
  if (service === "ftp") return profile?.ftp_port || 21;
  return profile?.telnet_port || 23;
};

async function loadDevices() {
  const { profiles } = await api("/api/v1/profiles");
  devices.list = profiles || [];
  if (!deviceByID(devices.currentID)) devices.currentID = devices.list[0]?.id || "";
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

function renderDeviceGrid() {
  const grid = $("#deviceGrid");
  grid.replaceChildren();
  $("#deviceEmpty").hidden = devices.list.length > 0;
  $("#devicesRefresh").disabled = !devices.list.length;
  $("#devicesExport").disabled = !devices.list.length;
  for (const profile of devices.list) {
    const status = devices.status.get(profile.id);
    const card = h("article", { class: `device-card${profile.id === devices.currentID ? " current" : ""}`, dataset: { id: profile.id } },
      h("div", { class: "device-top" },
        h("span", { class: "avatar" }, icon("tv"), h("span", { class: "dot", dataset: { state: status?.state || "unknown" } })),
        h("div", { class: "ellipsis" }, h("div", { class: "device-name", text: profile.name }), h("div", { class: "device-sub ellipsis", text: `${profile.host} · ${profile.username || "root"}` })),
        h("div", { class: "device-menu" },
          iconButton("list", "Receiver information (image, signal, tuners, disks)", () => toggleDeviceInfo(profile.id)),
          iconButton("refresh", "Check this device", () => testDevice(profile.id)),
          iconButton("edit", "Edit device", () => openDeviceDialog(profile)),
          iconButton("trash", "Delete device", () => deleteDevice(profile)))),
      h("div", { class: "chips", title: status && status.state !== "checking" ? status.message : "" }, statusChips(profile, status)),
      h("div", { class: "device-actions" },
        h("button", { class: "btn", type: "button", onClick: () => openDevice(profile.id, "files") }, icon("files"), "Files"),
        h("button", { class: "btn", type: "button", onClick: () => openDevice(profile.id, "terminal") }, icon("terminal"), "Terminal"),
        h("button", { class: "btn", type: "button", title: "Watch what the receiver shows on the TV", onClick: () => openDevice(profile.id, "screen") }, icon("screen"), "Screen"),
        h("button", { class: "btn", type: "button", title: "Channel editor, backup, .ipk install and signal meter", onClick: () => openDevice(profile.id, "tools") }, icon("gear"), "Tools")),
      deviceInfoState.has(profile.id) ? deviceInfoPanel(profile) : null);
    grid.append(card);
  }
}

async function testDevice(id) {
  devices.status.set(id, { state: "checking", message: "Checking…" });
  renderDeviceGrid(); updateDeviceDot();
  try {
    const { connection } = await post(`/api/v1/profiles/${encodeURIComponent(id)}/test`);
    devices.status.set(id, connection);
  } catch (error) { devices.status.set(id, { state: "error", message: error.message }); }
  renderDeviceGrid(); updateDeviceDot();
}
/* Device information (OpenWebif about + tuner signal, temperature when Files is connected) */
const deviceInfoState = new Map(); // id -> { loading, data, error }
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
    const data = await api(`/api/v1/profiles/${encodeURIComponent(id)}/info`);
    if (deviceInfoState.get(id) === state) { state.data = data; state.error = ""; }
  } catch (error) {
    if (deviceInfoState.get(id) === state) { state.error = error.message; if (!quiet) state.data = null; }
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
  const d = state.data || {};
  const grid = h("dl", { class: "info-grid" });
  const row = (label, value) => { if (value) grid.append(h("dt", { text: label }), h("dd", { text: value })); };
  row("Model", d.model); row("Image", d.image); row("Enigma2", d.enigma); row("Kernel", d.kernel); row("Chipset", d.chipset);
  row("Uptime", d.uptime); row("Memory", d.memory);
  row("Temperature", d.temperature != null ? `${d.temperature} °C` : "");
  panel.append(grid);
  if (d.temperature == null) panel.append(h("p", { class: "muted small", text: "Temperature appears when Files is connected to this device." }));
  if (d.signal) {
    panel.append(h("div", { class: "info-bars", "aria-label": "Tuner signal" },
      d.signal.tuner ? h("span", { class: "muted small", text: `Signal · ${d.signal.tuner}${d.signal.standby ? " (standby)" : ""}` }) : null,
      infoBar("SNR", d.signal.snr, d.signal.snr_db ? ` · ${d.signal.snr_db} dB` : ""), infoBar("AGC", d.signal.agc)));
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
    await api(`/api/v1/profiles/${encodeURIComponent(profile.id)}`, { method: "DELETE" });
    devices.status.delete(profile.id);
    if (devices.currentID === profile.id) { await fileDisconnect(true); closeTabsOfDevice(profile.id); devices.currentID = ""; }
    await loadDevices();
    store.set("device", devices.currentID);
    toast(`${profile.name} deleted`);
  } catch (error) { toastError(error); }
}

/* Device dialog */
const deviceDialog = { editing: null };
function openDeviceDialog(profile = null) {
  deviceDialog.editing = profile;
  $("#deviceDialogTitle").textContent = profile ? "Edit device" : "Add device";
  $("#devName").value = profile?.name || "";
  $("#devHost").value = profile?.host || "";
  $("#devUser").value = profile?.username || "root";
  $("#devPass").value = "";
  $("#devPass").type = "text"; // passwords are shown; the eye button hides them
  $("#devPass").placeholder = profile ? (profile.credential_configured ? "Saved" : "No password") : "dreambox";
  if (profile?.credential_configured) { // shown only on this computer; a remote page keeps the field empty (empty = keep)
    const field = $("#devPass"), editing = profile;
    api(`/api/v1/profiles/${encodeURIComponent(profile.id)}/password`).then((data) => {
      if (deviceDialog.editing === editing && !field.value && data?.password) field.value = data.password;
    }).catch(() => {});
  }
  $("#devFtp").value = profile?.ftp_port || "";
  $("#devWeb").value = profile?.port || "";
  $("#devWebHttps").checked = profile?.scheme === "https";
  $("#devTelnet").value = profile?.telnet_port || "";
  $(".advanced", $("#deviceDialog")).open = Boolean(profile && (profile.ftp_port || profile.telnet_port || profile.port || profile.scheme === "https"));
  $("#deviceError").hidden = true;
  openDialog($("#deviceDialog"));
  $("#devName").focus();
}
// Ticking or unticking HTTPS moves the web port between the usual 80 and 443.
$("#devWebHttps").addEventListener("change", () => {
  const field = $("#devWeb");
  const https = $("#devWebHttps").checked;
  if (https && (field.value === "" || field.value === "80")) field.value = "443";
  else if (!https && field.value === "443") field.value = "";
});
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
  try {
    body = {
      name: $("#devName").value.trim(), host: $("#devHost").value.trim(), username: $("#devUser").value.trim() || "root",
      password: $("#devPass").value, ftp_port: portValue("#devFtp"), telnet_port: portValue("#devTelnet"),
      web_port: portValue("#devWeb"), web_https: $("#devWebHttps").checked,
    };
  } catch (problem) { error.textContent = problem.message; error.hidden = false; return; }
  const editing = deviceDialog.editing;
  $("#deviceSave").disabled = true;
  try {
    const result = editing
      ? await api(`/api/v1/profiles/${encodeURIComponent(editing.id)}`, { method: "PUT", body: JSON.stringify(body) })
      : await post("/api/v1/profiles", body);
    $("#deviceDialog").close();
    const savedID = result.profile.id;
    if (editing && editing.id === devices.currentID) { await fileDisconnect(true); closeTabsOfDevice(editing.id); }
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
$("#devicesExport").addEventListener("click", async () => {
  try {
    const response = await fetch("/api/v1/profiles/export");
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = h("a", { href: url, download: "dcc-lite-devices.json" });
    document.body.append(link); link.click(); link.remove(); URL.revokeObjectURL(url);
    toast("Devices exported (passwords are not included)");
  } catch (error) { toastError(error); }
});
$("#devicesImport").addEventListener("click", () => $("#devicesImportFile").click());
$("#devicesImportFile").addEventListener("change", async (event) => {
  const file = event.target.files?.[0];
  event.target.value = "";
  if (!file) return;
  try {
    const data = JSON.parse(await file.text());
    const result = await post("/api/v1/profiles/import", { schema_version: data.schema_version, profiles: data.profiles });
    await loadDevices();
    toast(`${result.imported} device(s) imported with the default password — edit them to set the real one`);
  } catch (error) { toastError(error); }
});

/** Choose a device, optionally jump to a view. */
function openDevice(id, view) {
  setCurrentDevice(id);
  if (view) showView(view);
}
function setCurrentDevice(id) {
  const changed = id !== devices.currentID;
  devices.currentID = id;
  store.set("device", id);
  $("#deviceSelect").value = id;
  updateDeviceDot();
  renderDeviceGrid();
  if (changed) {
    fileDisconnect(true).then(() => { if (view.current === "files") filesEnter(); else if (view.current === "lists") listsEnter(); else if (view.current === "ipk") ipkEnter(); });
  }
  if (view.current === "terminal") terminalEnter();
  if (view.current === "screen") screenEnter();
  if (view.current === "signal") signalEnter();
  if (view.current === "ipk") ipkEnter();
  if (id && !devices.status.has(id)) testDevice(id);
}
$("#deviceSelect").addEventListener("change", (event) => setCurrentDevice(event.target.value));

/* ───────────── Views ───────────── */
const view = { current: "devices" };
/* "Tools" is one tab with four sub-pages. Each sub-page is its own section (view-lists, view-ipk, view-signal);
   view.current keeps the section name, tools.current says which sub-page is open. */
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
  if (!["devices", "files", "terminal", "screen", ...toolViews].includes(name)) name = "devices";
  if (view.current === "screen" && name !== "screen") screenStop(); // no polling while the tab is not shown
  if (view.current === "signal" && name !== "signal") signalStop();
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
}
for (const tab of $$("#mainTabs .tab")) tab.addEventListener("click", () => showView(tab.dataset.view));
