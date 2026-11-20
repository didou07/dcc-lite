"use strict";
const files = {
  sessionID: "", profileID: "", protocol: "ftp",
  connecting: false, gen: 0, manualOff: false, active: "remote", drag: null,
  transfers: [], queueTimer: 0, queueBusy: false, seenDone: new Set(),
  browserMode: false, ready: null, sync: store.get("filesSync", false), fresh: false, localLeft: store.get("filesLocalLeft", true),
  pending: [], fetched: new Set(), autoDone: new Set(), transfersSeen: false,
  clipboard: null,
};
files.ready = systemInfo().then((info) => {
  files.browserMode = info?.remote === "true";
  document.body.classList.toggle("browser-files", files.browserMode);
  $("#deviceBar").hidden = !files.browserMode;
  if (files.browserMode) { files.active = "remote"; $("#commander").dataset.activePane = "remote"; }
}).catch(() => {});
const coarsePointer = matchMedia("(pointer: coarse)").matches;

function makePane(kind) {
  const root = $(`#${kind}Pane`);
  const saved = store.get(`pane.${kind}`, {});
  return {
    kind, root, rows: $(`#${kind}Rows`), table: $(`#${kind}Table`), pathInput: $(`#${kind}Path`), filter: $(`#${kind}Filter`),
    summary: $(`#${kind}Summary`), empty: $(`#${kind}Empty`), hiddenBox: $('[data-role="hidden"]', root),
    entries: [], path: kind === "remote" ? "/" : ".", parent: kind === "remote" ? "/" : ".",
    selected: new Set(), anchor: -1, cursor: -1, visible: [],
    sort: { key: saved.key || "name", dir: saved.dir === "desc" ? "desc" : "asc" },
    showHidden: Boolean(saved.hidden), loaded: false,
  };
}
const panes = { remote: makePane("remote"), local: makePane("local") };
for (const pane of Object.values(panes)) pane.hiddenBox.checked = pane.showHidden;

function sessionLost(error) { return error?.code === "session_not_found" || /session not found/i.test(error?.message || ""); }
const requireSession = () => { if (!files.sessionID) { toast("Connect to the device first"); return false; } return true; };

function setFileStatus(state, text) {
  setStatusDot($("#fileStatus"), state, text);
  const connect = $("#listsConnect"), ipkConnect = $("#ipkConnect");
  if (connect) { connect.hidden = state === "connected"; connect.disabled = state === "connecting"; }
  if (ipkConnect) { ipkConnect.hidden = state === "connected"; ipkConnect.disabled = state === "connecting"; }
  if (typeof ipkRefresh === "function") ipkRefresh();
}
function updateFileUI() {
  const connected = Boolean(files.sessionID);
  const device = currentDevice();
  $("#fileConnect").hidden = connected;
  $("#fileConnect").disabled = files.connecting || !device;
  $("#fileDisconnect").hidden = !connected && !files.connecting;
  $("#filePort").disabled = connected || files.connecting;
  $("#remoteHost").textContent = connected ? `${device?.name || ""} · ${device?.host || ""}` : "Not connected";
  const active = panes[files.active];
  const selection = selectedEntries(active);
  const remoteSel = selectedEntries(panes.remote), localSel = selectedEntries(panes.local);
  const can = (cmd, value) => { const b = $(`.actionbar [data-cmd="${cmd}"]`); if (b) b.disabled = !value; };
  const paneReady = files.active === "local" || connected;
  can("mkdir", paneReady); can("rename", paneReady && selection.length === 1); can("chmod", paneReady && selection.length > 0);
  can("delete", paneReady && selection.length > 0); can("copypath", paneReady);
  can("edit", paneReady && selection.length === 1 && !selection[0].is_dir); can("transfer", connected && selection.length > 0); can("selectall", paneReady && active.visible.length > 0);
  { const allSel = active.visible.length > 0 && active.visible.every((e) => active.selected.has(e.path)); const lbl = $(".actionbar [data-cmd=\"selectall\"] .sel-label"); if (lbl) lbl.textContent = allSel ? "Deselect all" : "Select all"; }
  $("#btnDownload").disabled = !(connected && remoteSel.length);
  $("#btnUpload").disabled = !(connected && localSel.length);
  const transferBtn = $("#btnTransferActive");
  transferBtn.disabled = !(connected && selection.length);
  $("#transferActiveLabel").textContent = files.active === "remote" ? "Download" : "Upload";
  transferBtn.querySelector("use").setAttribute("href", files.active === "remote" ? "#i-download" : "#i-upload");
  $("#deviceUpload").disabled = !connected; $("#deviceUploadFolder").disabled = !connected;
  $("#deviceDownload").disabled = !(connected && remoteSel.some((e) => !e.is_dir));
  $("#fileTerminalHere").disabled = !connected;
  $("#fileRun").disabled = !(connected && remoteSel.length === 1 && !remoteSel[0].is_dir);
  for (const pane of Object.values(panes)) {
    const off = pane.kind === "remote" && !connected;
    pane.pathInput.disabled = off; pane.filter.disabled = off;
    $$(".pathbar .icon-btn", pane.root).forEach((b) => { b.disabled = off; });
  }
}

