"use strict";

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
const SVG_NS = "http://www.w3.org/2000/svg";

const store = {
  get(key, fallback) {
    try { const raw = localStorage.getItem(`dcclite.${key}`); return raw === null ? fallback : JSON.parse(raw); } catch { return fallback; }
  },
  set(key, value) { try { localStorage.setItem(`dcclite.${key}`, JSON.stringify(value)); } catch { } },
};

function authExpired(status, data) {
  if (status !== 401 || (data !== undefined && data?.code !== "auth_required")) return false;
  window.location.assign("/login");
  return true;
}

const ERROR_ACTIONABLE = /\b(check|try|edit|press|enable|untick|choose|use|reload|restart|wait|open|add|install|set)\b/i;
const ERROR_BY_CODE = {
  session_not_found: "Connection closed. Press Connect.",
  conflict: "The file changed on the receiver. Reload it first.",
  exists: "That name already exists.",
  too_large: "The file is too large to open.",
  not_text: "Not a text file.",
  file_not_found: "That file no longer exists.",
  still_running: "A job is still running.",
  host_files_unavailable: "Not available in a remote session.",
  web_error: "",
  web_unsupported: "Not supported by this web interface.",
};
const ERROR_PATTERNS = [
  [/login failed|\b530\b|authentication failed/i, "Wrong user name or password."],
  [/connection refused/i, "Nothing is listening on that port."],
  [/i\/o timeout|timed out|deadline exceeded/i, "The receiver did not answer in time."],
  [/no route to host|network is unreachable|no such host/i, "The receiver cannot be reached."],
  [/connection reset|broken pipe|use of closed|unexpected eof|^eof$/i, "Connection dropped. Press Connect."],
  [/not empty/i, "The folder is not empty."],
  [/no such file|not found|does not exist/i, "That item no longer exists."],
  [/permission denied|\b550\b|read-only file system/i, "The receiver does not allow this."],
  [/no space left|disk quota/i, "The receiver's storage is full."],
];
const ERROR_BY_STATUS = {
  400: "The request was not accepted.",
  403: "This is not allowed for this connection.",
  404: "That item no longer exists.",
  409: "It was changed in the meantime.",
  413: "That is too large for this action.",
  422: "The data was refused.",
  429: "Too many requests. Wait a moment.",
  500: "Internal problem. See Logs.",
  502: "The receiver did not answer properly.",
  503: "The service is not available.",
  504: "The receiver took too long to answer.",
};
function friendlyError(status, code, text) {
  const raw = (text || "").trim();
  if (code && ERROR_BY_CODE[code]) return ERROR_BY_CODE[code];
  if (raw && ERROR_ACTIONABLE.test(raw) && !/^Request failed/i.test(raw)) return raw;
  for (const [pattern, message] of ERROR_PATTERNS) if (pattern.test(raw)) return message;
  if (code === "web_error" && raw) return raw;
  if (!raw || /^Request failed/i.test(raw)) return ERROR_BY_STATUS[status] || `Something went wrong (${status}).`;
  return raw;
}

async function responseError(response, fallback = "Request failed") {
  const data = await response.json().catch(() => ({}));
  const error = new Error(friendlyError(response.status, data.code, data.error || `${fallback} (${response.status})`));
  error.raw = data.error || "";
  error.code = data.code || "";
  error.status = response.status;
  error.fatal = response.status === 404 || response.status === 409;
  error.auth = authExpired(response.status, data);
  return error;
}

async function fetchChecked(path, options = {}) {
  let response;
  try { response = await fetch(path, options); }
  catch (error) {
    if (error?.name === "AbortError") throw error;
    const failure = new Error("Cannot reach DCC Lite. Check that it is still running.");
    failure.network = true;
    throw failure;
  }
  if (!response.ok) throw await responseError(response);
  return response;
}

async function api(path, options = {}) {
  const response = await fetchChecked(path, {
    ...options,
    headers: { ...(options.body ? { "Content-Type": "application/json" } : {}), ...(options.headers || {}) },
  });
  if (response.status === 204) return null;
  return response.json().catch(() => ({}));
}
const systemInfo = () => (systemInfo.pending ||= api("/api/v1/system"));
const post = (path, body) => api(path, { method: "POST", body: JSON.stringify(body ?? {}) });

