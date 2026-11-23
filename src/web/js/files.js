"use strict";
const files = {
  sessionID: "", profileID: "", sessionInfo: null, sessionKey: "", protocol: "ftp",
  connecting: false, gen: 0, manualOff: false, active: "remote", drag: null,
  transfers: [], queueTimer: 0, queueBusy: false, seenDone: new Set(),
  browserMode: false, ready: null, sync: store.get("filesSync", false), fresh: false, localLeft: store.get("filesLocalLeft", true),
  pending: [], fetched: new Set(), autoDone: new Set(), transfersSeen: false,
  clipboard: null, undo: [],
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
    entries: [], path: kind === "remote" ? "/" : ".", parent: kind === "remote" ? "/" : ".", rootPath: kind === "remote" ? "/" : ".",
    selected: new Set(), anchor: -1, cursor: -1, visible: [], hist: [], hpos: -1,
    renameTimer: 0, renamePendingPath: "",
    loadSeq: 0, historyBusy: false,
    sort: { key: saved.key || "name", dir: saved.dir === "desc" ? "desc" : "asc" },
    showHidden: Boolean(saved.hidden), loaded: false,
  };
}
const panes = { remote: makePane("remote"), local: makePane("local") };
for (const pane of Object.values(panes)) pane.hiddenBox.checked = pane.showHidden;

function sessionLost(error) { return error?.code === "session_not_found" || /session not found/i.test(error?.message || ""); }
const requireSession = () => { if (!files.sessionID) { toast("Connect to the device first"); return false; } return true; };
function fileSessionKey(info = files.sessionInfo) {
  if (!info) return "";
  return [info.profile_id || files.profileID, String(info.protocol || files.protocol).toLowerCase(), String(info.host || "").trim().toLowerCase(), Number(info.port) || 0].join("|");
}
function isFileSessionCurrent(id, key = "") {
  return Boolean(id && files.sessionID === id && (!key || files.sessionKey === key));
}

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
    const atRoot = paneAtRoot(pane);
    pane.pathInput.disabled = off; pane.filter.disabled = off;
    $$(".pathbar .icon-btn:not([data-history])", pane.root).forEach((button) => { button.disabled = off; });
    const parentButton = $('.pathbar .icon-btn[data-act="up"]', pane.root);
    const rootButton = $('.pathbar .icon-btn[data-act="root"]', pane.root);
    const refreshButton = $('.pathbar .icon-btn[data-act="refresh"]', pane.root);
    if (parentButton) parentButton.disabled = off || !pane.loaded || atRoot;
    if (rootButton) rootButton.disabled = off || !pane.loaded || (pane.kind === "remote" ? atRoot : pane.path === "." || pane.path === "");
    if (refreshButton) refreshButton.disabled = off || !pane.loaded;
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
  const cursorPath = pane.visible[pane.cursor]?.path || "";
  const anchorPath = pane.visible[pane.anchor]?.path || "";
  const needle = pane.filter.value.trim().toLocaleLowerCase();
  pane.visible = pane.entries
    .filter((e) => (pane.showHidden || !e.name.startsWith(".")) && (!needle || e.name.toLocaleLowerCase().includes(needle)))
    .sort(compareEntries(pane));
  const visiblePaths = new Set(pane.visible.map((entry) => entry.path));
  pane.selected = new Set([...pane.selected].filter((path) => visiblePaths.has(path)));
  pane.cursor = cursorPath ? pane.visible.findIndex((entry) => entry.path === cursorPath) : -1;
  pane.anchor = anchorPath ? pane.visible.findIndex((entry) => entry.path === anchorPath) : -1;
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
function paneAtRoot(pane) {
  if (pane.kind === "remote") return pane.path === pane.rootPath || (!pane.rootPath && (pane.path === "/" || pane.path === "."));
  return pane.path === "." || pane.path === "" || pane.path === "@computer";
}
function renderPane(pane) {
  const visible = computeVisible(pane);
  const atRoot = paneAtRoot(pane);
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
  const rootPath = remote ? (pane.rootPath || (pane.path.startsWith("/") ? "/" : ".")) : ".";
  const relative = remote && rootPath === ".";
  const pathValue = remote && relative ? pane.path.replace(/^\.\//, "") : pane.path;
  const parts = pathValue.split("/").filter((part) => part && part !== ".");
  const crumbs = [h("button", { type: "button", class: "crumb", dataset: { path: rootPath }, title: remote ? "Root folder" : "Home folder" }, icon(remote ? "tv" : "home", "sm"))];
  let current = remote ? (relative ? "." : "") : ".";
  parts.forEach((part, index) => {
    current = remote ? (relative ? (current === "." ? part : `${current}/${part}`) : `${current}/${part}`) : (current === "." ? part : `${current}/${part}`);
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
    row.classList.toggle("cut", Boolean(entry && clip && clip.mode === "cut" && clip.kind === pane.kind && (pane.kind !== "remote" || clip.profileID === files.profileID) && clip.entries.some((c) => c.path === entry.path)));
  });
  const selection = selectedEntries(pane);
  const folders = pane.entries.filter((e) => e.is_dir).length;
  const total = `${pane.entries.length} item${pane.entries.length === 1 ? "" : "s"}${folders ? ` · ${folders} folder${folders === 1 ? "" : "s"}` : ""}`;
  const bytes = selection.filter((e) => !e.is_dir).reduce((sum, e) => sum + (e.size || 0), 0);
  pane.summary.textContent = pane.loaded ? (selection.length ? `${selection.length} selected${bytes ? ` · ${formatBytes(bytes)}` : ""}  —  ${total}` : total) : "—";
  updateFileUI();
}
const selectedEntries = (pane) => pane.visible.filter((entry) => pane.selected.has(entry.path));

function cancelPendingRename(pane) {
  if (pane.renameTimer) window.clearTimeout(pane.renameTimer);
  pane.renameTimer = 0;
  pane.renamePendingPath = "";
}

function clearPaneSelection(pane) {
  cancelPendingRename(pane);
  pane.selected.clear();
  pane.anchor = -1;
  pane.cursor = -1;
  updateSelectionUI(pane);
}

function setActivePane(kind) {
  if (files.browserMode) kind = "remote";
  for (const pane of Object.values(panes)) if (pane.kind !== kind) cancelPendingRename(pane);
  files.active = kind;
  for (const pane of Object.values(panes)) pane.root.classList.toggle("active", pane.kind === kind);
  $("#commander").dataset.activePane = kind;
  $$("#paneSwitch button").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.pane === kind)));
  updateSelectionUI(panes[kind]);
  const other = panes[kind === "remote" ? "local" : "remote"];
  updateSelectionUI(other);
}

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