function compareEntries(pane) {
  const { key, dir } = pane.sort;
  const factor = dir === "desc" ? -1 : 1;
  const byName = (a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true });
  return (a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    let result = 0;
    if (key === "size") result = (a.size || 0) - (b.size || 0);
    else if (key === "modified") result = (Date.parse(a.modified_at || "") || 0) - (Date.parse(b.modified_at || "") || 0);
    else if (key === "mode") result = permissionOctal(a.mode).localeCompare(permissionOctal(b.mode));
    return (result || byName(a, b)) * factor;
  };
}
function computeVisible(pane) {
  const needle = pane.filter.value.trim().toLocaleLowerCase();
  pane.visible = pane.entries
    .filter((e) => (pane.showHidden || !e.name.startsWith(".")) && (!needle || e.name.toLocaleLowerCase().includes(needle)))
    .sort(compareEntries(pane));
  return pane.visible;
}
const FILE_TYPES = {
  text: ["txt", "md", "markdown", "nfo", "text", "readme"],
  doc: ["doc", "docx", "odt", "rtf"],
  pdf: ["pdf"],
  sheet: ["csv", "tsv", "xls", "xlsx", "ods"],
  log: ["log", "out", "err", "trace"],
  config: ["conf", "cfg", "ini", "properties", "rules", "service", "desktop", "env"],
  data: ["xml", "json", "yml", "yaml", "toml", "dat", "sav", "plist"],
  db: ["db", "sqlite", "sqlite3"],
  code: ["c", "h", "cpp", "hpp", "cc", "java", "go", "rs", "js", "mjs", "py", "lua", "pl", "php", "rb", "cs", "jsx", "tsx", "diff", "patch"],
  script: ["sh", "bash", "zsh", "init", "run", "bat", "cmd", "ps1", "awk", "sed", "mk"],
  web: ["html", "htm", "xhtml", "css"],
  image: ["png", "jpg", "jpeg", "gif", "bmp", "webp", "ico", "mvi", "svg", "tif", "tiff", "heic"],
  video: ["ts", "mkv", "mp4", "avi", "mpg", "mpeg", "mov", "m2ts", "m4v", "vob", "wmv", "flv", "webm", "3gp", "divx"],
  audio: ["mp3", "ogg", "flac", "wav", "aac", "m4a", "wma", "opus", "ac3", "dts", "mka"],
  sub: ["srt", "sub", "ass", "ssa", "vtt", "idx"],
  archive: ["zip", "tar", "gz", "tgz", "bz2", "xz", "7z", "rar", "lzma", "z", "zst", "lz4", "cpio", "jar"],
  box: ["ipk", "deb", "opk", "rpm", "apk"],
  chip: ["bin", "img", "nfi", "ubi", "ubifs", "jffs2", "rootfs", "fw"],
  key: ["pem", "crt", "cer", "key", "pub", "pfx", "p12", "gpg", "asc"],
  font: ["ttf", "otf", "woff", "woff2", "pcf"],
  bouquet: ["tv", "radio"],
  disc: ["iso", "dmg"],
  epg: ["eit"],
  meta: ["meta", "cuts", "ap", "sc"],
  lock: ["lock", "pid", "lck"],
  bak: ["bak", "old", "orig", "tmp", "swp", "bk", "backup"],
};
const FILE_TYPE_BY_EXT = Object.fromEntries(Object.entries(FILE_TYPES).flatMap(([type, list]) => list.map((ext) => [ext, type])));
const FILE_TYPE_BY_NAME = [
  [/^(lamedb5?)$/, "db"],
  [/^(userbouquet\..*|bouquets\.(tv|radio))$/, "bouquet"],
  [/^(satellites|cables|terrestrial|atsc)\.xml$/, "tuner"],
  [/^epg\.(dat|db)$/, "epg"],
  [/^(settings|blacklist|whitelist|profile|fstab|passwd|shadow|group|hosts|inittab|resolv\.conf)$/, "config"],
  [/^(makefile|dockerfile|rcs|rc\.local)$/, "script"],
  [/^(readme|license|copying|changelog|authors)$/, "text"],
];
function fileType(name, mode = "") {
  const lower = String(name).toLowerCase();
  for (const [re, type] of FILE_TYPE_BY_NAME) if (re.test(lower)) return type;
  if (lower.endsWith("~")) return "bak";
  const dot = lower.lastIndexOf(".");
  const byExt = dot > 0 ? FILE_TYPE_BY_EXT[lower.slice(dot + 1)] || "" : "";
  if (byExt) return byExt;
  return /^-..x|^-.{5}x|^-.{8}x/.test(String(mode)) ? "script" : "";
}
function entryIcon(entry) {
  const hidden = String(entry.name).startsWith(".") ? " is-hidden" : "";
  if (entry.is_dir) return icon("folder", `dir-ic${hidden}`);
  if (String(entry.mode || "").startsWith("l")) return icon("link", "link-ic");
  const type = fileType(entry.name, entry.mode);
  return type ? icon(`ft-${type}`, `file-ic fi-${type}${hidden}`) : icon("file", `file-ic${hidden}`);
}
function renderPane(pane) {
  const visible = computeVisible(pane);
  const atRoot = pane.kind === "remote" ? (pane.path === "/" || pane.path === ".") : pane.path === ".";
  const fragment = document.createDocumentFragment();
  if (pane.loaded && !atRoot) {
    fragment.append(h("tr", { class: "row up", dataset: { up: "1" } }, h("td", { class: "name" }, icon("up", "dir-ic"), h("span", { text: ".." })), h("td"), h("td", { class: "hide-sm" }), h("td", { class: "hide-sm" })));
  }
  visible.forEach((entry, index) => {
    fragment.append(h("tr", {
      class: `row${pane.selected.has(entry.path) ? " selected" : ""}${index === pane.cursor ? " focus" : ""}`, dataset: { index }, draggable: "true",
    },
      h("td", { class: "name", title: entry.name }, entryIcon(entry), h("span", { text: entry.name })),
      h("td", { class: "num muted", text: entry.is_dir ? "—" : formatBytes(entry.size) }),
      h("td", { class: "muted hide-sm", text: formatDate(entry.modified_at) }),
      h("td", { class: "mode hide-sm", text: permissionOctal(entry.mode) })));
  });
  pane.rows.replaceChildren(fragment);
  $$("th[data-sort]", pane.root).forEach((th) => { th.dataset.dir = th.dataset.sort === pane.sort.key ? pane.sort.dir : ""; });
  const message = !pane.loaded ? (pane.kind === "remote" ? (files.connecting ? "Connecting…" : "Connect to a device to browse its files") : "Loading…")
    : !pane.entries.length ? "This folder is empty" : !visible.length ? (!pane.showHidden && !pane.filter.value.trim() ? "Only hidden items here. Tick “Hidden” to show them." : "No items match the filter") : "";
  pane.empty.textContent = message;
  pane.empty.hidden = !message;
  pane.empty.classList.toggle("loading", message.endsWith("…"));
  renderCrumbs(pane);
  updateSelectionUI(pane);
}
function renderCrumbs(pane) {
  if (!pane.crumbs) {
    pane.crumbs = h("nav", { class: "crumbs", "aria-label": "Path" });
    $(".filterbar", pane.root).before(pane.crumbs);
    pane.crumbs.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-path]");
      if (button) loadPane(pane.kind, button.dataset.path);
    });
    // Right-click on the path links.
    pane.crumbs.addEventListener("contextmenu", (event) => {
      event.preventDefault();
      const button = event.target.closest("button[data-path]");
      const where = button ? button.dataset.path : pane.path;
      openMenu([
        { label: "Copy path", icon: "copy", run: async () => { if (await clipboardWrite(where)) toast("Path copied"); else toast("Could not copy", "error"); } },
        { label: "Open", icon: "folder", run: () => loadPane(pane.kind, where) },
      ], event.clientX, event.clientY);
    });
  }
  const remote = pane.kind === "remote";
  const parts = pane.path.split("/").filter((part) => part && part !== ".");
  const crumbs = [h("button", { type: "button", class: "crumb", dataset: { path: remote ? "/" : "." }, title: remote ? "Root folder" : "Home folder" }, icon(remote ? "tv" : "home", "sm"))];
  let current = remote ? "" : ".";
  parts.forEach((part, index) => {
    current = remote ? `${current}/${part}` : (current === "." ? part : `${current}/${part}`);
    crumbs.push(h("span", { class: "crumb-sep", text: "›" }));
    crumbs.push(h("button", { type: "button", class: `crumb${index === parts.length - 1 ? " last" : ""}`, dataset: { path: current }, text: part }));
  });
  pane.crumbs.hidden = !pane.loaded || parts.length === 0;
  pane.crumbs.replaceChildren(...crumbs);
  pane.crumbs.scrollLeft = pane.crumbs.scrollWidth;
}
function updateSelectionUI(pane) {
  const rows = $$("tr.row[data-index]", pane.rows);
  rows.forEach((row) => {
    const entry = pane.visible[Number(row.dataset.index)];
    row.classList.toggle("selected", Boolean(entry) && pane.selected.has(entry.path));
    row.classList.toggle("focus", Number(row.dataset.index) === pane.cursor && files.active === pane.kind);
    const clip = files.clipboard;
    row.classList.toggle("cut", Boolean(entry && clip && clip.mode === "cut" && clip.kind === pane.kind && clip.entries.some((c) => c.path === entry.path)));
  });
  const selection = selectedEntries(pane);
  const folders = pane.entries.filter((e) => e.is_dir).length;
  const total = `${pane.entries.length} item${pane.entries.length === 1 ? "" : "s"}${folders ? ` · ${folders} folder${folders === 1 ? "" : "s"}` : ""}`;
  const bytes = selection.filter((e) => !e.is_dir).reduce((sum, e) => sum + (e.size || 0), 0);
  pane.summary.textContent = pane.loaded ? (selection.length ? `${selection.length} selected${bytes ? ` · ${formatBytes(bytes)}` : ""}  —  ${total}` : total) : "—";
  updateFileUI();
}
const selectedEntries = (pane) => pane.entries.filter((entry) => pane.selected.has(entry.path));

function setActivePane(kind) {
  if (files.browserMode) kind = "remote";
  files.active = kind;
  for (const pane of Object.values(panes)) pane.root.classList.toggle("active", pane.kind === kind);
  $("#commander").dataset.activePane = kind;
  $$("#paneSwitch button").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.pane === kind)));
  updateSelectionUI(panes[kind]);
  const other = panes[kind === "remote" ? "local" : "remote"];
  updateSelectionUI(other);
}

/* Drive / place picker (Home, This computer, C:, D:, USB…) above the local panel. */
let placesList = [], placesLoading = null;
function placeFor(path) {
  const norm = (v) => String(v).replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
  const here = norm(path);
  if (path === "." || path === "") return placesList.find((p) => p.kind === "home");
  if (path === "@computer") return placesList.find((p) => p.kind === "computer");
  let best = null;
  for (const place of placesList) {
    if (place.kind === "home" || place.kind === "computer") continue;
    const base = norm(place.path);
    const abs = /^([a-z]:|\/)/.test(here);
    if (!abs && place.kind === "drive") continue;
    if (here === base || here.startsWith(base + "/") || (base === "" && abs)) { if (!best || base.length > norm(best.path).length) best = place; }
  }
  return best || placesList.find((p) => p.kind === "home");
}
function renderPlaces() {
  const select = $("#localPlaces");
  if (!select) return;
  select.replaceChildren(...placesList.map((p) => { const o = document.createElement("option"); o.value = p.path; o.textContent = p.label; return o; }));
  select.value = (placeFor(panes.local.path || ".") || placesList[0] || {}).path ?? ".";
}
function syncPlaces(path) {
  if (!placesLoading) {
    placesLoading = api("/api/v1/files/local/places").then((r) => { placesList = r.places || []; renderPlaces(); }).catch(() => { placesLoading = null; });
    return;
  }
  const select = $("#localPlaces");
  const place = placeFor(path);
  if (select && place) select.value = place.path;
}

async function loadPane(kind, path, { keepSelection = false, quiet = false } = {}) {
  const pane = panes[kind];
  if (kind === "remote" && !files.sessionID) return false;
  const sessionAtStart = files.sessionID, gen = files.gen;
  try {
    const result = kind === "local"
      ? await api(`/api/v1/files/local/list?path=${encodeURIComponent(path)}`)
      : await api(sessionURL(sessionAtStart, `list?path=${encodeURIComponent(path)}`));
    if (kind === "remote" && (gen !== files.gen || sessionAtStart !== files.sessionID)) return false;
    const previous = keepSelection && pane.path === result.path ? new Set(pane.selected) : new Set();
    pane.path = result.path; pane.parent = result.parent; pane.entries = result.entries || [];
    pane.selected = new Set([...previous].filter((p) => pane.entries.some((e) => e.path === p)));
    pane.anchor = -1; pane.cursor = -1; pane.loaded = true;
    pane.pathInput.value = result.path;
    if (kind === "local") { $("#localRoot").textContent = result.path === "." ? "Home folder" : "This computer"; syncPlaces(result.path); }
    renderPane(pane);
    if (kind === "local") store.set("localPath", result.path);
    else if (files.profileID) store.set(`remotePath.${files.profileID}.${files.protocol}`, result.path);
    return true;
  } catch (error) {
    if (kind === "remote" && sessionLost(error)) { handleSessionLost(); return false; }
    if (!quiet) toastError(error);
    pane.pathInput.value = pane.path;
    return false;
  }
}
function handleSessionLost() {
  if (!files.sessionID) return;
  files.sessionID = "";
  panes.remote.loaded = false; panes.remote.entries = []; panes.remote.selected.clear();
  setFileStatus("error", "Connection lost");
  renderPane(panes.remote);
  if (!files.manualOff && !files.connecting && currentDevice()) {
    toast("The file connection was closed. Reconnecting…");
    window.setTimeout(() => { if (!files.sessionID && !files.connecting && !files.manualOff && (view.current === "files" || view.current === "lists" || view.current === "ipk")) fileConnect().then(() => { if (view.current === "lists") listsEnterNow(); }); }, 600);
  } else toast("Connection closed. Press Connect.", "error");
}
const goUp = (kind, mirrored = false) => {
  const other = panes[kind === "remote" ? "local" : "remote"];
  const result = loadPane(kind, panes[kind].parent || (kind === "remote" ? "/" : "."));
  if (files.sync && !mirrored && other.loaded && other.parent && other.parent !== other.path) goUp(other.kind, true);
  return result;
};
const goRoot = (kind) => loadPane(kind, kind === "remote" ? "/" : ".");
const refreshPane = (kind) => loadPane(kind, panes[kind].path, { keepSelection: true });
async function refreshBoth() { await Promise.all([refreshPane("local"), files.sessionID ? refreshPane("remote") : null]); }