function h(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") node.className = value;
    else if (key === "dataset") Object.assign(node.dataset, value);
    else if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2).toLowerCase(), value);
    else if (key === "text") node.textContent = value;
    else node.setAttribute(key, value === true ? "" : String(value));
  }
  for (const child of children.flat()) {
    if (child === undefined || child === null || child === false) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return node;
}
function icon(name, extra = "") {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", `ic ${extra}`.trim());
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS(SVG_NS, "use");
  use.setAttribute("href", `#i-${name}`);
  svg.append(use);
  return svg;
}
// One button for connect and disconnect: its icon and colour show the state, the tooltip says what a click does.
function connState(button, state, tip) {
  if (!button) return;
  button.hidden = false;
  button.dataset.conn = state;
  button.querySelector("use")?.setAttribute("href", state === "off" ? "#i-plug-off" : state === "busy" ? "#i-refresh" : "#i-plug");
  button.title = tip;
  button.setAttribute("aria-label", tip);
}

function iconButton(name, title, onClick, extra = "") {
  return h("button", { class: `icon-btn ${extra}`.trim(), type: "button", title, "aria-label": title, onClick }, icon(name));
}

function setStatusDot(node, state, text) {
  if (!node) return;
  node.dataset.state = state;
  node.textContent = "";
  node.title = text;
  node.setAttribute("role", "img");
  node.setAttribute("aria-label", text);
}

let toastTimer = 0;
function toast(message, kind = "info") {
  const node = $("#toast");
  window.clearTimeout(toastTimer);
  node.textContent = String(message ?? "");
  node.classList.toggle("error", kind === "error");
  node.classList.add("show");
  toastTimer = window.setTimeout(() => node.classList.remove("show"), kind === "error" ? 5200 : 2600);
}
const toastError = (error) => toast(error?.message || String(error), "error");

function formatBytes(value) {
  const size = Number(value) || 0;
  if (size < 1024) return `${size} B`;
  if (size < 1048576) return `${(size / 1024).toFixed(1)} KB`;
  if (size < 1073741824) return `${(size / 1048576).toFixed(1)} MB`;
  return `${(size / 1073741824).toFixed(2)} GB`;
}
function formatETA(value) {
  const seconds = Math.max(0, Math.floor(Number(value) || 0));
  if (!seconds) return "";
  const hours = Math.floor(seconds / 3600), minutes = Math.floor((seconds % 3600) / 60), rest = seconds % 60;
  if (hours) return `${hours}h ${minutes}m left`;
  if (minutes) return `${minutes}m ${rest}s left`;
  return `${rest}s left`;
}
const pad2 = (n, width = 2) => String(n).padStart(width, "0");
const sleep = (ms) => new Promise((resolve) => window.setTimeout(resolve, ms));
function formatDate(value) {
  const time = Date.parse(value || "");
  if (!time || time < 86400000) return "—";
  const d = new Date(time);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}
// Starts a browser download.
function downloadLink(href, name = "", release = false) {
  const link = h("a", { href, download: name });
  document.body.append(link);
  link.click();
  link.remove();
  if (release) window.setTimeout(() => URL.revokeObjectURL(href), 2000);
}
const downloadBlob = (blob, name) => downloadLink(URL.createObjectURL(blob), name, true);

const profileURL = (id, tail = "") => `/api/v1/profiles/${encodeURIComponent(id)}${tail ? `/${tail}` : ""}`;
const sessionURL = (id, tail = "") => `/api/v1/files/sessions/${encodeURIComponent(id)}${tail ? `/${tail}` : ""}`;
const transferURL = (id, tail = "") => `/api/v1/files/transfers/${encodeURIComponent(id)}${tail ? `/${tail}` : ""}`;