function resetPaneHistory(pane) {
  pane.loadSeq++;
  pane.hist = [];
  pane.hpos = -1;
  pane.historyBusy = false;
  updateHistoryButtons(pane);
}
async function loadPane(kind, path, { keepSelection = false, quiet = false, record = true } = {}) {
  const pane = panes[kind];
  if (kind === "remote" && !files.sessionID) return false;
  cancelPendingRename(pane);
  const request = ++pane.loadSeq;
  const sessionAtStart = files.sessionID, gen = files.gen;
  try {
    const result = kind === "local"
      ? await api(`/api/v1/files/local/list?path=${encodeURIComponent(path)}`)
      : await api(sessionURL(sessionAtStart, `list?path=${encodeURIComponent(path)}`));
    if (request !== pane.loadSeq || (kind === "remote" && (gen !== files.gen || sessionAtStart !== files.sessionID))) return false;
    const cursorPath = pane.visible[pane.cursor]?.path || "";
    const anchorPath = pane.visible[pane.anchor]?.path || "";
    const previous = keepSelection && pane.path === result.path ? new Set(pane.selected) : new Set();
    pane.path = result.path; pane.parent = result.parent; pane.entries = result.entries || [];
    pane.selected = new Set([...previous].filter((p) => pane.entries.some((e) => e.path === p)));
    pane.anchor = -1; pane.cursor = -1; pane.loaded = true;
    if (record && pane.hist[pane.hpos] !== result.path) {
      pane.hist = pane.hist.slice(0, pane.hpos + 1);
      pane.hist.push(result.path);
      if (pane.hist.length > 100) pane.hist.splice(0, pane.hist.length - 100);
      pane.hpos = pane.hist.length - 1;
    }
    updateHistoryButtons(pane);
    pane.pathInput.value = result.path;
    if (kind === "local") { $("#localRoot").textContent = result.path === "." ? "Home folder" : "This computer"; syncPlaces(result.path); }
    renderPane(pane);
    if (keepSelection && pane.selected.size) {
      const focused = cursorPath && pane.selected.has(cursorPath) ? cursorPath : [...pane.selected][0];
      pane.cursor = pane.visible.findIndex((entry) => entry.path === focused);
      pane.anchor = anchorPath && pane.selected.has(anchorPath) ? pane.visible.findIndex((entry) => entry.path === anchorPath) : pane.cursor;
      updateSelectionUI(pane);
    }
    if (kind === "local") store.set("localPath", result.path);
    else if (files.profileID) store.set(`remotePath.${files.profileID}.${files.protocol}`, result.path);
    return true;
  } catch (error) {
    if (request !== pane.loadSeq) return false;
    if (kind === "remote" && sessionAtStart !== files.sessionID) return false;
    if (kind === "remote" && sessionLost(error)) { handleSessionLost(sessionAtStart); return false; }
    if (!quiet) toastError(error);
    pane.pathInput.value = pane.path;
    return false;
  }
}
function handleSessionLost(lostID = files.sessionID) {
  if (!lostID || files.sessionID !== lostID) return;
  files.sessionID = ""; files.sessionInfo = null; files.sessionKey = "";
  resetPaneHistory(panes.remote);
  panes.remote.loaded = false; panes.remote.entries = []; panes.remote.selected.clear(); panes.remote.rootPath = "/";
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
const goRoot = (kind) => loadPane(kind, kind === "remote" ? (panes.remote.rootPath || "/") : ".");
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
    files.sessionID = opened; files.profileID = device.id; files.sessionInfo = result.session;
    files.sessionKey = fileSessionKey(result.session);
    resetPaneHistory(panes.remote);
    setFileStatus("connected", `${protoName(protocol)} · ${device.host}`);
    const remembered = files.fresh && device.start_dir ? device.start_dir : store.get(`remotePath.${device.id}.${protocol}`, device.start_dir || "/");
    files.fresh = false;
    const remote = panes.remote;
    remote.rootPath = "";
    let ok = await loadPane("remote", "/", { quiet: true });
    if (ok) remote.rootPath = remote.path;
    if (!ok) {
      ok = await loadPane("remote", ".", { quiet: true });
      if (ok) remote.rootPath = remote.path;
    }
    if (ok && remembered && remembered !== remote.rootPath && !(remote.rootPath === "." && remembered === "/")) {
      await loadPane("remote", remembered, { quiet: true });
    } else if (!ok && remembered && remembered !== "/" && remembered !== ".") {
      ok = await loadPane("remote", remembered, { quiet: true });
      if (ok) remote.rootPath = remembered;
    }
    if (!ok && !files.sessionID) throw new Error("Connected, but the folder list could not be read.");
    if (!ok) toast("Connected, but the folder could not be listed.", "error");
    loadTransfers();
  } catch (error) {
    if (opened) api(sessionURL(opened), { method: "DELETE" }).catch(() => {});
    if (gen === files.gen) {
      files.sessionID = ""; files.sessionInfo = null; files.sessionKey = "";
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
  files.sessionID = ""; files.sessionInfo = null; files.sessionKey = ""; files.connecting = false; files.manualOff = !silent;
  if (id) await api(sessionURL(id), { method: "DELETE" }).catch(() => {});
  const remote = panes.remote;
  resetPaneHistory(remote);
  remote.entries = []; remote.selected.clear(); remote.loaded = false; remote.path = "/"; remote.parent = "/"; remote.rootPath = "/"; remote.pathInput.value = "/";
  setFileStatus("idle", "Not connected");
  renderPane(remote); updateFileUI();
  if (id && !silent) toast("Disconnected");
}
function filesEnter() { files.ready.then(filesEnterNow); }
function filesEnterNow() {
  syncFilePort();
  if (!files.browserMode && !panes.local.loaded) {
    const remembered = store.get("localPath", ".");
    loadPane("local", ".", { quiet: true }).then(async (ok) => {
      if (ok && remembered && remembered !== ".") await loadPane("local", remembered, { quiet: true });
      else if (!ok && remembered && remembered !== ".") await loadPane("local", remembered, { quiet: true });
    });
  }
  if (!currentDevice()) { setFileStatus("idle", "No device selected"); renderPane(panes.remote); updateFileUI(); return; }
  if (!files.sessionID && !files.connecting && !files.manualOff) fileConnect();
  else updateFileUI();
  loadTransfers();
}
$("#fileConnect").addEventListener("click", () => { files.manualOff = false; fileConnect(); });
$("#fileDisconnect").addEventListener("click", () => fileDisconnect(false));

for (const pane of Object.values(panes)) {
  if (pane.kind === "local") $("#localPlaces")?.addEventListener("change", (event) => { loadPane("local", event.target.value, {}).then((ok) => { if (!ok) syncPlaces(panes.local.path); else pane.table.focus(); }); });
  pane.pathInput.addEventListener("dblclick", () => pane.pathInput.select());
  pane.pathInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter") { event.preventDefault(); loadPane(pane.kind, pane.pathInput.value.trim() || (pane.kind === "remote" ? "/" : "."), {}).then(() => pane.table.focus()); }
    if (event.key === "Escape") { pane.pathInput.value = pane.path; pane.table.focus(); }
  });
  pane.filter.addEventListener("input", () => { cancelPendingRename(pane); pane.cursor = -1; pane.anchor = -1; renderPane(pane); });
  pane.hiddenBox.addEventListener("change", () => { cancelPendingRename(pane); pane.showHidden = pane.hiddenBox.checked; saveSort(pane); renderPane(pane); });
  $$("th[data-sort]", pane.root).forEach((th) => th.addEventListener("click", () => {
    const key = th.dataset.sort;
    cancelPendingRename(pane);
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
    if (!row) return;
    setActivePane(pane.kind);
    if (row.dataset.up) { cancelPendingRename(pane); if (coarsePointer || event.detail === 1) goUp(pane.kind); return; }
    const index = Number(row.dataset.index), entry = pane.visible[index];
    if (!entry) return;
    const nameCell = Boolean(event.target.closest("td.name"));
    if (pane.renameTimer && (event.detail > 1 || event.ctrlKey || event.metaKey || event.shiftKey || !nameCell || pane.renamePendingPath !== entry.path)) cancelPendingRename(pane);
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
      const wasOnlySelected = pane.selected.size === 1 && pane.selected.has(entry.path);
      pane.selected = new Set([entry.path]); pane.anchor = index;
      cancelPendingRename(pane);
      if (wasOnlySelected && event.detail === 1 && !event.ctrlKey && !event.metaKey && nameCell && !(pane.kind === "remote" && !files.sessionID)) {
        pane.renamePendingPath = entry.path;
        pane.renameTimer = window.setTimeout(() => {
          pane.renameTimer = 0;
          pane.renamePendingPath = "";
          if (pane.selected.has(entry.path) && pane.path === dirOf(pane.kind, entry.path)) inlineRename(pane, entry);
        }, 450);
      }
    }
    pane.cursor = index;
    updateSelectionUI(pane);
  });
  pane.table.addEventListener("click", (event) => {
    if (event.button !== 0 || event.target.closest?.("tr.row") || event.target.closest?.("thead") || event.ctrlKey || event.metaKey || event.shiftKey) return;
    setActivePane(pane.kind);
    clearPaneSelection(pane);
    pane.table.focus({ preventScroll: true });
  });
  pane.rows.addEventListener("dblclick", (event) => {
    cancelPendingRename(pane);
    if (coarsePointer) return;
    const row = event.target.closest("tr.row");
    if (!row || row.dataset.up) { if (row?.dataset.up) goUp(pane.kind); return; }
    openEntry(pane, rowEntry(pane, row));
  });
  pane.table.addEventListener("contextmenu", (event) => {
    if (event.target.closest?.(".rename-input")) return;
    event.preventDefault();
    if (pane.kind === "remote" && !files.sessionID) return;
    cancelPendingRename(pane);
    setActivePane(pane.kind);
    const row = event.target.closest("tr.row[data-index]");
    const entry = rowEntry(pane, row);
    if (entry) {
      const index = Number(row.dataset.index);
      if (!pane.selected.has(entry.path)) pane.selected = new Set([entry.path]);
      pane.anchor = index;
      pane.cursor = index;
      updateSelectionUI(pane);
    } else {
      clearPaneSelection(pane);
    }
    pane.table.focus({ preventScroll: true });
    openMenu(contextItems(pane, Boolean(entry)), event.clientX, event.clientY);
  });
  pane.rows.addEventListener("dragstart", (event) => {
    cancelPendingRename(pane);
    const row = event.target.closest("tr.row[data-index]");
    const entry = rowEntry(pane, row);
    if (!entry || (pane.kind === "remote" && !files.sessionID)) { event.preventDefault(); return; }
    if (!pane.selected.has(entry.path)) { pane.selected = new Set([entry.path]); updateSelectionUI(pane); }
    pane.cursor = Number(row.dataset.index);
    if (pane.anchor < 0) pane.anchor = pane.cursor;
    updateSelectionUI(pane);
    files.drag = { from: pane.kind, entries: selectedEntries(pane), sessionID: files.sessionID, sessionKey: files.sessionKey, profileID: files.profileID };
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
    const expected = { sessionID: dragged.sessionID, sessionKey: dragged.sessionKey, profileID: dragged.profileID };
    if (plan.same) moveIntoFolder(pane.kind, dragged.entries, plan.target.path, expected);
    else transferSelection(dragged.from === "remote" ? "download" : "upload", dragged.entries, plan.target ? plan.target.path : null, expected);
  });
}