function syncFilePort() {
  const device = currentDevice();
  $("#filePort").value = String(defaultPort(device, files.protocol));
}
async function fileConnect() {
  const device = currentDevice();
  if (!device) { toast("Add a device first"); showView("devices"); return; }
  if (files.sessionID || files.connecting) return;
  const port = Number($("#filePort").value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) { toast("Enter a valid port", "error"); return; }
  const gen = ++files.gen;
  const protocol = files.protocol;
  files.connecting = true; files.manualOff = false;
  setFileStatus("connecting", `Connecting to ${device.host}…`);
  renderPane(panes.remote); updateFileUI();
  let opened = "";
  try {
    const payload = { profile_id: device.id, protocol, port };
    const result = await post("/api/v1/files/sessions", payload);
    opened = result.session.id;
    if (gen !== files.gen) { api(sessionURL(opened), { method: "DELETE" }).catch(() => {}); return; }
    files.sessionID = opened; files.profileID = device.id;
    setFileStatus("connected", `${protoName(protocol)} · ${device.host}`);
    const remembered = files.fresh && device.start_dir ? device.start_dir : store.get(`remotePath.${device.id}.${protocol}`, device.start_dir || "/");
    files.fresh = false;
    let ok = await loadPane("remote", remembered, { quiet: true });
    if (!ok && remembered !== "/") ok = await loadPane("remote", "/", { quiet: true });
    if (!ok) ok = await loadPane("remote", ".", { quiet: true });
    if (!ok && !files.sessionID) throw new Error("Connected, but the folder list could not be read.");
    if (!ok) toast("Connected, but the folder could not be listed.", "error");
    loadTransfers();
  } catch (error) {
    if (opened) api(sessionURL(opened), { method: "DELETE" }).catch(() => {});
    if (gen === files.gen) {
      files.sessionID = "";
      setFileStatus("error", error.message);
      toastError(error);
    }
  } finally {
    if (gen === files.gen) { files.connecting = false; renderPane(panes.remote); }
    updateFileUI();
  }
}
window.addEventListener("pagehide", () => {
  if (files.sessionID) fetch(sessionURL(files.sessionID), { method: "DELETE", keepalive: true }).catch(() => {});
});
async function fileDisconnect(silent = false) {
  files.gen++;
  const id = files.sessionID;
  files.sessionID = ""; files.connecting = false; files.manualOff = !silent;
  if (id) await api(sessionURL(id), { method: "DELETE" }).catch(() => {});
  const remote = panes.remote;
  remote.entries = []; remote.selected.clear(); remote.loaded = false; remote.path = "/"; remote.parent = "/"; remote.pathInput.value = "/";
  setFileStatus("idle", "Not connected");
  renderPane(remote); updateFileUI();
  if (id && !silent) toast("Disconnected");
}
function filesEnter() { files.ready.then(filesEnterNow); }
function filesEnterNow() {
  syncFilePort();
  if (!files.browserMode && !panes.local.loaded) loadPane("local", store.get("localPath", "."), { quiet: true }).then((ok) => { if (!ok) loadPane("local", ".", { quiet: true }); });
  if (!currentDevice()) { setFileStatus("idle", "No device selected"); renderPane(panes.remote); updateFileUI(); return; }
  if (!files.sessionID && !files.connecting && !files.manualOff) fileConnect();
  else updateFileUI();
  loadTransfers();
}
$("#fileConnect").addEventListener("click", () => { files.manualOff = false; fileConnect(); });
$("#fileDisconnect").addEventListener("click", () => fileDisconnect(false));

for (const pane of Object.values(panes)) {
  if (pane.kind === "local") $("#localPlaces")?.addEventListener("change", (event) => { loadPane("local", event.target.value, {}).then((ok) => { if (!ok) syncPlaces(panes.local.path); else pane.table.focus(); }); });
  pane.pathInput.addEventListener("dblclick", () => pane.pathInput.select()); // double click selects the whole path
  pane.pathInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter") { event.preventDefault(); loadPane(pane.kind, pane.pathInput.value.trim() || (pane.kind === "remote" ? "/" : "."), {}).then(() => pane.table.focus()); }
    if (event.key === "Escape") { pane.pathInput.value = pane.path; pane.table.focus(); }
  });
  pane.filter.addEventListener("input", () => { pane.cursor = -1; renderPane(pane); });
  pane.hiddenBox.addEventListener("change", () => { pane.showHidden = pane.hiddenBox.checked; saveSort(pane); renderPane(pane); });
  $$("th[data-sort]", pane.root).forEach((th) => th.addEventListener("click", () => {
    const key = th.dataset.sort;
    pane.sort = { key, dir: pane.sort.key === key && pane.sort.dir === "asc" ? "desc" : "asc" };
    saveSort(pane); renderPane(pane);
  }));
  $$(".pathbar .icon-btn", pane.root).forEach((button) => button.addEventListener("click", (event) => {
    const act = button.dataset.act;
    if (act === "up") goUp(pane.kind); else if (act === "root") goRoot(pane.kind); else if (act === "refresh") refreshPane(pane.kind);
    else if (act === "bookmarks") { const rect = button.getBoundingClientRect(); openBookmarks(pane.kind, rect.left, rect.bottom + 4); }
  }));
  pane.root.addEventListener("pointerdown", () => { if (files.active !== pane.kind) setActivePane(pane.kind); });
}
makePanesResizable();
// Drag the strip between the panels to change their width; stored as a ratio in CSS variables.
function makePanesResizable() {
  const commander = $("#commander"), bridge = commander && $(".bridge", commander);
  if (!commander || !bridge) return;
  const KEY = "paneSplit", MIN = 0.25, MAX = 0.75;
  const apply = (ratio) => {
    if (!(ratio >= MIN && ratio <= MAX)) { commander.style.removeProperty("--split-l"); commander.style.removeProperty("--split-r"); return; }
    commander.style.setProperty("--split-l", `${ratio}fr`);
    commander.style.setProperty("--split-r", `${1 - ratio}fr`);
  };
  apply(Number(store.get(KEY, 0)));
  const handle = document.createElement("div");
  handle.className = "pane-resize";
  handle.title = "Drag to make a panel wider · double-click to reset";
  handle.setAttribute("aria-hidden", "true");
  bridge.appendChild(handle);
  const reset = () => { apply(0); store.set(KEY, 0); };
  handle.addEventListener("dblclick", reset);
  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const box = commander.getBoundingClientRect(), usable = box.width - bridge.getBoundingClientRect().width;
    if (usable < 400) return;
    const startX = event.clientX, startLeft = commander.firstElementChild.getBoundingClientRect().width;
    let ratio = startLeft / usable;
    handle.setPointerCapture(event.pointerId);
    document.body.classList.add("col-resizing");
    const onMove = (moveEvent) => { ratio = Math.max(MIN, Math.min(MAX, (startLeft + moveEvent.clientX - startX) / usable)); apply(ratio); };
    const onUp = () => {
      handle.removeEventListener("pointermove", onMove);
      handle.removeEventListener("pointerup", onUp);
      handle.removeEventListener("pointercancel", onUp);
      document.body.classList.remove("col-resizing");
      store.set(KEY, ratio);
    };
    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
    handle.addEventListener("pointercancel", onUp);
  });
}
function saveSort(pane) { store.set(`pane.${pane.kind}`, { key: pane.sort.key, dir: pane.sort.dir, hidden: pane.showHidden }); }

$$("#paneSwitch button").forEach((button) => button.addEventListener("click", () => setActivePane(button.dataset.pane)));