async function clipboardWrite(text) {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(text); return true; } catch { }
  }
  const active = document.activeElement;
  const box = h("textarea", { "aria-hidden": "true", tabindex: "-1", readonly: true, style: "position:fixed;left:-9999px;top:0;opacity:0" });
  box.value = text;
  document.body.append(box);
  box.select();
  box.setSelectionRange(0, text.length);
  let ok = false;
  try { ok = document.execCommand("copy"); } catch { ok = false; }
  box.remove();
  active?.focus?.({ preventScroll: true });
  return ok;
}
async function clipboardRead() {
  if (window.isSecureContext && navigator.clipboard?.readText) {
    try { return await navigator.clipboard.readText(); } catch { return null; }
  }
  return null;
}
function permissionOctal(mode) {
  const value = String(mode || "").trim();
  if (!value) return "—";
  if (/^[0-7]{3,4}$/.test(value)) return value.length === 4 && value[0] === "0" ? value.slice(1) : value;
  if (value.length < 10) return "—";
  const bits = [];
  let special = 0;
  for (let group = 0; group < 3; group++) {
    const offset = 1 + group * 3;
    const exec = value[offset + 2];
    bits.push((value[offset] === "r" ? 4 : 0) + (value[offset + 1] === "w" ? 2 : 0) + (exec && "xst".includes(exec.toLowerCase()) ? 1 : 0));
    if (group === 0 && (exec === "s" || exec === "S")) special |= 4;
    if (group === 1 && (exec === "s" || exec === "S")) special |= 2;
    if (group === 2 && (exec === "t" || exec === "T")) special |= 1;
  }
  return special ? `${special}${bits.join("")}` : bits.join("");
}
const joinLocal = (base, name) => (base === "." || base === "" ? name : `${base.replace(/\/$/, "")}/${name}`);
const joinRemote = (base, name) => (base === "/" ? `/${name}` : base === "." || base === "" ? name : `${base.replace(/\/$/, "")}/${name}`);
const shellQuote = (value) => `'${String(value).replace(/'/g, "'\\''")}'`;
const validName = (name) => name && name !== "." && name !== ".." && !/[\\/\0\r\n]/.test(name) && name.length <= 255;

function openDialog(dialog) { if (!dialog.open) dialog.showModal(); }

function ask({ title, message = "", label = "", value = "", input = true, ok = "OK", alt = "", alt2 = "", danger = false, validate = null, icon: iconName = "info" }) {
  const dialog = $("#askDialog");
  dialog.querySelector(".dlg-head > .ic use")?.setAttribute("href", `#i-${iconName}`);
  $("#askTitle").textContent = title;
  $("#askMessage").textContent = message;
  $("#askField").hidden = !input;
  $("#askLabel").textContent = label;
  const field = $("#askInput");
  field.value = value;
  const okButton = $("#askOk");
  okButton.replaceChildren(...(iconName === "trash" ? [icon("trash")] : []), ok);
  okButton.classList.toggle("danger", danger);
  okButton.classList.toggle("primary", !danger);
  const altButton = $("#askAlt");
  altButton.hidden = !alt;
  altButton.textContent = alt;
  const alt2Button = $("#askAlt2");
  alt2Button.hidden = !alt2;
  alt2Button.textContent = alt2;
  const error = $("#askError");
  error.hidden = true;
  return new Promise((resolve) => {
    let finished = false;
    const finish = (result) => {
      if (finished) return;
      finished = true;
      form.removeEventListener("submit", onSubmit);
      $("#askCancel").removeEventListener("click", onCancel);
      altButton.removeEventListener("click", onAlt);
      alt2Button.removeEventListener("click", onAlt2);
      dialog.removeEventListener("cancel", onCancel);
      dialog.removeEventListener("close", onClose);
      if (dialog.open) dialog.close();
      resolve(result);
    };
    const form = $("#askForm");
    const onSubmit = (event) => {
      event.preventDefault();
      const text = field.value.trim();
      if (input && validate) {
        const problem = validate(text);
        if (problem) { error.textContent = problem; error.hidden = false; field.focus(); return; }
      }
      finish(input ? text : true);
    };
    const onCancel = (event) => { event?.preventDefault?.(); finish(null); };
    form.addEventListener("submit", onSubmit);
    const onAlt = () => finish("alt");
    const onAlt2 = () => finish("alt2");
    $("#askCancel").addEventListener("click", onCancel);
    altButton.addEventListener("click", onAlt);
    alt2Button.addEventListener("click", onAlt2);
    dialog.addEventListener("cancel", onCancel);
    const onClose = () => { if (!dialog.open) finish(null); };
    dialog.addEventListener("close", onClose);
    openDialog(dialog);
    if (input) { field.focus(); field.select(); } else okButton.focus();
  });
}