document.addEventListener("pointerdown", (event) => {
  for (const pane of Object.values(panes)) {
    if (!pane.renameTimer) continue;
    const row = event.target.closest?.("tr.row[data-index]");
    const entry = row && pane.rows.contains(row) ? rowEntry(pane, row) : null;
    if (!entry || entry.path !== pane.renamePendingPath || !event.target.closest("td.name")) cancelPendingRename(pane);
  }
}, true);

function dropFolder(pane, event) {
  const row = event.target.closest?.("tr.row[data-index]");
  const entry = row ? rowEntry(pane, row) : null;
  return entry && entry.is_dir ? { row, path: entry.path } : null;
}
function markDropRow(pane, row) {
  $$("tr.drop-target", pane.rows).forEach((r) => { if (r !== row) r.classList.remove("drop-target"); });
  if (row) row.classList.add("drop-target");
}
async function moveIntoFolder(kind, entries, folderPath, expected = null) {
  if (kind === "remote" && !requireSession()) return;
  const sessionID = kind === "remote" ? (expected?.sessionID || files.sessionID) : "";
  const sessionKey = kind === "remote" ? (expected?.sessionKey || files.sessionKey) : "";
  const profileID = kind === "remote" ? (expected?.profileID || files.profileID) : "";
  if (kind === "remote" && (!isFileSessionCurrent(sessionID, sessionKey) || files.profileID !== profileID)) { toast("The receiver connection changed. Move cancelled.", "error"); return; }
  const join = kind === "local" ? joinLocal : joinRemote;
  let existing;
  try {
    const listing = kind === "local"
      ? await api(`/api/v1/files/local/list?path=${encodeURIComponent(folderPath)}`)
      : await api(sessionURL(sessionID, `list?path=${encodeURIComponent(folderPath)}`));
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Move cancelled.", "error"); return; }
    existing = new Set((listing.entries || []).map((e) => e.name));
  } catch (error) { if (kind === "remote" && sessionLost(error)) handleSessionLost(sessionID); else handleOpError(error, kind, sessionID); return; }
  let moved = 0, skipped = 0;
  const undoPairs = [];
  for (const entry of entries) {
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { skipped += entries.length - moved - skipped; break; }
    if (existing.has(entry.name) || folderPath === entry.path || folderPath.startsWith(`${entry.path}/`)) { skipped++; continue; }
    const body = { old_path: entry.path, new_path: join(folderPath, entry.name) };
    try {
      if (kind === "local") await post("/api/v1/files/local/rename", body);
      else await post(sessionURL(sessionID, "rename"), body);
      moved++; undoPairs.push({ from: body.new_path, to: entry.path });
    } catch (error) { toast(`${entry.name}: ${error.message}`, "error"); if (kind === "remote" && sessionLost(error)) { handleSessionLost(sessionID); return; } }
  }
  if (undoPairs.length) pushUndo(kind, "Move", undoPairs, profileID, sessionKey);
  if (moved) toast(`${moved} item${moved === 1 ? "" : "s"} moved · Ctrl+Z to undo`);
  if (skipped) toast(`${skipped} skipped: the name already exists there`);
  panes[kind].selected = new Set();
  panes[kind].anchor = -1;
  panes[kind].cursor = -1;
  updateSelectionUI(panes[kind]);
  if (kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) await loadPane(kind, panes[kind].path);
}

function contextItems(pane, onEntry) {
  const kind = pane.kind, sel = selectedEntries(pane), single = sel.length === 1 ? sel[0] : null;
  const many = sel.length > 1;
  const items = [];
  if (onEntry) {
    if (single?.is_dir) items.push({ label: "Open", icon: "folder", run: () => loadPane(kind, single.path) });
    else if (single) {
      items.push({ label: "Preview", icon: "eye", sub: "Space", run: () => cmdPreview() });
      items.push({ label: "Edit as text", icon: "file", sub: "F4", run: () => cmdEdit() });
    }
    items.push(kind === "remote"
      ? { label: files.browserMode ? (many ? `Download ${sel.length} items to this device` : "Download to this device") : (many ? `Download ${sel.length} items` : "Download"), icon: "download", disabled: files.browserMode && !sel.some((e) => !e.is_dir), run: () => transferSelection("download") }
      : { label: many ? `Upload ${sel.length} items` : "Upload", icon: "upload", run: () => transferSelection("upload") });
    items.push("-");
    items.push({ label: many ? `Cut ${sel.length} items` : "Cut", icon: "scissors", sub: "Ctrl+X", run: () => cmdCut() });
    items.push({ label: many ? `Copy ${sel.length} items` : "Copy", icon: "copy", sub: "Ctrl+C", run: () => cmdCopy() });
    if (kind === "remote") items.push({ label: many ? `Copy ${sel.length} items on the device…` : "Copy on the device…", icon: "copy", run: () => cmdCopyOnDevice() });
    items.push({ label: many ? "Copy names" : "Copy name", icon: "copy", run: () => cmdCopyName() });
    items.push({ label: many ? "Copy paths" : "Copy path", icon: "copy", run: () => cmdCopyPath() });
    items.push("-");
    items.push({ label: "Rename", icon: "edit", sub: "F2", disabled: !single, run: () => cmdRename() });
    items.push({ label: "Permissions…", icon: "lock", sub: "F9", run: () => cmdChmod() });
    items.push({ label: many ? `Delete ${sel.length} items` : "Delete", icon: "trash", sub: "F8", danger: true, run: () => cmdDelete() });
    if (kind === "remote") {
      items.push("-");
      items.push({ label: "Terminal here", icon: "terminal", run: () => openTerminalHere(single?.is_dir ? single.path : pane.path) });
      if (single && !single.is_dir) items.push({ label: "Run in terminal…", icon: "play", run: () => runRemoteFile() });
      if (sel.some((e) => !e.is_dir && /\.(ipk|deb)$/i.test(e.name))) items.push({ label: "Install package…", icon: "box", run: () => cmdIpk() });
    }
  } else {
    if (files.browserMode && kind === "remote") items.push({ label: "Upload files from this device…", icon: "upload", run: () => chooseDeviceFiles(false) }, { label: "Upload a folder from this device…", icon: "newfolder", run: () => chooseDeviceFiles(true) }, "-");
    items.push({ label: "New folder", icon: "newfolder", sub: "F7", run: () => cmdMkdir() });
    items.push({ label: "New text file…", icon: "file", sub: "⇧F4", run: () => cmdNewFile() });
    items.push({ label: "Paste", icon: "paste", sub: "Ctrl+V", disabled: !files.clipboard?.entries?.length, run: () => cmdPaste() });
    items.push("-");
    items.push({ label: "Refresh", icon: "refresh", sub: "F5", run: () => refreshPane(kind) });
    const nextUndo = lastUndo(kind);
    items.push({ label: nextUndo ? `Undo: ${nextUndo.label}` : "Nothing to undo", icon: "left", sub: "Ctrl+Z", disabled: !nextUndo, run: () => cmdUndo() });
    items.push({ label: pane.visible.length > 0 && pane.visible.every((e) => pane.selected.has(e.path)) ? "Deselect all" : "Select all", icon: "check", sub: "Ctrl+A", run: () => cmdSelectAll() });
    if (kind === "remote") {
      items.push("-");
      items.push({ label: "Terminal here", icon: "terminal", run: () => openTerminalHere(pane.path) });
      items.push({ label: "Install .ipk packages…", icon: "box", run: () => cmdIpk() });
      items.push({ label: "New symbolic link…", icon: "link", run: () => cmdSymlink() });
    }
  }
  return items;
}
async function cmdCopyName() {
  const pane = panes[files.active], sel = selectedEntries(pane);
  if (!sel.length) { toast("Select an item first"); return; }
  if (await clipboardWrite(sel.map((e) => e.name).join("\n"))) toast(sel.length > 1 ? "Names copied" : "Name copied");
  else toast("Copy was blocked by the browser", "error");
}