function rowEntry(pane, row) { return row && row.dataset.index !== undefined ? pane.visible[Number(row.dataset.index)] : null; }
function openEntry(pane, entry) {
  if (!entry) return;
  if (entry.is_dir) {
    loadPane(pane.kind, entry.path);
    const other = panes[pane.kind === "remote" ? "local" : "remote"];
    const twin = files.sync && other.loaded ? other.entries.find((e) => e.is_dir && e.name === entry.name) : null;
    if (twin) loadPane(other.kind, twin.path);
  }
  else if (pane.kind === "remote") transferSelection("download", [entry]);
  else transferSelection("upload", [entry]);
}
for (const pane of Object.values(panes)) {
  pane.rows.addEventListener("click", (event) => {
    const row = event.target.closest("tr.row");
    if (!row) { if (!event.ctrlKey && !event.shiftKey && pane.selected.size) { pane.selected = new Set(); updateSelectionUI(pane); } return; }
    setActivePane(pane.kind);
    if (row.dataset.up) { if (coarsePointer || event.detail === 1) goUp(pane.kind); return; }
    const index = Number(row.dataset.index), entry = pane.visible[index];
    if (!entry) return;
    if (coarsePointer && !event.ctrlKey && !event.shiftKey) {
      if (entry.is_dir) { loadPane(pane.kind, entry.path); return; }
      if (pane.selected.has(entry.path)) pane.selected.delete(entry.path); else pane.selected.add(entry.path);
      pane.anchor = pane.cursor = index; updateSelectionUI(pane); return;
    }
    if (event.shiftKey && pane.anchor >= 0) {
      const [from, to] = [Math.min(pane.anchor, index), Math.max(pane.anchor, index)];
      if (!(event.ctrlKey || event.metaKey)) pane.selected.clear();
      for (let i = from; i <= to; i++) pane.selected.add(pane.visible[i].path);
    } else if (event.ctrlKey || event.metaKey) {
      if (pane.selected.has(entry.path)) pane.selected.delete(entry.path); else pane.selected.add(entry.path);
      pane.anchor = index;
    } else {
      // Clicking the name of an item that is already the only selection starts renaming (like a…
      const wasOnlySelected = pane.selected.size === 1 && pane.selected.has(entry.path);
      pane.selected = new Set([entry.path]); pane.anchor = index;
      clearTimeout(pane.renameTimer);
      if (wasOnlySelected && event.detail === 1 && !event.ctrlKey && !event.metaKey && event.target.closest("td.name span") && !(pane.kind === "remote" && !files.sessionID)) {
        pane.renameTimer = setTimeout(() => inlineRename(pane, entry), 450);
      }
    }
    pane.cursor = index;
    updateSelectionUI(pane);
  });
  const scroller = pane.table.parentElement;
  scroller?.addEventListener("click", (event) => {
    if (event.target !== scroller || event.ctrlKey || event.shiftKey || !pane.selected.size) return;
    setActivePane(pane.kind); pane.selected = new Set(); updateSelectionUI(pane);
  });
  pane.rows.addEventListener("dblclick", (event) => {
    clearTimeout(pane.renameTimer);
    if (coarsePointer) return;
    const row = event.target.closest("tr.row");
    if (!row || row.dataset.up) { if (row?.dataset.up) goUp(pane.kind); return; }
    openEntry(pane, rowEntry(pane, row));
  });
  pane.table.addEventListener("contextmenu", (event) => {
    event.preventDefault();
    if (pane.kind === "remote" && !files.sessionID) return;
    setActivePane(pane.kind);
    const row = event.target.closest("tr.row[data-index]");
    const entry = rowEntry(pane, row);
    if (entry && !pane.selected.has(entry.path)) { pane.selected = new Set([entry.path]); pane.anchor = pane.cursor = Number(row.dataset.index); updateSelectionUI(pane); }
    openMenu(contextItems(pane, Boolean(entry)), event.clientX, event.clientY);
  });
  pane.rows.addEventListener("dragstart", (event) => {
    const row = event.target.closest("tr.row[data-index]");
    const entry = rowEntry(pane, row);
    if (!entry || (pane.kind === "remote" && !files.sessionID)) { event.preventDefault(); return; }
    if (!pane.selected.has(entry.path)) { pane.selected = new Set([entry.path]); updateSelectionUI(pane); }
    files.drag = { from: pane.kind, entries: selectedEntries(pane) };
    event.dataTransfer.effectAllowed = "copyMove";
    event.dataTransfer.setData("text/plain", files.drag.entries.map((e) => e.name).join("\n"));
    row.classList.add("dragging");
  });
  pane.rows.addEventListener("dragend", () => { files.drag = null; $$(".dragging", pane.rows).forEach((r) => r.classList.remove("dragging")); Object.values(panes).forEach((p) => { p.root.classList.remove("drop"); markDropRow(p, null); }); });
  const dropPlan = (event) => {
    if (!files.drag) return null;
    const target = dropFolder(pane, event), same = files.drag.from === pane.kind;
    if (same && (!target || files.drag.entries.some((e) => e.path === target.path))) return null;
    return { target, same };
  };
  pane.root.addEventListener("dragover", (event) => {
    const plan = dropPlan(event);
    if (!plan) { markDropRow(pane, null); pane.root.classList.remove("drop"); return; }
    event.preventDefault(); event.dataTransfer.dropEffect = plan.same ? "move" : "copy";
    markDropRow(pane, plan.target?.row || null);
    pane.root.classList.toggle("drop", !plan.target);
  });
  pane.root.addEventListener("dragleave", (event) => { if (!pane.root.contains(event.relatedTarget)) { pane.root.classList.remove("drop"); markDropRow(pane, null); } });
  pane.root.addEventListener("drop", (event) => {
    const plan = dropPlan(event);
    if (!plan) return;
    event.preventDefault(); pane.root.classList.remove("drop"); markDropRow(pane, null);
    const dragged = files.drag; files.drag = null;
    if (plan.same) moveIntoFolder(pane.kind, dragged.entries, plan.target.path);
    else transferSelection(dragged.from === "remote" ? "download" : "upload", dragged.entries, plan.target ? plan.target.path : null);
  });
}

function dropFolder(pane, event) {
  const row = event.target.closest?.("tr.row[data-index]");
  const entry = row ? rowEntry(pane, row) : null;
  return entry && entry.is_dir ? { row, path: entry.path } : null;
}
function markDropRow(pane, row) {
  $$("tr.drop-target", pane.rows).forEach((r) => { if (r !== row) r.classList.remove("drop-target"); });
  if (row) row.classList.add("drop-target");
}
async function moveIntoFolder(kind, entries, folderPath) {
  if (kind === "remote" && !requireSession()) return;
  const join = kind === "local" ? joinLocal : joinRemote;
  let existing;
  try {
    const listing = kind === "local"
      ? await api(`/api/v1/files/local/list?path=${encodeURIComponent(folderPath)}`)
      : await api(sessionURL(files.sessionID, `list?path=${encodeURIComponent(folderPath)}`));
    existing = new Set((listing.entries || []).map((e) => e.name));
  } catch (error) { handleOpError(error, kind); return; }
  let moved = 0, skipped = 0;
  for (const entry of entries) {
    if (existing.has(entry.name) || folderPath === entry.path || folderPath.startsWith(`${entry.path}/`)) { skipped++; continue; }
    const body = { old_path: entry.path, new_path: join(folderPath, entry.name) };
    try {
      if (kind === "local") await post("/api/v1/files/local/rename", body);
      else await post(sessionURL(files.sessionID, "rename"), body);
      moved++;
    } catch (error) { toast(`${entry.name}: ${error.message}`, "error"); if (kind === "remote" && sessionLost(error)) { handleSessionLost(); return; } }
  }
  if (moved) toast(`${moved} item${moved === 1 ? "" : "s"} moved`);
  if (skipped) toast(`${skipped} skipped: the name already exists there`);
  panes[kind].selected = new Set();
  await loadPane(kind, panes[kind].path);
}