// A menu opened with keepFocus leaves the keyboard focus where it was (a text box with a selection), so
// the selection stays coloured while the menu is open. These keys then move into the menu or close it.
let menuKeys = null;
function closeMenu() {
  if (menuKeys) { document.removeEventListener("keydown", menuKeys, true); menuKeys = null; }
  const menu = $("#menu");
  if (!menu.hidden) { try { if (menu.matches(":popover-open")) menu.hidePopover(); } catch { } menu.hidden = true; menu.replaceChildren(); if (menu.parentElement !== document.body) document.body.append(menu); }
}

// Every menu entry gets an icon: the one it names, or one picked from its wording.
const MENU_ICONS = [
  [/from the list|^remove$/i, "minus"], [/\bcut\b/i, "scissors"], [/\bcopy\b|duplicate/i, "copy"], [/paste/i, "paste"],
  [/select all|invert/i, "select"], [/rename|edit|marker/i, "edit"], [/delete|remove|clear|erase/i, "trash"],
  [/to top|first/i, "chev-up"], [/to bottom|last/i, "chev-down"], [/sort|reverse|order/i, "sort"], [/new folder/i, "newfolder"],
  [/download|export|save/i, "download"], [/upload|import/i, "upload"], [/deep standby|shut|power|standby/i, "power"],
  [/wake|wol/i, "sun"], [/reboot|restart|reload|refresh|retry|check|scan/i, "refresh"], [/permission|chmod|lock|password/i, "lock"],
  [/terminal|shell|command/i, "terminal"], [/run|start|play|open|connect/i, "play"], [/stop|disconnect|close|cancel/i, "stop"],
  [/undo/i, "undo"], [/redo/i, "redo"], [/bookmark|pin|favou?rite/i, "star"], [/stream|link|url/i, "link"], [/preview|view|show/i, "eye"],
  [/hex|code|script/i, "code"], [/info|propert|detail/i, "info"], [/folder|place|path|home/i, "folder"], [/find|search|filter/i, "search"],
  [/device|receiver|site/i, "devices"], [/log|fault|error/i, "bug"], [/move|swap/i, "swap"],
];
function menuIcon(item) {
  if (item.icon) return item.icon;
  if (!item.label || (item.disabled && /^no /i.test(item.label))) return "";
  for (const [pattern, name] of MENU_ICONS) if (pattern.test(item.label)) return name;
  return "more";
}
function openMenu(items, x, y, { search = "", keepFocus = false } = {}) {
  const menu = $("#menu");
  if (menuKeys) { document.removeEventListener("keydown", menuKeys, true); menuKeys = null; }
  menu.replaceChildren();
  let box = null;
  if (search) {
    box = h("input", { class: "menu-search", type: "search", placeholder: search, "aria-label": search, spellcheck: "false", autocomplete: "off" });
    menu.append(box);
  }
  for (const item of items) {
    if (item === "-") { menu.append(h("hr")); continue; }
    if (item.heading) { menu.append(h("div", { class: "label", text: item.heading })); continue; }
    const main = h("button", {
      type: "button", role: "menuitem", dataset: item.searchable ? { find: `${item.label} ${item.find || item.sub || ""}`.toLowerCase(), searchable: "1" } : { find: "" }, class: item.danger ? "danger" : "", disabled: item.disabled,
      onClick: () => { closeMenu(); item.run?.(); },
    }, menuIcon(item) ? icon(menuIcon(item), "sm") : null, h("span", { class: "ellipsis", text: item.label }), item.sub ? h("span", { class: "sub", text: item.sub }) : null);
    const extras = [];
    if (item.runNow) extras.push({ icon: "play", title: "Run now", label: `Run ${item.label} now`, cls: "menu-run", run: item.runNow });
    for (const action of item.actions || []) extras.push({ icon: action.icon, title: action.title, label: `${action.title}: ${item.label}`, cls: `menu-act${action.danger ? " danger" : ""}`, run: action.run });
    if (!extras.length) { menu.append(main); continue; }
    // Small buttons at the end of the row.
    menu.append(h("div", { class: "menu-row" }, main,
      ...extras.map((extra) => h("button", { type: "button", role: "menuitem", class: extra.cls, title: extra.title, "aria-label": extra.label, onClick: () => { closeMenu(); extra.run(); } }, icon(extra.icon, "sm")))));
  }
  if (box) {
    box.addEventListener("input", () => {
      const q = box.value.trim().toLowerCase();
      menu.querySelectorAll("button[data-find]").forEach((b) => { const row = b.closest(".menu-row") || b; row.hidden = Boolean(q) && b.dataset.searchable !== undefined && !b.dataset.find.includes(q); });
    });
    box.addEventListener("keydown", (event) => { if (event.key === "ArrowDown") { event.preventDefault(); menu.querySelector("button:not(:disabled)")?.focus(); } });
  }
  // Use all the room the window has.
  menu.style.maxHeight = `${Math.max(120, window.innerHeight - 12)}px`;
  menu.hidden = false;
  // A modal dialog (the file editor) lives in the browser's top layer and paints over every normal element,
  // whatever its z-index. A popover joins the top layer too and is added last, so the menu shows above it.
  // A modal dialog also makes everything outside it unclickable, so the menu moves inside the open dialog.
  const modal = [...document.querySelectorAll("dialog[open]")].pop();
  if (modal && menu.parentElement !== modal) modal.append(menu);
  else if (!modal && menu.parentElement !== document.body) document.body.append(menu);
  try { if (menu.matches(":popover-open")) menu.hidePopover(); menu.showPopover(); } catch { }
  const width = menu.offsetWidth, height = menu.offsetHeight;
  menu.style.left = `${Math.max(6, Math.min(x, window.innerWidth - width - 6))}px`;
  menu.style.top = `${Math.max(6, Math.min(y, window.innerHeight - height - 6))}px`;
  if (keepFocus && !box) {
    // Clicking an item must not move the focus away from the text box either.
    menu.querySelectorAll("button").forEach((b) => b.addEventListener("mousedown", (event) => event.preventDefault()));
    menuKeys = (event) => {
      if (menu.hidden || event.key === "Escape" || ["Shift", "Control", "Alt", "Meta"].includes(event.key)) return;
      if (menu.contains(document.activeElement)) return;
      const enabled = [...menu.querySelectorAll("button:not(:disabled)")];
      if ((event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Tab") && enabled.length) {
        event.preventDefault(); event.stopPropagation();
        (event.key === "ArrowUp" ? enabled[enabled.length - 1] : enabled[0]).focus({ preventScroll: true });
      } else if (!event.ctrlKey && !event.metaKey && !event.altKey) closeMenu(); // typing carries on as usual
    };
    document.addEventListener("keydown", menuKeys, true);
    return;
  }
  (box || menu.querySelector("button:not(:disabled)"))?.focus({ preventScroll: true });
}
document.addEventListener("pointerdown", (event) => { if (!event.target.closest("#menu")) closeMenu(); });
document.addEventListener("keydown", (event) => { if (event.key === "Escape") closeMenu(); });
window.addEventListener("blur", closeMenu);
window.addEventListener("resize", closeMenu);

function applyTheme(theme) {
  if (!["auto", "light", "dark"].includes(theme)) theme = "light";
  document.documentElement.dataset.theme = theme;
  store.set("theme", theme);
}
function effectiveTheme() {
  const theme = document.documentElement.dataset.theme;
  return theme === "auto" ? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : theme;
}
applyTheme(store.get("theme", "light"));
$("#themeToggle").addEventListener("click", () => applyTheme(effectiveTheme() === "dark" ? "light" : "dark"));

const protoName = (p) => ({ ftp: "FTP", telnet: "Telnet" })[p] || String(p || "");