async function cmdCopyPath() {
  const pane = panes[files.active], selection = selectedEntries(pane);
  const value = selection.length ? selection.map((entry) => entry.path).join("\n") : pane.path;
  if (await clipboardWrite(value)) toast(selection.length > 1 ? "Paths copied" : "Path copied");
  else toast("Copy was blocked by the browser", "error");
}

function cmdSelectAll() {
  const pane = panes[files.active];
  cancelPendingRename(pane);
  const all = pane.visible.length > 0 && pane.visible.every((entry) => pane.selected.has(entry.path));
  if (all) {
    pane.selected.clear();
    pane.anchor = -1;
    pane.cursor = -1;
  } else {
    pane.selected = new Set(pane.visible.map((entry) => entry.path));
    if (pane.cursor < 0 && pane.visible.length) pane.cursor = pane.anchor = 0;
  }
  updateSelectionUI(pane);
}

function cmdSelectNone() {
  clearPaneSelection(panes[files.active]);
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
  const pane = panes[kind], path = pane.path;
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  const name = await ask({ title: "New folder", label: "Folder name", ok: "Create", validate: (v) => (validName(v) ? "" : "Enter a valid folder name (no slashes).") });
  if (!name) return;
  if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Folder creation was cancelled.", "error"); return; }
  try {
    if (kind === "local") await post("/api/v1/files/local/mkdir", { path: joinLocal(path, name) });
    else await post(sessionURL(sessionID, "mkdir"), { path: joinRemote(path, name) });
    if (kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) await loadPane(kind, path, { keepSelection: true });
    toast(`Folder “${name}” created`);
  } catch (error) { if (kind === "remote" && sessionLost(error)) handleSessionLost(sessionID); else handleOpError(error, kind, sessionID); }
}
async function applyRename(kind, entry, name, expected = null) {
  if (!validName(name) || name === entry.name) return;
  const pane = panes[kind];
  const parentPath = pane.path;
  const sessionID = kind === "remote" ? (expected?.sessionID || files.sessionID) : "";
  const profileID = kind === "remote" ? (expected?.profileID || files.profileID) : "";
  const sessionKey = kind === "remote" ? (expected?.sessionKey || files.sessionKey) : "";
  if (kind === "remote" && (!isFileSessionCurrent(sessionID, sessionKey) || files.profileID !== profileID)) { toast("The receiver connection changed. Rename cancelled.", "error"); return; }
  const target = kind === "local" ? joinLocal(parentPath, name) : joinRemote(parentPath, name);
  try {
    if (kind === "local") await post("/api/v1/files/local/rename", { old_path: entry.path, new_path: target });
    else await post(sessionURL(sessionID, "rename"), { old_path: entry.path, new_path: target });
    const sameSession = kind === "local" || isFileSessionCurrent(sessionID, sessionKey);
    if (sameSession && pane.path === parentPath) {
      if (pane.selected.has(entry.path)) {
        const selection = new Set(pane.selected);
        selection.delete(entry.path);
        selection.add(target);
        pane.selected = selection;
      }
      await loadPane(kind, parentPath, { keepSelection: true });
    }
    pushUndo(kind, "Rename", [{ from: target, to: entry.path }], profileID, sessionKey);
    toast(kind === "remote" && !sameSession ? "Renamed on the previous receiver" : "Renamed · Ctrl+Z to undo");
  } catch (error) {
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) toast(`${entry.name}: ${error.message}`, "error");
    else if (kind === "remote" && sessionLost(error)) handleSessionLost(sessionID);
    else handleOpError(error, kind, sessionID);
  }
}
function inlineRename(pane, entry) {
  const kind = pane.kind;
  if (kind === "remote" && !files.sessionID) return false;
  const expected = kind === "remote" ? { sessionID: files.sessionID, sessionKey: files.sessionKey, profileID: files.profileID } : null;
  if (files.active !== kind || !pane.selected.has(entry.path) || pane.path !== dirOf(kind, entry.path)) return false;
  const index = pane.visible.findIndex((e) => e.path === entry.path);
  const row = index >= 0 ? pane.rows.querySelector(`tr.row[data-index="${index}"]`) : null;
  const label = row?.querySelector("td.name span");
  if (!label || row.querySelector(".rename-input")) return Boolean(label);
  const input = h("input", { class: "rename-input", type: "text", value: entry.name, spellcheck: "false", autocomplete: "off", "aria-label": "New name" });
  input.value = entry.name;
  const cell = label.parentElement;
  cell?.classList.add("renaming");
  const wasDraggable = row.getAttribute("draggable");
  row.setAttribute("draggable", "false");
  let done = false;
  const finish = (save) => {
    if (done) return;
    const name = input.value;
    if (save && !validName(name)) {
      toast("Enter a valid name (no slashes).", "error");
      input.focus({ preventScroll: true });
      input.select();
      return;
    }
    done = true;
    document.removeEventListener("pointerdown", outside, true);
    if (wasDraggable === null) row.removeAttribute("draggable"); else row.setAttribute("draggable", wasDraggable);
    cell?.classList.remove("renaming");
    input.replaceWith(label);
    if (!save) { pane.table.focus?.(); return; }
    applyRename(kind, entry, name, expected);
  };
  input.addEventListener("keydown", (event) => {
    event.stopPropagation();
    if (event.key === "Enter") { event.preventDefault(); finish(true); }
    else if (event.key === "Escape") { event.preventDefault(); finish(false); }
  });
  for (const type of ["click", "dblclick", "mousedown", "mouseup", "pointerdown", "pointerup", "dragstart", "selectstart"]) {
    input.addEventListener(type, (event) => { event.stopPropagation(); });
  }
  input.addEventListener("dragstart", (event) => event.preventDefault());
  let keepSel = true, press = null;
  input.addEventListener("mousedown", (event) => {
    if (event.button !== 0 || !keepSel || input.selectionStart === input.selectionEnd) { keepSel = false; return; }
    press = { x: event.clientX, y: event.clientY, a: input.selectionStart, b: input.selectionEnd, dir: input.selectionDirection };
  });
  input.addEventListener("mouseup", (event) => {
    const p = press; press = null;
    if (!p) return;
    keepSel = false;
    const still = Math.abs(event.clientX - p.x) < 4 && Math.abs(event.clientY - p.y) < 4;
    if (!still || event.detail > 1) return;
    window.setTimeout(() => { if (!done) { input.focus({ preventScroll: true }); input.setSelectionRange(p.a, p.b, p.dir || "none"); } }, 0);
  });
  let menuOpen = false;
  input.addEventListener("contextmenu", () => {
    menuOpen = true;
    const menu = $("#menu");
    const watch = new MutationObserver(() => {
      if (!menu.hidden) return;
      watch.disconnect();
      window.setTimeout(() => { menuOpen = false; if (!done) input.focus({ preventScroll: true }); }, 30);
    });
    watch.observe(menu, { attributes: true, attributeFilter: ["hidden"] });
  });
  input.addEventListener("blur", () => { if (!menuOpen) finish(true); });
  const outside = (event) => { if (event.target === input || event.target.closest?.("#menu")) return; menuOpen = false; finish(true); };
  document.addEventListener("pointerdown", outside, true);
  label.replaceWith(input);
  input.focus();
  const dot = entry.is_dir ? -1 : entry.name.lastIndexOf(".");
  input.setSelectionRange(0, dot > 0 ? dot : entry.name.length);
  return true;
}
async function cmdRename() {
  const kind = files.active;
  cancelPendingRename(panes[kind]);
  const sel = selectedEntries(panes[kind]);
  if (sel.length !== 1) { toast("Select one item to rename"); return; }
  if (kind === "remote" && !requireSession()) return;
  const entry = sel[0];
  const expected = kind === "remote" ? { sessionID: files.sessionID, sessionKey: files.sessionKey, profileID: files.profileID } : null;
  if (inlineRename(panes[kind], entry)) return;
  const name = await ask({ title: "Rename", label: "New name", value: entry.name, ok: "Rename", validate: (v) => (validName(v) ? "" : "Enter a valid name (no slashes).") });
  await applyRename(kind, entry, name, expected);
}
async function cmdDelete() {
  const kind = files.active, sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to delete"); return; }
  if (kind === "remote" && !requireSession()) return;
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  const names = sel.slice(0, 6).map((e) => `${e.is_dir ? "📁" : "•"} ${e.name}`).join("\n") + (sel.length > 6 ? `\n… and ${sel.length - 6} more` : "");
  const where = kind === "remote" ? "on the device" : "on this computer";
  const confirmed = await ask({ title: `Delete ${sel.length === 1 ? "item" : `${sel.length} items`}?`, message: `${names}\n\nThis permanently deletes the selection ${where}${sel.some((e) => e.is_dir) ? ", including everything inside folders" : ""}. It cannot be undone.`, input: false, ok: "Delete", danger: true });
  if (!confirmed) return;
  if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Delete cancelled.", "error"); return; }
  let failed = 0, last = null;
  for (const entry of sel) {
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Remaining items were not deleted."); break; }
    try {
      if (kind === "local") await post("/api/v1/files/local/remove", { path: entry.path });
      else await post(sessionURL(sessionID, "remove"), { path: entry.path, directory: entry.is_dir });
    } catch (error) { failed++; last = error; if (sessionLost(error)) { handleSessionLost(sessionID); break; } }
  }
  if (kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) await refreshPane(kind);
  if (failed) { toast(`${failed} item(s) could not be deleted: ${last.message}`, "error"); if (kind === "remote" && sessionLost(last)) handleSessionLost(sessionID); }
  else toast(`${sel.length} item${sel.length === 1 ? "" : "s"} deleted`);
}
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
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  const current = sel.length === 1 ? permissionOctal(sel[0].mode) : "";
  const choice = await askPermissions("Permissions", sel.length === 1 ? `${sel[0].name} — now ${current}` : `${sel.length} items`, current, sel.some((e) => e.is_dir));
  if (!choice) return;
  const { mode, recursive } = choice;
  if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. Permission changes were cancelled.", "error"); return; }
  const chmodOne = (path) => (kind === "local" ? post("/api/v1/files/local/chmod", { path, mode }) : post(sessionURL(sessionID, "chmod"), { path, mode }));
  const listDir = (path) => (kind === "local" ? api(`/api/v1/files/local/list?path=${encodeURIComponent(path)}`) : api(sessionURL(sessionID, `list?path=${encodeURIComponent(path)}`)));
  const isLink = (e) => /^l/.test(String(e.mode || ""));
  const LIMIT = 5000;
  let failed = 0, done = 0, last = null, capped = false;
  const queue = sel.map((entry) => ({ entry, depth: 0 }));
  while (queue.length) {
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Remaining items were not changed."); break; }
    const { entry, depth } = queue.shift();
    if (done + failed >= LIMIT) { capped = true; break; }
    try { await chmodOne(entry.path); done++; } catch (error) { failed++; last = error; if (kind === "remote" && sessionLost(error)) { handleSessionLost(sessionID); break; } continue; }
    if (recursive && entry.is_dir && !isLink(entry) && depth < 12) {
      try {
        const result = await listDir(entry.path);
        if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Remaining items were not changed."); break; }
        for (const child of result.entries || []) if (!isLink(child)) queue.push({ entry: child, depth: depth + 1 });
      } catch (error) {
        failed++; last = error;
        if (kind === "remote" && sessionLost(error)) { handleSessionLost(sessionID); break; }
      }
    }
  }
  if (kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) await loadPane(kind, panes[kind].path, { keepSelection: true });
  if (failed) toast(`Permissions failed for ${failed} item(s): ${last.message}`, "error");
  else if (capped) toast(`Permissions set to ${mode} on the first ${LIMIT} items only`, "error");
  else toast(recursive ? `Permissions set to ${mode} on ${done} items` : `Permissions set to ${mode}`);
}

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
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  cancelPendingRename(panes[kind]);
  const sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to copy"); return; }
  files.clipboard = { mode: "copy", kind, profileID: kind === "remote" ? files.profileID : "", sessionKey: kind === "remote" ? files.sessionKey : "", entries: sel.map((e) => ({ path: e.path, name: e.name, is_dir: e.is_dir })) };
  Object.values(panes).forEach(updateSelectionUI);
  toast(`${sel.length} item${sel.length === 1 ? "" : "s"} copied`);
}
function cmdCut() {
  const kind = files.active;
  cancelPendingRename(panes[kind]);
  const sel = selectedEntries(panes[kind]);
  if (!sel.length) { toast("Select items to cut"); return; }
  if (kind === "remote" && !requireSession()) return;
  files.clipboard = { mode: "cut", kind, profileID: kind === "remote" ? files.profileID : "", sessionKey: kind === "remote" ? files.sessionKey : "", entries: sel.map((e) => ({ path: e.path, name: e.name, is_dir: e.is_dir })) };
  toast(`${sel.length} item${sel.length === 1 ? "" : "s"} ready to paste`);
  Object.values(panes).forEach(updateSelectionUI);
}
async function cmdPaste() {
  const clip = files.clipboard;
  if (!clip || !clip.entries.length) { toast("Nothing to paste"); return; }
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  if (clip.kind === "remote" && (!files.sessionID || clip.profileID !== files.profileID || clip.sessionKey !== files.sessionKey)) { toast("Reconnect to the source device before pasting its items", "error"); return; }
  if (clip.kind === kind) await pasteWithinPane(clip, kind);
  else await pasteAcrossPanes(clip, kind);
}
async function pasteWithinPane(clip, kind) {
  const destDir = panes[kind].path;
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  const profileID = kind === "remote" ? files.profileID : "";
  const existingNames = new Set(panes[kind].entries.map((e) => e.name));
  let done = 0, failed = 0, last = null;
  const undoPairs = [];
  for (const entry of clip.entries) {
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Remaining items were not pasted."); break; }
    let name = entry.name;
    let dst = kind === "local" ? joinLocal(destDir, name) : joinRemote(destDir, name);
    if (clip.mode === "copy" && dst === entry.path) { name = uniqueName(entry.name, existingNames); dst = kind === "local" ? joinLocal(destDir, name) : joinRemote(destDir, name); }
    else if (dst === entry.path) { continue; }
    existingNames.add(name);
    const paste = async (overwrite) => {
      if (clip.mode === "cut") {
        if (overwrite) await post(kind === "local" ? "/api/v1/files/local/remove" : sessionURL(sessionID, "remove"), kind === "local" ? { path: dst } : { path: dst, directory: entry.is_dir });
        await post(kind === "local" ? "/api/v1/files/local/rename" : sessionURL(sessionID, "rename"), { old_path: entry.path, new_path: dst });
      } else {
        await post(kind === "local" ? "/api/v1/files/local/copy" : sessionURL(sessionID, "copy"), { src_path: entry.path, dst_path: dst, overwrite });
      }
    };
    try {
      await paste(false);
      done++;
      if (clip.mode === "cut") undoPairs.push({ from: dst, to: entry.path });
    } catch (error) {
      if (error.code === "exists") {
        const overwrite = await ask({ title: "Replace existing item?", message: `“${name}” already exists. Replace it?`, input: false, ok: "Replace", danger: true });
        if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Paste cancelled."); break; }
        if (overwrite) {
          try { await paste(true); done++; } catch (error2) { failed++; last = error2; }
        }
      } else { failed++; last = error; if (sessionLost(error)) { if (kind === "remote") handleSessionLost(sessionID); break; } }
    }
  }
  if (clip.mode === "cut" && !failed) { files.clipboard = null; Object.values(panes).forEach(updateSelectionUI); }
  if (undoPairs.length) pushUndo(kind, "Move", undoPairs, profileID, sessionKey);
  if (kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) await refreshPane(kind);
  if (failed) toast(`${failed} item(s) could not be pasted: ${last.message}`, "error");
  else if (done) toast(`${done} item${done === 1 ? "" : "s"} ${clip.mode === "cut" ? "moved" : "copied"}`);
}
async function pasteAcrossPanes(clip, kind) {
  const direction = kind === "remote" ? "upload" : "download";
  const sourceSessionID = clip.kind === "remote" ? files.sessionID : "";
  const sourceProfileID = clip.kind === "remote" ? files.profileID : "";
  const sourceSessionKey = clip.kind === "remote" ? files.sessionKey : "";
  const destDir = panes[kind].path;
  const created = await transferSelection(direction, clip.entries, destDir);
  if (clip.mode !== "cut") return;
  const pending = new Map((created || []).filter((item) => item?.id && item.entry).map((item) => [item.id, item.entry]));
  if (!pending.size) return;
  files.clipboard = null;
  Object.values(panes).forEach(updateSelectionUI);
  toast(`Moving ${pending.size} item${pending.size === 1 ? "" : "s"} — the original is removed once the transfer finishes`);
  let sourceChangedNotified = false;
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
            else {
              if (!isFileSessionCurrent(sourceSessionID, sourceSessionKey) || files.profileID !== sourceProfileID) {
                if (!sourceChangedNotified) {
                  toast("The receiver connection changed. Downloaded originals were kept on the source device.", "error");
                  sourceChangedNotified = true;
                }
                continue;
              }
              await post(sessionURL(sourceSessionID, "remove"), { path: entry.path, directory: entry.is_dir });
            }
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

const editor = { path: "", name: "", size: 0, modifiedAt: "", hash: "", lineEnding: "lf", mixed: false, encoding: "utf-8", link: "", create: false, original: "", busy: false, kind: "remote", sessionID: "", sessionKey: "" };
const editorURL = () => (editor.kind === "local" ? "/api/v1/files/local/content" : sessionURL(editor.sessionID, "content"));
const editorDirty = () => $("#editorText").value !== editor.original;
function editorStatus(message, kind = "") { const node = $("#editorStatus"); node.textContent = message; node.dataset.kind = kind; }
function editorRender() {
  const dirty = editorDirty();
  $("#editorTitle").textContent = editor.name + (dirty ? " •" : "");
  $("#editorSave").disabled = editor.busy;
  $("#editorReload").disabled = editor.busy || editor.create;
  $("#editorMeta").textContent = `${formatBytes(editor.size)} · ${(editor.lineEnding === "crlf" ? "CRLF" : "LF") + (editor.mixed ? " (mixed)" : "")}${editor.encoding === "latin1" ? " · Latin-1" : ""}${editor.create ? " · new file" : ""}`;
  const notice = $("#editorNotice");
  const notes = [];
  if (editor.mixed) notes.push(`This file mixes CRLF and LF line endings. Saving uses ${editor.lineEnding === "crlf" ? "CRLF" : "LF"}, the more common one, for every line.`);
  if (editor.size > 2 * 1048576) notes.push(`This is a large file (${formatBytes(editor.size)}). Editing it may be slow.`);
  if (editor.encoding === "latin1") notes.push("This file is not UTF-8, so it is shown as Latin-1. Other letters may look garbled, but saving keeps the original bytes of everything you do not change. Text outside Latin-1 cannot be saved.");
  if (editor.link) notes.push(editor.link === "?" ? "This file is a link. The device follows it, so you are editing the file it points to, which may belong to a different image." : `This file is a link to ${editor.link}. The device follows it, so you are editing that target, which may belong to a different image than the folder you opened.`);
  notice.hidden = !notes.length;
  notice.textContent = notes.join(" ");
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
  const copyOnSelect = () => store.get("editorCopyOnSelect2", false) === true;
  const selected = () => text.value.slice(text.selectionStart, text.selectionEnd);
  const insertText = (clip) => {
    text.focus();
    // execCommand keeps the browser's own undo history (Ctrl+Z works after a paste).
    let done = false;
    try { done = document.execCommand("insertText", false, clip); } catch { done = false; }
    if (!done) {
      text.setRangeText(clip, text.selectionStart, text.selectionEnd, "end");
      text.dispatchEvent(new Event("input", { bubbles: true }));
    }
  };
  const copyEditor = async () => {
    const chosen = selected();
    if (!chosen) return;
    if (await clipboardWrite(chosen)) toast("Selection copied");
    else toast("Copy was blocked by the browser.", "error");
    text.focus();
  };
  const pasteEditor = async () => {
    const clip = await clipboardRead();
    if (clip === null) {
      const box = $("#pasteDialog");
      $("#pasteText").value = "";
      openDialog(box);
      $("#pasteText").focus();
      window.termPasteHook = insertText;
      const done = () => { box.removeEventListener("close", done); window.termPasteHook = null; text.focus(); };
      box.addEventListener("close", done);
      return;
    }
    if (clip) insertText(clip);
  };
  // Optional (off by default): copy a selection made with the mouse. Keyboard selection such as
  // Ctrl+A never copies, so selecting the old text to replace it cannot overwrite the clipboard.
  text.addEventListener("mouseup", (event) => {
    if (event.button !== 0 || !copyOnSelect()) return;
    window.setTimeout(() => { const chosen = selected(); if (chosen) clipboardWrite(chosen); }, 0);
  });
  const cutEditor = async () => {
    const chosen = selected();
    if (!chosen) return;
    if (await clipboardWrite(chosen)) {
      text.focus();
      let done = false;
      try { done = document.execCommand("delete"); } catch { done = false; }
      if (!done) {
        text.setRangeText("", text.selectionStart, text.selectionEnd, "end");
        text.dispatchEvent(new Event("input", { bubbles: true }));
      }
    } else toast("Cut was blocked by the browser.", "error");
  };
  text.addEventListener("contextmenu", (event) => {
    event.preventDefault(); event.stopPropagation();
    const has = Boolean(selected());
    openMenu([
      { label: "Cut", icon: "copy", sub: "Ctrl+X", disabled: !has, run: cutEditor },
      { label: "Copy", icon: "copy", sub: "Ctrl+C", disabled: !has, run: copyEditor },
      { label: "Paste", icon: "paste", sub: "Ctrl+V", run: pasteEditor },
      { label: "Select all", icon: "check", sub: "Ctrl+A", run: () => { text.focus(); text.select(); } },
      { label: copyOnSelect() ? "✓ Copy when selecting with the mouse" : "Copy when selecting with the mouse", icon: "copy", run: () => { store.set("editorCopyOnSelect2", !copyOnSelect()); toast(copyOnSelect() ? "Mouse selection is copied automatically" : "Automatic copy is off"); } },
    ], event.clientX, event.clientY);
  }, true);
  $("#editorWrap").addEventListener("change", () => update());
  gutter.addEventListener("mousedown", (event) => { event.preventDefault(); text.focus(); });
  gutter.addEventListener("wheel", (event) => { event.preventDefault(); text.scrollTop += event.deltaY; text.scrollLeft += event.deltaX; }, { passive: false });
  if (window.ResizeObserver) new ResizeObserver(() => update()).observe(text);
  const undoEditor = () => { text.focus(); try { document.execCommand("undo"); } catch { } text.dispatchEvent(new Event("input", { bubbles: true })); };
  return { update, copyEditor, cutEditor, pasteEditor, undoEditor };
})();
$("#editorCopy").addEventListener("click", () => editorLines.copyEditor());
$("#editorCut").addEventListener("click", () => editorLines.cutEditor());
$("#editorPaste").addEventListener("click", () => editorLines.pasteEditor());
$("#editorUndo").addEventListener("click", () => editorLines.undoEditor());
$("#editorSelectAll").addEventListener("click", () => { const t = $("#editorText"); t.focus(); t.select(); });
$("#editorSaveExit").addEventListener("click", async () => {
  await editorSave();
  if (!editor.busy && !editorDirty()) editorClose();
});
function showEditor(kind, file, create, sessionID = "", sessionKey = "") {
  Object.assign(editor, {
    kind, path: file.path, name: file.path.split("/").filter(Boolean).pop() || file.path, size: Number(file.size) || 0,
    modifiedAt: file.modified_at || "", hash: file.hash || "", lineEnding: file.line_ending || "lf", mixed: !!file.mixed_line_endings, encoding: file.encoding || "utf-8", link: file.link || "", create, original: file.content, busy: false, sessionID, sessionKey,
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
function editorError(kind, error, sessionID = "") {
  if (error?.code === "file_not_found") { toast(error.message, "error"); if (kind === "local" || isFileSessionCurrent(sessionID, editor.sessionKey)) refreshPane(kind); return; }
  handleOpError(error, kind, sessionID);
}
async function openEditor(kind, path) {
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  if (kind === "remote" && !requireSession()) return;
  try {
    const url = kind === "local" ? "/api/v1/files/local/content" : sessionURL(sessionID, "content");
    const data = await api(`${url}?path=${encodeURIComponent(path)}`);
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) return;
    showEditor(kind, data.file, false, sessionID, sessionKey);
  } catch (error) { editorError(kind, error, sessionID); }
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
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  const basePath = panes[kind].path;
  const name = await ask({ title: "New text file", label: "File name", ok: "Create", validate: (v) => (validName(v) ? "" : "Enter a valid file name (no slashes).") });
  if (!name) return;
  if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { toast("The receiver connection changed. New file creation was cancelled.", "error"); return; }
  const path = kind === "local" ? joinLocal(basePath, name) : joinRemote(basePath, name);
  showEditor(kind, { path, content: "", size: 0, line_ending: "lf" }, true, sessionID, sessionKey);
}
async function editorSave(force = false) {
  if (editor.busy) return;
  if (!force && !editorDirty() && !editor.create) { editorStatus("No changes to save.", ""); return; }
  const sessionID = editor.sessionID, sessionKey = editor.sessionKey, editorPath = editor.path, editorKind = editor.kind;
  if (editorKind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { editorStatus("The original device connection is no longer active. Reconnect to that device before saving.", "error"); return; }
  editor.busy = true; editorRender(); editorStatus("Saving…");
  const content = $("#editorText").value;
  try {
    const data = await api(editorURL(), {
      method: "PUT",
      body: JSON.stringify({
        path: editor.path, content, line_ending: editor.lineEnding, encoding: editor.encoding, expected_size: editor.size,
        expected_modified_at: editor.modifiedAt || undefined, expected_hash: editor.hash || undefined, force, create: editor.create,
      }),
    });
    if (editor.path !== editorPath || editor.kind !== editorKind || editor.sessionID !== sessionID) return;
    Object.assign(editor, {
      size: Number(data.file.size) || 0, modifiedAt: data.file.modified_at || "", hash: data.file.hash || "", lineEnding: data.file.line_ending || "lf", mixed: false, encoding: data.file.encoding || "utf-8",
      link: data.file.link || "",
      create: false, original: content,
    });
    editor.busy = false; editorRender();
    editorStatus(data.file.mode_unknown
      ? `Saved ${new Date().toLocaleTimeString()}.`
      : `Saved ${new Date().toLocaleTimeString()}`, data.file.mode_unknown ? "" : "ok");
    if ((editor.kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) && panes[editor.kind].path === (editor.kind === "local" ? LocalParentPath(editor.path) : RemoteParentPath(editor.path))) loadPane(editor.kind, panes[editor.kind].path, { keepSelection: true });
  } catch (error) {
    if (editor.path !== editorPath || editor.kind !== editorKind || editor.sessionID !== sessionID) return;
    editor.busy = false; editorRender();
    editorStatus(error.message, "error");
    if (error.code === "conflict") {
      const overwrite = await ask({ title: "File changed since it was opened", message: `“${editor.name}” changed after you opened it. Overwrite?`, input: false, ok: "Overwrite", danger: true });
      if (overwrite && editor.path === editorPath && editor.sessionID === sessionID && (editor.kind === "local" || isFileSessionCurrent(sessionID, sessionKey))) await editorSave(true);
    } else if (error.code === "exists") {
      editor.create = false;
      editorRender();
    } else if (sessionLost(error)) {
      handleSessionLost(sessionID);
    }
  }
}
const RemoteParentPath = (value) => { const index = value.lastIndexOf("/"); return index < 0 ? "." : index === 0 ? "/" : value.slice(0, index); };
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
  const sessionID = editor.sessionID, sessionKey = editor.sessionKey, editorPath = editor.path, editorKind = editor.kind;
  if (!(await editorDiscardOK(editor.kind === "local" ? "Reload from this computer?" : "Reload from the device?"))) return;
  if (editor.path !== editorPath || editor.kind !== editorKind || (editor.kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey))) { editorStatus("The original device connection is no longer active.", "error"); return; }
  try {
    const data = await api(`${editorURL()}?path=${encodeURIComponent(editor.path)}`);
    if (editor.path !== editorPath || editor.sessionID !== sessionID) return;
    showEditor(editor.kind, data.file, false, sessionID, sessionKey);
    editorStatus(editor.kind === "local" ? "Reloaded from this computer" : "Reloaded from the device", "ok");
  } catch (error) { editorStatus(error.message, "error"); if (sessionLost(error)) handleSessionLost(sessionID); else editorError(editor.kind, error, sessionID); }
}
{
  const dialog = $("#editorDialog"), text = $("#editorText");
  text.addEventListener("input", () => { editorRender(); if ($("#editorStatus").dataset.kind) editorStatus(""); });
  for (const type of ["keyup", "click", "focus"]) text.addEventListener(type, editorCursor);
  dialog.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); if (!$("#editorSave").disabled) editorSave(); }
  });
  // Tab inserts a tab (and indents or outdents a multi-line selection) instead of leaving the editor.
  // Shift+Tab with no selection and Esc, Tab still move focus out, so the keyboard is never trapped.
  text.addEventListener("keydown", (event) => {
    if (event.key !== "Tab" || event.ctrlKey || event.metaKey || event.altKey || text.readOnly) return;
    const { selectionStart: from, selectionEnd: to, value } = text;
    const multi = value.slice(from, to).includes("\n");
    if (event.shiftKey && from === to) return;
    event.preventDefault();
    if (!multi && !event.shiftKey) { text.setRangeText("\t", from, to, "end"); }
    else {
      const start = value.lastIndexOf("\n", from - 1) + 1;
      const end = to > from && value[to - 1] === "\n" ? to - 1 : to;
      const lines = value.slice(start, end).split("\n");
      const changed = event.shiftKey ? lines.map((line) => line.replace(/^(\t| {1,4})/, "")) : lines.map((line) => "\t" + line);
      const block = changed.join("\n");
      text.setRangeText(block, start, end, "preserve");
      text.setSelectionRange(start, start + block.length);
    }
    text.dispatchEvent(new Event("input", { bubbles: true }));
  });
  dialog.addEventListener("cancel", (event) => { event.preventDefault(); editorClose(); });
  $("#editorSave").addEventListener("click", () => editorSave());
  $("#editorClose").addEventListener("click", editorClose);
  $("#editorReload").addEventListener("click", editorReload);
  $("#editorWrap").addEventListener("change", (event) => { store.set("editorWrap", event.target.checked); text.setAttribute("wrap", event.target.checked ? "soft" : "off"); });
  window.addEventListener("beforeunload", (event) => { if (dialog.open && editorDirty()) { event.preventDefault(); event.returnValue = ""; } });
}


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


