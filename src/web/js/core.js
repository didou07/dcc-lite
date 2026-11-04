"use strict";
/* DCC Lite — Devices, Files (FTP), Terminal (Telnet) and Live screen. */

/* ───────────── Helpers ───────────── */
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
const SVG_NS = "http://www.w3.org/2000/svg";

const store = {
  get(key, fallback) {
    try { const raw = localStorage.getItem(`dcclite.${key}`) ?? localStorage.getItem(`boxdeck.${key}`) ?? localStorage.getItem(`dccnext.${key}`); return raw === null ? fallback : JSON.parse(raw); } catch { return fallback; }
  },
  set(key, value) { try { localStorage.setItem(`dcclite.${key}`, JSON.stringify(value)); } catch { /* storage unavailable */ } },
};

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { ...(options.body ? { "Content-Type": "application/json" } : {}), ...(options.headers || {}) },
  });
  if (response.status === 204) return null;
  const data = await response.json().catch(() => ({}));
  if (response.status === 401 && data.code === "auth_required") { window.location.assign("/login"); }
  if (!response.ok) {
    const error = new Error(data.error || `Request failed (${response.status})`);
    error.code = data.code || "";
    error.status = response.status;
    throw error;
  }
  return data;
}
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
function iconButton(name, title, onClick, extra = "") {
  return h("button", { class: `icon-btn ${extra}`.trim(), type: "button", title, "aria-label": title, onClick }, icon(name));
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
function formatDate(value) {
  const time = Date.parse(value || "");
  if (!time || time < 86400000) return "—";
  const d = new Date(time), pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
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

/* ───────────── Dialogs & menus ───────────── */
function openDialog(dialog) { if (!dialog.open) dialog.showModal(); }

/** Ask for text (or just confirm when `input` is false). Resolves to the string / true, or null when cancelled. */
function ask({ title, message = "", label = "", value = "", input = true, ok = "OK", danger = false, validate = null }) {
  const dialog = $("#askDialog");
  $("#askTitle").textContent = title;
  $("#askMessage").textContent = message;
  $("#askField").hidden = !input;
  $("#askLabel").textContent = label;
  const field = $("#askInput");
  field.value = value;
  const okButton = $("#askOk");
  okButton.textContent = ok;
  okButton.classList.toggle("danger", danger);
  okButton.classList.toggle("primary", !danger);
  const error = $("#askError");
  error.hidden = true;
  return new Promise((resolve) => {
    let finished = false;
    const finish = (result) => {
      if (finished) return;
      finished = true;
      form.removeEventListener("submit", onSubmit);
      $("#askCancel").removeEventListener("click", onCancel);
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
    $("#askCancel").addEventListener("click", onCancel);
    dialog.addEventListener("cancel", onCancel);
    // A "close" event queued by a previous prompt can arrive after this one opened; only react when it is really closed.
    const onClose = () => { if (!dialog.open) finish(null); };
    dialog.addEventListener("close", onClose);
    openDialog(dialog);
    if (input) { field.focus(); field.select(); } else okButton.focus();
  });
}

const menuState = { onClose: null };
function closeMenu() { const menu = $("#menu"); if (!menu.hidden) { menu.hidden = true; menu.replaceChildren(); } }
/** items: [{label, icon, run, danger, disabled, sub}] | "-" | {heading: "text"} */
function openMenu(items, x, y) {
  const menu = $("#menu");
  menu.replaceChildren();
  for (const item of items) {
    if (item === "-") { menu.append(h("hr")); continue; }
    if (item.heading) { menu.append(h("div", { class: "label", text: item.heading })); continue; }
    menu.append(h("button", {
      type: "button", role: "menuitem", class: item.danger ? "danger" : "", disabled: item.disabled,
      onClick: () => { closeMenu(); item.run?.(); },
    }, item.icon ? icon(item.icon, "sm") : null, h("span", { class: "ellipsis", text: item.label }), item.sub ? h("span", { class: "sub", text: item.sub }) : null));
  }
  menu.hidden = false;
  const width = menu.offsetWidth, height = menu.offsetHeight;
  menu.style.left = `${Math.max(6, Math.min(x, window.innerWidth - width - 6))}px`;
  menu.style.top = `${Math.max(6, Math.min(y, window.innerHeight - height - 6))}px`;
  menu.querySelector("button:not(:disabled)")?.focus({ preventScroll: true });
}
document.addEventListener("pointerdown", (event) => { if (!event.target.closest("#menu")) closeMenu(); });
document.addEventListener("keydown", (event) => { if (event.key === "Escape") closeMenu(); });
window.addEventListener("blur", closeMenu);
window.addEventListener("resize", closeMenu);

/* ───────────── Theme ───────────── */
function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  store.set("theme", theme);
}
function effectiveTheme() {
  const theme = document.documentElement.dataset.theme;
  return theme === "auto" ? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : theme;
}
applyTheme(store.get("theme", "auto"));
$("#themeToggle").addEventListener("click", () => applyTheme(effectiveTheme() === "dark" ? "light" : "dark"));