function contextItems(pane, onEntry) {
  const kind = pane.kind, sel = selectedEntries(pane), single = sel.length === 1 ? sel[0] : null;
  const items = [];
  if (onEntry) {
    if (single?.is_dir) items.push({ label: "Open", icon: "folder", run: () => loadPane(kind, single.path) });
    items.push(kind === "remote"
      ? { label: files.browserMode ? (sel.length > 1 ? `Download ${sel.length} items to this device` : "Download to this device") : (sel.length > 1 ? `Download ${sel.length} items` : "Download"), icon: "download", disabled: files.browserMode && !sel.some((e) => !e.is_dir), run: () => transferSelection("download") }
      : { label: sel.length > 1 ? `Upload ${sel.length} items` : "Upload", icon: "upload", run: () => transferSelection("upload") });
    items.push("-");
    items.push({ label: "Edit as text", icon: "file", sub: "F4", disabled: !single || single.is_dir, run: () => cmdEdit() });
    items.push({ label: "Rename", icon: "edit", sub: "F2", disabled: !single, run: () => cmdRename() });
    items.push({ label: "Permissions…", icon: "lock", sub: "F9", run: () => cmdChmod() });
    items.push({ label: "Copy path", icon: "copy", run: () => cmdCopyPath() });
    if (kind === "remote") items.push({ label: sel.length > 1 ? `Copy ${sel.length} items on the device…` : "Copy on the device…", icon: "copy", run: () => cmdCopyOnDevice() }, { label: "New symbolic link…", icon: "link", disabled: sel.length > 1, run: () => cmdSymlink() });
    items.push("-");
    items.push({ label: sel.length > 1 ? `Copy ${sel.length} items` : "Copy", icon: "copy", sub: "Ctrl+C", run: () => cmdCopy() });
    items.push({ label: sel.length > 1 ? `Cut ${sel.length} items` : "Cut", icon: "scissors", sub: "Ctrl+X", run: () => cmdCut() });
    if (kind === "remote") {
      items.push("-");
      items.push({ label: "Terminal here", icon: "terminal", run: () => openTerminalHere(single?.is_dir ? single.path : pane.path) });
      items.push({ label: "Install .ipk packages…", icon: "box", run: () => cmdIpk() });
      items.push({ label: "Terminal here, split beside current", icon: "split-h", run: () => openTerminalHere(single?.is_dir ? single.path : pane.path, { split: "row" }) });
      if (single && !single.is_dir) items.push({ label: "Run in terminal…", icon: "play", run: () => runRemoteFile() });
    }
    items.push("-");
    items.push({ label: sel.length > 1 ? `Delete ${sel.length} items` : "Delete", icon: "trash", sub: "F8", danger: true, run: () => cmdDelete() });
  } else {
    if (files.browserMode && kind === "remote") items.push({ label: "Upload files from this device…", icon: "upload", run: () => chooseDeviceFiles(false) }, { label: "Upload a folder from this device…", icon: "newfolder", run: () => chooseDeviceFiles(true) }, "-");
    items.push({ label: "New folder", icon: "newfolder", sub: "F7", run: () => cmdMkdir() });
    items.push({ label: "New text file…", icon: "file", sub: "⇧F4", run: () => cmdNewFile() });
    if (kind === "remote") items.push({ label: "New symbolic link…", icon: "link", run: () => cmdSymlink() });
    items.push({ label: "Paste", icon: "paste", sub: "Ctrl+V", disabled: !files.clipboard?.entries?.length, run: () => cmdPaste() });
    items.push({ label: "Refresh", icon: "refresh", run: () => refreshPane(kind) });
    items.push({ label: pane.visible.length > 0 && pane.visible.every((e) => pane.selected.has(e.path)) ? "Deselect all" : "Select all", icon: "check", sub: "Ctrl+A", run: () => cmdSelectAll() });
    if (kind === "remote") items.push("-", { label: "Terminal here", icon: "terminal", run: () => openTerminalHere(pane.path) }, { label: "Install .ipk packages…", icon: "box", run: () => cmdIpk() });
  }
  return items;
}

function bookmarkKey(kind) { return kind === "remote" ? `bookmarks.remote.${files.profileID || devices.currentID}` : "bookmarks.local"; }
const ENIGMA2_QUICK_JUMP = [
  ["Settings & bouquets", "/etc/enigma2"],
  ["Recordings", "/media/hdd/movie"],
  ["Hard disk", "/media/hdd"],
  ["USB drive", "/media/usb"],
  ["Plugins", "/usr/lib/enigma2/python/Plugins"],
  ["Skins", "/usr/share/enigma2"],
  ["Softcam config", "/etc/tuxbox/config"],
  ["Keys", "/var/keys"],
  ["Temporary files", "/tmp"],
  ["Root", "/"],
];
function openBookmarks(kind, x, y) {
  const pane = panes[kind];
  const list = store.get(bookmarkKey(kind), []);
  const has = list.includes(pane.path);
  const items = [];
  if (kind === "remote") {
    items.push({ heading: "Quick jump (Enigma2)" });
    for (const [label, path] of ENIGMA2_QUICK_JUMP) items.push({ label, icon: "folder", sub: path.length > 22 ? "…" + path.slice(-20) : path, run: () => loadPane(kind, path) });
    items.push("-");
  }
  items.push({ heading: "Bookmarks" });
  if (!list.length) items.push({ label: "No bookmarks yet", disabled: true });
  for (const path of list) items.push({ label: path, icon: "folder", run: () => loadPane(kind, path) });
  items.push("-");
  items.push(has
    ? { label: "Remove this folder", icon: "x", danger: true, run: () => { store.set(bookmarkKey(kind), list.filter((p) => p !== pane.path)); toast("Bookmark removed"); } }
    : { label: "Bookmark this folder", icon: "star", run: () => { store.set(bookmarkKey(kind), [...list, pane.path].slice(-30)); toast("Bookmark added"); } });
  openMenu(items, x, y);
}

async function cmdMkdir() {
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  const name = await ask({ title: "New folder", label: "Folder name", ok: "Create", validate: (v) => (validName(v) ? "" : "Enter a valid folder name (no slashes).") });
  if (!name) return;
  try {
    if (kind === "local") await post("/api/v1/files/local/mkdir", { path: joinLocal(panes.local.path, name) });
    else await post(sessionURL(files.sessionID, "mkdir"), { path: joinRemote(panes.remote.path, name) });
    await refreshPane(kind);
    toast(`Folder “${name}” created`);
  } catch (error) { handleOpError(error, kind); }
}
async function applyRename(kind, entry, name) {
  if (!name || name === entry.name) return;
  const parentPath = kind === "local" ? panes.local.path : panes.remote.path;
  const target = kind === "local" ? joinLocal(parentPath, name) : joinRemote(parentPath, name);
  try {
    if (kind === "local") await post("/api/v1/files/local/rename", { old_path: entry.path, new_path: target });
    else await post(sessionURL(files.sessionID, "rename"), { old_path: entry.path, new_path: target });
    panes[kind].selected = new Set([target]);
    await loadPane(kind, panes[kind].path, { keepSelection: true });
    toast("Renamed");
  } catch (error) { handleOpError(error, kind); }
}
// Edit the name right in the list (Enter saves, Esc cancels).
function inlineRename(pane, entry) {
  const kind = pane.kind;
  if (kind === "remote" && !files.sessionID) return false;
  const index = pane.visible.findIndex((e) => e.path === entry.path);
  const row = index >= 0 ? pane.rows.querySelector(`tr.row[data-index="${index}"]`) : null;
  const label = row?.querySelector("td.name span");
  if (!label || row.querySelector(".rename-input")) return Boolean(label);
  const input = h("input", { class: "rename-input", type: "text", value: entry.name, spellcheck: "false", autocomplete: "off", "aria-label": "New name" });
  input.value = entry.name;
  let done = false;
  const finish = (save) => {
    if (done) return;
    done = true;
    const name = input.value.trim();
    input.replaceWith(label);
    if (!save) { pane.table.focus?.(); return; }
    if (name && name !== entry.name && !validName(name)) { toast("Enter a valid name (no slashes).", "error"); return; }
    applyRename(kind, entry, name);
  };
  input.addEventListener("keydown", (event) => {
    event.stopPropagation();
    if (event.key === "Enter") { event.preventDefault(); finish(true); }
    else if (event.key === "Escape") { event.preventDefault(); finish(false); }
  });
  input.addEventListener("click", (event) => event.stopPropagation());
  input.addEventListener("dblclick", (event) => event.stopPropagation());
  input.addEventListener("contextmenu", (event) => event.stopPropagation());
  input.addEventListener("blur", () => finish(true));
  label.replaceWith(input);
  input.focus();
  const dot = entry.is_dir ? -1 : entry.name.lastIndexOf(".");
  input.setSelectionRange(0, dot > 0 ? dot : entry.name.length);
  return true;
}
async function cmdRename() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (sel.length !== 1) { toast("Select one item to rename"); return; }
  if (kind === "remote" && !requireSession()) return;
  const entry = sel[0];
  if (inlineRename(panes[kind], entry)) return;
  const name = await ask({ title: "Rename", label: "New name", value: entry.name, ok: "Rename", validate: (v) => (validName(v) ? "" : "Enter a valid name (no slashes).") });
  await applyRename(kind, entry, name);
}
async function cmdDelete() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to delete"); return; }
  if (kind === "remote" && !requireSession()) return;
  const names = sel.slice(0, 6).map((e) => `${e.is_dir ? "📁" : "•"} ${e.name}`).join("\n") + (sel.length > 6 ? `\n… and ${sel.length - 6} more` : "");
  const where = kind === "remote" ? "on the device" : "on this computer";
  const confirmed = await ask({ title: `Delete ${sel.length === 1 ? "item" : `${sel.length} items`}?`, message: `${names}\n\nThis permanently deletes the selection ${where}${sel.some((e) => e.is_dir) ? ", including everything inside folders" : ""}. It cannot be undone.`, input: false, ok: "Delete", danger: true });
  if (!confirmed) return;
  let failed = 0, last = null;
  for (const entry of sel) {
    try {
      if (kind === "local") await post("/api/v1/files/local/remove", { path: entry.path });
      else await post(sessionURL(files.sessionID, "remove"), { path: entry.path, directory: entry.is_dir });
    } catch (error) { failed++; last = error; if (sessionLost(error)) break; }
  }
  await refreshPane(kind);
  if (failed) { toast(`${failed} item(s) could not be deleted: ${last.message}`, "error"); if (sessionLost(last)) handleSessionLost(); }
  else toast(`${sel.length} item${sel.length === 1 ? "" : "s"} deleted`);
}
// Permissions dialog: tick rwx boxes, pick a preset (644 / 755 / 777) or type the octal mode.
function askPermissions(title, info, initial, hasFolder = false) {
  const dialog = $("#chmodDialog"), input = $("#chmodInput"), error = $("#chmodError"), form = $("#chmodForm");
  const boxes = $$("input[data-bit]", dialog);
  $("#chmodTitle").textContent = title;
  $("#chmodInfo").textContent = info;
  error.hidden = true;
  $("#chmodRecursiveRow").hidden = !hasFolder; $("#chmodRecursive").checked = false;
  const fromBoxes = () => boxes.reduce((sum, box) => sum + (box.checked ? Number(box.dataset.bit) : 0), 0);
  const setBoxes = (value) => boxes.forEach((box) => { box.checked = Boolean(value & Number(box.dataset.bit)); });
  const setOctal = (value) => { input.value = (value & 0o777).toString(8).padStart(3, "0"); };
  const start = /^[0-7]{3,4}$/.test(initial) ? parseInt(initial, 8) & 0o777 : 0o755;
  setBoxes(start); setOctal(start);
  return new Promise((resolve) => {
    let finished = false;
    const finish = (result) => {
      if (finished) return;
      finished = true;
      form.removeEventListener("submit", onSubmit); dialog.removeEventListener("click", onClick);
      dialog.removeEventListener("change", onChange); input.removeEventListener("input", onInput);
      dialog.removeEventListener("cancel", onCancel); dialog.removeEventListener("close", onClose);
      if (dialog.open) dialog.close();
      resolve(result);
    };
    const onChange = (event) => { if (event.target.matches("input[data-bit]")) setOctal(fromBoxes()); };
    const onInput = () => { if (/^[0-7]{3,4}$/.test(input.value.trim())) setBoxes(parseInt(input.value.trim(), 8) & 0o777); };
    const onClick = (event) => {
      const preset = event.target.closest("[data-mode]");
      if (preset) { const v = parseInt(preset.dataset.mode, 8); setBoxes(v); setOctal(v); return; }
      if (event.target.closest("#chmodCancel")) finish(null);
    };
    const onSubmit = (event) => {
      event.preventDefault();
      const text = input.value.trim();
      if (!/^[0-7]{3,4}$/.test(text)) { error.textContent = "Use 3 or 4 octal digits."; error.hidden = false; input.focus(); return; }
      finish({ mode: text, recursive: hasFolder && $("#chmodRecursive").checked });
    };
    const onCancel = (event) => { event?.preventDefault?.(); finish(null); };
    const onClose = () => { if (!dialog.open) finish(null); };
    form.addEventListener("submit", onSubmit); dialog.addEventListener("click", onClick);
    dialog.addEventListener("change", onChange); input.addEventListener("input", onInput);
    dialog.addEventListener("cancel", onCancel); dialog.addEventListener("close", onClose);
    openDialog(dialog);
    input.focus(); input.select();
  });
}
async function cmdChmod() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items first"); return; }
  if (kind === "remote" && !requireSession()) return;
  const current = sel.length === 1 ? permissionOctal(sel[0].mode) : "";
  const choice = await askPermissions("Permissions", sel.length === 1 ? `${sel[0].name} — now ${current}` : `${sel.length} items`, current, sel.some((e) => e.is_dir));
  if (!choice) return;
  const { mode, recursive } = choice;
  const chmodOne = (path) => (kind === "local" ? post("/api/v1/files/local/chmod", { path, mode }) : post(sessionURL(files.sessionID, "chmod"), { path, mode }));
  const listDir = (path) => (kind === "local" ? api(`/api/v1/files/local/list?path=${encodeURIComponent(path)}`) : api(sessionURL(files.sessionID, `list?path=${encodeURIComponent(path)}`)));
  const isLink = (e) => /^l/.test(String(e.mode || ""));
  const LIMIT = 5000;
  let failed = 0, done = 0, last = null, capped = false;
  const queue = sel.map((entry) => ({ entry, depth: 0 }));
  while (queue.length) {
    const { entry, depth } = queue.shift();
    if (done + failed >= LIMIT) { capped = true; break; }
    try { await chmodOne(entry.path); done++; } catch (error) { failed++; last = error; if (kind === "remote" && sessionLost(error)) break; continue; }
    if (recursive && entry.is_dir && !isLink(entry) && depth < 12) {
      try { const result = await listDir(entry.path); for (const child of result.entries || []) if (!isLink(child)) queue.push({ entry: child, depth: depth + 1 }); }
      catch (error) { failed++; last = error; }
    }
  }
  await loadPane(kind, panes[kind].path, { keepSelection: true });
  if (failed) toast(`Permissions failed for ${failed} item(s): ${last.message}`, "error");
  else if (capped) toast(`Permissions set to ${mode} on the first ${LIMIT} items only`, "error");
  else toast(recursive ? `Permissions set to ${mode} on ${done} items` : `Permissions set to ${mode}`);
}