function updateHistoryButtons(pane) {
  if (!pane.backBtn) return;
  const available = pane.loaded && !pane.historyBusy && (pane.kind !== "remote" || Boolean(files.sessionID));
  const rootFallback = Boolean(pane.rootPath && pane.path !== pane.rootPath && pane.hpos <= 0);
  pane.backBtn.disabled = !available || (pane.hpos <= 0 && !rootFallback);
  pane.fwdBtn.disabled = !available || pane.hpos < 0 || pane.hpos >= pane.hist.length - 1;
}
async function stepPaneHistory(kind, delta) {
  const pane = panes[kind];
  const to = pane.hpos + delta;
  if (pane.historyBusy || (kind === "remote" && !files.sessionID)) return false;
  const target = to >= 0 && to < pane.hist.length ? pane.hist[to] : delta < 0 && pane.rootPath && pane.path !== pane.rootPath ? pane.rootPath : "";
  if (!target) return false;
  pane.historyBusy = true;
  updateHistoryButtons(pane);
  try {
    const ok = await loadPane(kind, target, { record: false });
    if (ok) {
      if (to >= 0 && to < pane.hist.length) pane.hpos = to;
      else { pane.hist = [pane.rootPath]; pane.hpos = 0; }
    }
    return ok;
  } finally {
    pane.historyBusy = false;
    updateHistoryButtons(pane);
  }
}
for (const pane of Object.values(panes)) {
  const up = $('.pathbar .icon-btn[data-act="up"]', pane.root);
  if (!up) continue;
  const make = (name, title, delta, direction) => h("button", {
    type: "button", class: `icon-btn history-${direction}`, title, "aria-label": title,
    dataset: { history: String(delta) }, disabled: true,
  }, icon(name));
  pane.backBtn = make("left", "Back (Alt+Left)", -1, "back");
  pane.fwdBtn = make("right", "Forward (Alt+Right)", 1, "forward");
  up.before(pane.backBtn, pane.fwdBtn);
  pane.root.addEventListener("click", (event) => {
    const button = event.target.closest?.("button[data-history]");
    if (!button || !pane.root.contains(button)) return;
    event.preventDefault();
    stepPaneHistory(pane.kind, Number(button.dataset.history));
  });
  updateHistoryButtons(pane);
  pane.root.addEventListener("mousedown", (event) => {
    if (event.button === 3 || event.button === 4) {
      event.preventDefault();
      stepPaneHistory(pane.kind, event.button === 3 ? -1 : 1);
    }
  });
}
const filesKeyReady = () => view.current === "files" && !$("dialog[open]");
document.addEventListener("keydown", (event) => {
  if (!filesKeyReady() || !event.altKey || event.ctrlKey || event.metaKey) return;
  const tag = event.target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
  if (event.key === "ArrowLeft") { event.preventDefault(); stepPaneHistory(files.active, -1); }
  else if (event.key === "ArrowRight") { event.preventDefault(); stepPaneHistory(files.active, 1); }
});

const typeAhead = { text: "", timer: 0 };
document.addEventListener("keydown", (event) => {
  if (!filesKeyReady() || event.ctrlKey || event.metaKey || event.altKey || event.key.length !== 1 || event.key === " ") return;
  const tag = event.target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || event.target.isContentEditable) return;
  const pane = panes[files.active];
  if (!pane.visible.length) return;
  cancelPendingRename(pane);
  event.preventDefault();
  typeAhead.text += event.key.toLowerCase();
  window.clearTimeout(typeAhead.timer);
  typeAhead.timer = window.setTimeout(() => { typeAhead.text = ""; }, 900);
  const names = pane.visible.map((e) => e.name.toLowerCase());
  const repeat = typeAhead.text.length > 1 && [...typeAhead.text].every((c) => c === typeAhead.text[0]);
  const needle = repeat ? typeAhead.text[0] : typeAhead.text;
  const from = repeat || typeAhead.text.length === 1 ? pane.cursor + 1 : Math.max(pane.cursor, 0);
  let index = -1;
  for (let i = 0; i < names.length && index < 0; i++) { const at = (from + i) % names.length; if (names[at].startsWith(needle)) index = at; }
  if (index < 0) return;
  pane.selected = new Set([pane.visible[index].path]); pane.anchor = pane.cursor = index;
  updateSelectionUI(pane);
  $(`tr[data-index="${index}"]`, pane.rows)?.scrollIntoView({ block: "nearest" });
});