/* ── Copy / Cut / Paste ───────────────────────────────────────────────── */
const LocalParentPath = (value) => { const index = value.lastIndexOf("/"); return index < 0 ? "." : index === 0 ? "/" : /^[A-Za-z]:$/.test(value.slice(0, index)) ? value.slice(0, index) + "/" : value.slice(0, index); };
function uniqueName(baseName, existingNames) {
  if (!existingNames.has(baseName)) return baseName;
  const dot = baseName.lastIndexOf(".");
  const hasExt = dot > 0 && dot < baseName.length - 1;
  const stem = hasExt ? baseName.slice(0, dot) : baseName, ext = hasExt ? baseName.slice(dot) : "";
  for (let n = 2; n < 1000; n++) {
    const candidate = `${stem} (copy${n > 2 ? ` ${n}` : ""})${ext}`;
    if (!existingNames.has(candidate)) return candidate;
  }
  return `${stem} (copy ${Date.now()})${ext}`;
}
function cmdCopy() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to copy"); return; }
  files.clipboard = { mode: "copy", kind, entries: sel.map((e) => ({ path: e.path, name: e.name, is_dir: e.is_dir })) };
  Object.values(panes).forEach(updateSelectionUI);
  toast(`${sel.length} item${sel.length === 1 ? "" : "s"} copied`);
}
function cmdCut() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to cut"); return; }
  if (kind === "remote" && !requireSession()) return;
  files.clipboard = { mode: "cut", kind, entries: sel.map((e) => ({ path: e.path, name: e.name, is_dir: e.is_dir })) };
  toast(`${sel.length} item${sel.length === 1 ? "" : "s"} ready to paste`);
  Object.values(panes).forEach(updateSelectionUI);
}
async function cmdPaste() {
  const clip = files.clipboard;
  if (!clip || !clip.entries.length) { toast("Nothing to paste"); return; }
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  if (clip.kind === "remote" && !files.sessionID) { toast("Connect to the device to paste items copied or cut from it"); return; }
  if (clip.kind === kind) await pasteWithinPane(clip, kind);
  else await pasteAcrossPanes(clip, kind);
}
async function pasteWithinPane(clip, kind) {
  const destDir = panes[kind].path;
  const existingNames = new Set(panes[kind].entries.map((e) => e.name));
  let done = 0, failed = 0, last = null;
  for (const entry of clip.entries) {
    let name = entry.name;
    let dst = kind === "local" ? joinLocal(destDir, name) : joinRemote(destDir, name);
    if (clip.mode === "copy" && dst === entry.path) { name = uniqueName(entry.name, existingNames); dst = kind === "local" ? joinLocal(destDir, name) : joinRemote(destDir, name); }
    else if (dst === entry.path) { continue; } // cutting into the same folder it's already in: nothing to do
    existingNames.add(name);
    const paste = async (overwrite) => {
      if (clip.mode === "cut") {
        if (overwrite) await post(kind === "local" ? "/api/v1/files/local/remove" : sessionURL(files.sessionID, "remove"), kind === "local" ? { path: dst } : { path: dst, directory: entry.is_dir });
        await post(kind === "local" ? "/api/v1/files/local/rename" : sessionURL(files.sessionID, "rename"), { old_path: entry.path, new_path: dst });
      } else {
        await post(kind === "local" ? "/api/v1/files/local/copy" : sessionURL(files.sessionID, "copy"), { src_path: entry.path, dst_path: dst, overwrite });
      }
    };
    try {
      await paste(false);
      done++;
    } catch (error) {
      if (error.code === "exists") {
        const overwrite = await ask({ title: "Replace existing item?", message: `“${name}” already exists. Replace it?`, input: false, ok: "Replace", danger: true });
        if (overwrite) {
          try { await paste(true); done++; } catch (error2) { failed++; last = error2; }
        }
      } else { failed++; last = error; if (sessionLost(error)) break; }
    }
  }
  if (clip.mode === "cut" && !failed) { files.clipboard = null; Object.values(panes).forEach(updateSelectionUI); }
  await refreshPane(kind);
  if (failed) toast(`${failed} item(s) could not be pasted: ${last.message}`, "error");
  else if (done) toast(`${done} item${done === 1 ? "" : "s"} ${clip.mode === "cut" ? "moved" : "copied"}`);
}
async function pasteAcrossPanes(clip, kind) {
  const direction = kind === "remote" ? "upload" : "download";
  const destDir = panes[kind].path;
  const beforeIDs = new Set(files.transfers.map((t) => t.id));
  await transferSelection(direction, clip.entries, destDir);
  if (clip.mode !== "cut") return;
  let transfers = [];
  try { ({ transfers } = await api("/api/v1/files/transfers")); } catch { return; }
  files.transfers = transfers || [];
  const matchEntry = (task) => clip.entries.find((e) => (direction === "upload" ? task.local_path === e.path : task.remote_path === e.path));
  const pending = new Map();
  for (const task of files.transfers) { if (!beforeIDs.has(task.id)) { const entry = matchEntry(task); if (entry) pending.set(task.id, entry); } }
  if (!pending.size) return;
  files.clipboard = null;
  toast(`Moving ${pending.size} item${pending.size === 1 ? "" : "s"} — the original is removed once the transfer finishes`);
  const poll = async () => {
    let current;
    try { ({ transfers: current } = await api("/api/v1/files/transfers")); } catch { window.setTimeout(poll, 1500); return; }
    for (const [id, entry] of [...pending]) {
      const task = current.find((t) => t.id === id);
      if (!task || task.state === "completed" || task.state === "failed" || task.state === "cancelled") {
        pending.delete(id);
        if (task?.state === "completed") {
          try {
            if (clip.kind === "local") await post("/api/v1/files/local/remove", { path: entry.path });
            else await post(sessionURL(files.sessionID, "remove"), { path: entry.path, directory: entry.is_dir });
            await refreshPane(clip.kind);
          } catch (error) { toast(`Could not remove the original “${entry.name}” after moving it: ${error.message}`, "error"); }
        } else if (task) {
          toast(`“${entry.name}” was not moved: the transfer ${task.state}`, "error");
        }
      }
    }
    if (pending.size) window.setTimeout(poll, 1200);
  };
  window.setTimeout(poll, 1200);
}