const preview = { pane: null, index: -1, seq: 0, sessionID: "", sessionKey: "" };
async function showPreview(pane, index) {
  const entry = pane.visible[index];
  if (!entry || entry.is_dir) return false;
  const sessionID = pane.kind === "remote" ? files.sessionID : "";
  const sessionKey = pane.kind === "remote" ? files.sessionKey : "";
  if (pane.kind === "remote" && !requireSession()) return false;
  cancelPendingRename(pane);
  const seq = ++preview.seq;
  const dialog = $("#previewDialog");
  $("#previewTitle").textContent = entry.name;
  $("#previewPath").textContent = entry.path;
  $("#previewMeta").textContent = `${formatBytes(entry.size)} · ${formatDate(entry.modified_at)}`;
  $("#previewNote").hidden = true;
  const body = $("#previewText");
  body.textContent = "Loading…";
  preview.pane = pane; preview.index = index; preview.sessionID = sessionID; preview.sessionKey = sessionKey;
  pane.selected = new Set([entry.path]); pane.anchor = pane.cursor = index; updateSelectionUI(pane);
  $(`tr[data-index="${index}"]`, pane.rows)?.scrollIntoView({ block: "nearest" });
  if (!dialog.open) openDialog(dialog);
  try {
    const url = pane.kind === "local" ? "/api/v1/files/local/content" : sessionURL(sessionID, "content");
    const data = await api(`${url}?path=${encodeURIComponent(entry.path)}`);
    if (seq !== preview.seq) return true;
    if (pane.kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) return true;
    const text = String(data.file?.content ?? "");
    const CUT = 200000;
    body.textContent = text.length > CUT ? text.slice(0, CUT) : (text || "(empty file)");
    body.scrollTop = 0;
    $("#previewNote").hidden = text.length <= CUT;
    $("#previewNote").textContent = "Only the beginning is shown.";
  } catch (error) {
    if (seq !== preview.seq) return true;
    if (pane.kind === "remote" && sessionLost(error)) { dialog.close(); handleSessionLost(sessionID); return true; }
    body.textContent = error.code === "not_text" ? "This file is not text, so it cannot be previewed here."
      : error.code === "too_large" ? "This file is too large to preview here. Download it instead."
      : error.message;
  }
  return true;
}
function cmdPreview() {
  const pane = panes[files.active];
  if (pane.kind === "remote" && !requireSession()) return;
  let index = pane.cursor;
  if (index < 0 || !pane.visible[index]) index = pane.visible.findIndex((e) => pane.selected.has(e.path));
  const entry = pane.visible[index];
  if (!entry || entry.is_dir) { toast("Select a file first"); return; }
  showPreview(pane, index);
}
{
  const dialog = $("#previewDialog");
  const step = (delta) => {
    const pane = preview.pane;
    if (!pane) return;
    for (let i = preview.index + delta; i >= 0 && i < pane.visible.length; i += delta) if (!pane.visible[i].is_dir) { showPreview(pane, i); return; }
  };
  dialog.addEventListener("keydown", (event) => {
    const onButton = event.target.tagName === "BUTTON";
    if (event.key === " " && !onButton) { event.preventDefault(); dialog.close(); }
    else if ((event.key === "ArrowDown" || event.key === "ArrowRight") && !onButton) { event.preventDefault(); step(1); }
    else if ((event.key === "ArrowUp" || event.key === "ArrowLeft") && !onButton) { event.preventDefault(); step(-1); }
  });
  $("#previewClose").addEventListener("click", () => dialog.close());
  $("#previewEdit").addEventListener("click", () => {
    const pane = preview.pane, entry = pane?.visible[preview.index];
    dialog.close();
    if (entry) { setActivePane(pane.kind); openEditor(pane.kind, entry.path); }
  });
  dialog.addEventListener("click", (event) => { if (event.target === dialog) dialog.close(); });
  dialog.addEventListener("close", () => { preview.seq++; $("#previewText").textContent = ""; preview.pane?.table.focus?.(); });
}

const dirOf = (kind, path) => (kind === "local" ? LocalParentPath(path) : RemoteParentPath(path));
const baseOf = (path) => path.split("/").filter(Boolean).pop() || path;
function pushUndo(kind, label, pairs, profileID = files.profileID, sessionKey = files.sessionKey) {
  if (!pairs.length) return;
  files.undo.push({ kind, label: pairs.length > 1 ? `${label} ${pairs.length} items` : `${label} “${baseOf(pairs[0].to)}”`, pairs, profileID: kind === "remote" ? profileID : "", sessionKey: kind === "remote" ? sessionKey : "" });
  if (files.undo.length > 20) files.undo.shift();
}
function lastUndo(kind) {
  for (let i = files.undo.length - 1; i >= 0; i--) {
    const item = files.undo[i];
    if (item.kind === kind && (kind === "local" || (files.sessionID && item.profileID === files.profileID && item.sessionKey === files.sessionKey))) return item;
  }
  return null;
}
async function cmdUndo() {
  const kind = files.active;
  if (kind === "remote" && !requireSession()) return;
  const sessionID = kind === "remote" ? files.sessionID : "";
  const sessionKey = kind === "remote" ? files.sessionKey : "";
  const item = lastUndo(kind);
  if (!item) { toast("Nothing to undo"); return; }
  files.undo.splice(files.undo.indexOf(item), 1);
  const list = (dir) => (kind === "local" ? api(`/api/v1/files/local/list?path=${encodeURIComponent(dir)}`) : api(sessionURL(sessionID, `list?path=${encodeURIComponent(dir)}`)));
  const taken = new Map();
  let restored = 0, blocked = 0, failed = 0, last = null;
  for (const pair of [...item.pairs].reverse()) {
    if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Undo stopped before changing another device."); break; }
    try {
      const dir = dirOf(kind, pair.to);
      if (!taken.has(dir)) {
        const result = await list(dir);
        if (kind === "remote" && !isFileSessionCurrent(sessionID, sessionKey)) { failed++; last = new Error("The receiver connection changed. Undo cancelled."); break; }
        taken.set(dir, new Set((result.entries || []).map((e) => e.name)));
      }
      const names = taken.get(dir);
      if (names.has(baseOf(pair.to))) { blocked++; continue; }
      const body = { old_path: pair.from, new_path: pair.to };
      if (kind === "local") await post("/api/v1/files/local/rename", body); else await post(sessionURL(sessionID, "rename"), body);
      names.add(baseOf(pair.to)); restored++;
    } catch (error) { failed++; last = error; if (kind === "remote" && sessionLost(error)) { handleSessionLost(sessionID); return; } }
  }
  if (kind === "local" || isFileSessionCurrent(sessionID, sessionKey)) await refreshPane(kind);
  if (failed) toast(`Could not undo: ${last.message}`, "error");
  else if (blocked && !restored) toast("Could not undo: the original name is taken now", "error");
  else if (blocked) toast(`Undone ${restored}, ${blocked} skipped: the original name is taken now`);
  else toast(`Undone: ${item.label}`);
}