const editor = { path: "", name: "", size: 0, modifiedAt: "", hash: "", lineEnding: "lf", create: false, original: "", busy: false, kind: "remote" };
const editorURL = () => (editor.kind === "local" ? "/api/v1/files/local/content" : sessionURL(files.sessionID, "content"));
const editorDirty = () => $("#editorText").value !== editor.original;
function editorStatus(message, kind = "") { const node = $("#editorStatus"); node.textContent = message; node.dataset.kind = kind; }
function editorRender() {
  const dirty = editorDirty();
  $("#editorTitle").textContent = editor.name + (dirty ? " •" : "");
  $("#editorSave").disabled = editor.busy || (!dirty && !editor.create);
  $("#editorReload").disabled = editor.busy || editor.create;
  $("#editorMeta").textContent = `${formatBytes(editor.size)} · ${editor.lineEnding === "crlf" ? "CRLF" : editor.lineEnding === "mixed" ? "mixed → LF" : "LF"}${editor.create ? " · new file" : ""}`;
  const notice = $("#editorNotice");
  notice.hidden = editor.lineEnding !== "mixed";
  notice.textContent = editor.lineEnding === "mixed" ? "Mixed line endings. Saving converts them to LF." : "";
}
function editorCursor() {
  const text = $("#editorText"), before = text.value.slice(0, text.selectionStart);
  const line = before.split("\n").length;
  $("#editorCursor").textContent = `Ln ${line}, Col ${before.length - before.lastIndexOf("\n")}`;
}
const editorLines = (() => {
  const text = $("#editorText"), body = $("#editorBody"), gutter = $("#editorGutter"), nums = $("#editorNums"), toggle = $("#editorLineNumbers");
  const mirror = document.createElement("div");
  mirror.className = "editor-mirror"; mirror.setAttribute("aria-hidden", "true");
  body.append(mirror);
  let raf = 0, plainCount = 0;
  const syncScroll = () => { nums.style.transform = text.scrollTop ? `translateY(${-text.scrollTop}px)` : ""; };
  function wrappedRows(lines) {
    const rows = new Array(lines.length).fill(1), style = getComputedStyle(text);
    const width = text.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight), lineHeight = parseFloat(style.lineHeight);
    if (!(width > 0) || !(lineHeight > 0)) return rows;
    mirror.style.width = `${width}px`;
    const probe = document.createElement("span");
    probe.style.whiteSpace = "pre"; probe.textContent = "0".repeat(50);
    mirror.replaceChildren(probe);
    const charWidth = probe.getBoundingClientRect().width / 50, cols = charWidth > 0 ? Math.floor(width / charWidth) : 0;
    const measured = [], fragment = document.createDocumentFragment();
    lines.forEach((line, index) => {
      if (cols > 0 && line.length < cols && /^[\x20-\x7e]*$/.test(line)) return;
      const row = document.createElement("div");
      row.textContent = line || "\u200b";
      fragment.append(row); measured.push(index);
    });
    mirror.replaceChildren(fragment);
    const children = mirror.children;
    measured.forEach((index, k) => { rows[index] = Math.max(1, Math.round(children[k].getBoundingClientRect().height / lineHeight)); });
    mirror.replaceChildren();
    return rows;
  }
  function render() {
    raf = 0;
    const on = toggle.checked;
    gutter.hidden = !on;
    if (!on || !text.clientWidth) return;
    const wrapping = text.getAttribute("wrap") === "soft", value = text.value;
    let count = 1;
    for (let at = value.indexOf("\n"); at >= 0; at = value.indexOf("\n", at + 1)) count++;
    let out;
    if (!wrapping) {
      if (count !== plainCount || !nums.dataset.plain) {
        const list = new Array(count);
        for (let i = 0; i < count; i++) list[i] = i + 1;
        nums.textContent = list.join("\n"); plainCount = count; nums.dataset.plain = "1";
      }
    } else {
      const rows = wrappedRows(value.split("\n")), list = [];
      rows.forEach((n, i) => { list.push(i + 1); for (let r = 1; r < n; r++) list.push(""); });
      nums.textContent = list.join("\n"); delete nums.dataset.plain;
    }
    gutter.style.width = `calc(${Math.max(3, String(count).length)}ch + calc(18px * var(--ui)))`;
    syncScroll();
  }
  function update(now = false) {
    if (now) { if (raf) cancelAnimationFrame(raf); render(); return; }
    if (!raf) raf = requestAnimationFrame(render);
  }
  toggle.checked = store.get("editorLines", true);
  toggle.addEventListener("change", () => { store.set("editorLines", toggle.checked); update(true); text.focus(); });
  text.addEventListener("input", () => update());
  text.addEventListener("scroll", syncScroll);
  $("#editorWrap").addEventListener("change", () => update());
  gutter.addEventListener("mousedown", (event) => { event.preventDefault(); text.focus(); });
  gutter.addEventListener("wheel", (event) => { event.preventDefault(); text.scrollTop += event.deltaY; text.scrollLeft += event.deltaX; }, { passive: false });
  if (window.ResizeObserver) new ResizeObserver(() => update()).observe(text);
  return { update };
})();
function showEditor(kind, file, create) {
  Object.assign(editor, {
    kind, path: file.path, name: file.path.split("/").filter(Boolean).pop() || file.path, size: Number(file.size) || 0,
    modifiedAt: file.modified_at || "", hash: file.hash || "", lineEnding: file.line_ending || "lf", create, original: file.content, busy: false,
  });
  const text = $("#editorText");
  text.value = file.content;
  $("#editorPath").textContent = file.path;
  $("#editorPath").title = file.path;
  editorStatus(create ? `New file.` : "");
  const wrap = store.get("editorWrap", false);
  $("#editorWrap").checked = wrap;
  text.setAttribute("wrap", wrap ? "soft" : "off");
  editorRender();
  openDialog($("#editorDialog"));
  text.focus();
  text.setSelectionRange(0, 0);
  text.scrollTop = 0;
  editorCursor();
  editorLines.update(true);
}
function editorError(kind, error) {
  if (error?.code === "file_not_found") { toast(error.message, "error"); refreshPane(kind); return; }
  handleOpError(error, kind);
}
async function openEditor(kind, path) {
  try {
    const url = kind === "local" ? "/api/v1/files/local/content" : sessionURL(files.sessionID, "content");
    const data = await api(`${url}?path=${encodeURIComponent(path)}`);
    showEditor(kind, data.file, false);
  } catch (error) { editorError(kind, error); }
}
async function cmdEdit() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (sel.length !== 1 || sel[0].is_dir) { toast("Select one file to edit"); return; }
  if (kind === "remote" && !requireSession()) return;
  await openEditor(kind, sel[0].path);
}
async function cmdNewFile() {
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  const name = await ask({ title: "New text file", label: "File name", ok: "Create", validate: (v) => (validName(v) ? "" : "Enter a valid file name (no slashes).") });
  if (!name) return;
  const path = kind === "local" ? joinLocal(panes.local.path, name) : joinRemote(panes.remote.path, name);
  showEditor(kind, { path, content: "", size: 0, line_ending: "lf" }, true);
}
async function editorSave(force = false) {
  if (editor.busy) return;
  editor.busy = true; editorRender(); editorStatus("Saving…");
  const content = $("#editorText").value;
  try {
    const data = await api(editorURL(), {
      method: "PUT",
      body: JSON.stringify({
        path: editor.path, content, line_ending: editor.lineEnding, expected_size: editor.size,
        expected_modified_at: editor.modifiedAt || undefined, expected_hash: editor.hash || undefined, force, create: editor.create,
      }),
    });
    Object.assign(editor, {
      size: Number(data.file.size) || 0, modifiedAt: data.file.modified_at || "", hash: data.file.hash || "", lineEnding: data.file.line_ending || "lf",
      create: false, original: content,
    });
    editor.busy = false; editorRender();
    editorStatus(data.file.mode_unknown
      ? `Saved ${new Date().toLocaleTimeString()}.`
      : `Saved ${new Date().toLocaleTimeString()}`, data.file.mode_unknown ? "" : "ok");
    if (panes[editor.kind].path === (editor.kind === "local" ? LocalParentPath(editor.path) : RemoteParentPath(editor.path))) loadPane(editor.kind, panes[editor.kind].path, { keepSelection: true });
  } catch (error) {
    editor.busy = false; editorRender();
    editorStatus(error.message, "error");
    if (error.code === "conflict") {
      const overwrite = await ask({ title: "File changed since it was opened", message: `“${editor.name}” changed after you opened it. Overwrite?`, input: false, ok: "Overwrite", danger: true });
      if (overwrite) await editorSave(true);
    } else if (error.code === "exists") {
      editor.create = false;
      editorRender();
    } else if (sessionLost(error)) {
      handleSessionLost();
    }
  }
}
const RemoteParentPath = (value) => { const index = value.lastIndexOf("/"); return index <= 0 ? "/" : value.slice(0, index); };
async function editorDiscardOK(title) {
  if (!editorDirty()) return true;
  return Boolean(await ask({ title, message: `“${editor.name}” has unsaved changes.`, input: false, ok: "Discard changes", danger: true }));
}
async function editorClose() {
  if (editor.busy) return;
  if (!(await editorDiscardOK("Close without saving?"))) return;
  $("#editorText").value = editor.original;
  $("#editorDialog").close();
}
async function editorReload() {
  if (editor.busy || editor.create) return;
  if (!(await editorDiscardOK(editor.kind === "local" ? "Reload from this computer?" : "Reload from the device?"))) return;
  try {
    const data = await api(`${editorURL()}?path=${encodeURIComponent(editor.path)}`);
    showEditor(editor.kind, data.file, false);
    editorStatus(editor.kind === "local" ? "Reloaded from this computer" : "Reloaded from the device", "ok");
  } catch (error) { editorStatus(error.message, "error"); editorError(editor.kind, error); }
}
{
  const dialog = $("#editorDialog"), text = $("#editorText");
  text.addEventListener("input", () => { editorRender(); if ($("#editorStatus").dataset.kind) editorStatus(""); });
  for (const type of ["keyup", "click", "focus"]) text.addEventListener(type, editorCursor);
  dialog.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); if (!$("#editorSave").disabled) editorSave(); }
  });
  dialog.addEventListener("cancel", (event) => { event.preventDefault(); editorClose(); });
  $("#editorSave").addEventListener("click", () => editorSave());
  $("#editorClose").addEventListener("click", editorClose);
  $("#editorReload").addEventListener("click", editorReload);
  $("#editorWrap").addEventListener("change", (event) => { store.set("editorWrap", event.target.checked); text.setAttribute("wrap", event.target.checked ? "soft" : "off"); });
  window.addEventListener("beforeunload", (event) => { if (dialog.open && editorDirty()) { event.preventDefault(); event.returnValue = ""; } });
}


/* ── Layout helpers: swap panels, synchronised browsing, saved-site drawer ── */
function applyFilesLayout() {
  $("#commander").dataset.localLeft = files.localLeft ? "1" : "0";
  const sync = $("#fileSync");
  sync.setAttribute("aria-pressed", String(files.sync)); sync.classList.toggle("on", files.sync);
  const sites = $("#fileSites"), open = !$("#sitesSide").hidden;
  sites.setAttribute("aria-pressed", String(open)); sites.classList.toggle("on", open);
}
$("#fileSwap").addEventListener("click", () => { files.localLeft = !files.localLeft; store.set("filesLocalLeft", files.localLeft); applyFilesLayout(); });
$("#fileSync").addEventListener("click", () => {
  files.sync = !files.sync; store.set("filesSync", files.sync); applyFilesLayout();
  toast(files.sync ? "Synchronised browsing on: folders open in both panels" : "Synchronised browsing off");
});
function renderSitesSide() {
  const side = $("#sitesSide");
  if (side.hidden) return;
  const q = $("#sitesSideFilter").value.trim().toLowerCase(), list = $("#sitesSideList");
  list.replaceChildren();
  let group = null;
  const shown = sortedSites().filter((site) => siteMatches(site, q));
  if (!shown.length) list.append(h("p", { class: "muted small", text: devices.list.length ? "No match." : "No saved devices yet." }));
  for (const site of shown) {
    const title = site.favorite ? "Pinned" : (site.group || "");
    if (title !== group && title) list.append(h("div", { class: "side-heading", text: title }));
    group = title;
    const status = devices.status.get(site.id);
    list.append(h("button", { type: "button", role: "option", class: `side-site${site.id === devices.currentID ? " current" : ""}`, "aria-selected": String(site.id === devices.currentID), dataset: { color: site.color || "" },
      title: `${site.host}${site.start_dir ? ` · opens ${site.start_dir}` : ""}`, onClick: () => { files.fresh = true; openDevice(site.id, "files"); } },
      h("span", { class: "dot", dataset: { state: status?.state || "unknown" } }),
      h("span", { class: "side-name ellipsis", text: site.name }),
      h("span", { class: "side-host ellipsis muted", text: site.host })));
  }
}
$("#fileSites").addEventListener("click", () => {
  const side = $("#sitesSide"); side.hidden = !side.hidden; store.set("filesSitesOpen", !side.hidden);
  applyFilesLayout(); renderSitesSide();
  if (!side.hidden) { devices.list.forEach((d) => { if (!devices.status.has(d.id)) testDevice(d.id); }); $("#sitesSideFilter").focus(); }
});
$("#sitesSideFilter").addEventListener("input", renderSitesSide);
$("#sitesSideAdd").addEventListener("click", () => openDeviceDialog());
$("#sitesSide").hidden = !store.get("filesSitesOpen", false);
applyFilesLayout();

// ---- Editor: find and replace -------------------------------------------------
(function editorFindReplace() {
  const bar = $("#editorFind"), text = $("#editorText"), needleInput = $("#findText"), replaceInput = $("#replaceText"), info = $("#findInfo");
  const matchCase = () => $("#findCase").checked;
  const fold = (value) => (matchCase() ? value : value.toLowerCase());
  const setInfo = (message) => { info.textContent = message; };
  function openBar() {
    bar.hidden = false;
    const picked = text.value.slice(text.selectionStart, text.selectionEnd);
    if (picked && !/\n/.test(picked)) needleInput.value = picked;
    needleInput.focus(); needleInput.select();
  }
  function closeBar() { bar.hidden = true; setInfo(""); text.focus(); }
  function reveal(start, end) {
    text.focus();
    text.setSelectionRange(start, end);
    const lineHeight = parseFloat(getComputedStyle(text).lineHeight) || 18;
    const line = text.value.slice(0, start).split("\n").length - 1;
    if (line * lineHeight < text.scrollTop || (line + 2) * lineHeight > text.scrollTop + text.clientHeight) text.scrollTop = Math.max(0, line * lineHeight - text.clientHeight / 2);
    editorCursor();
    needleInput.focus();
  }
  function step(backwards) {
    const needle = needleInput.value;
    if (!needle) { setInfo(""); return false; }
    const hay = fold(text.value), key = fold(needle);
    let at = backwards ? (text.selectionStart > 0 ? hay.lastIndexOf(key, text.selectionStart - 1) : -1) : hay.indexOf(key, text.selectionEnd);
    let wrapped = false;
    if (at < 0) { at = backwards ? hay.lastIndexOf(key) : hay.indexOf(key); wrapped = at >= 0; }
    if (at < 0) { setInfo("Not found"); return false; }
    reveal(at, at + needle.length);
    setInfo(wrapped ? "Wrapped around" : "");
    return true;
  }
  function writeValue(value, from, to) {
    text.focus();
    text.setSelectionRange(from, to);
    if (!document.execCommand || !document.execCommand("insertText", false, value)) {
      text.setRangeText(value, from, to, "end");
      text.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }
  function replaceOne() {
    const needle = needleInput.value;
    if (!needle) return;
    const selected = text.value.slice(text.selectionStart, text.selectionEnd);
    if (fold(selected) === fold(needle)) {
      writeValue(replaceInput.value, text.selectionStart, text.selectionEnd);
      needleInput.focus();
    }
    step(false);
  }
  function replaceAll() {
    const needle = needleInput.value;
    if (!needle) return;
    const pattern = new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), matchCase() ? "g" : "gi");
    let count = 0;
    const next = text.value.replace(pattern, () => { count++; return replaceInput.value; });
    if (!count) { setInfo("Not found"); return; }
    writeValue(next, 0, text.value.length);
    text.setSelectionRange(0, 0);
    text.scrollTop = 0;
    editorCursor();
    needleInput.focus();
    setInfo(`${count} replaced`);
  }
  $("#findNext").addEventListener("click", () => step(false));
  $("#findPrev").addEventListener("click", () => step(true));
  $("#replaceOne").addEventListener("click", replaceOne);
  $("#replaceAll").addEventListener("click", replaceAll);
  $("#editorFindBtn").addEventListener("click", () => (bar.hidden ? openBar() : closeBar()));
  for (const input of [needleInput, replaceInput]) {
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") { event.preventDefault(); if (input === replaceInput) replaceOne(); else step(event.shiftKey); }
      else if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeBar(); }
    });
  }
  $("#findCase").addEventListener("change", () => setInfo(""));
  $("#editorDialog").addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === "f") { event.preventDefault(); openBar(); }
    else if (event.key === "F3") { event.preventDefault(); step(event.shiftKey); }
  });
  $("#editorDialog").addEventListener("close", () => { bar.hidden = true; setInfo(""); });
})();
