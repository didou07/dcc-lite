"use strict";

const palette = { items: [], shown: [], index: 0 };
const PALETTE_VIEWS = [
  { view: "files", label: "Files", icon: "files", kw: "ملفات ftp transfer" },
  { view: "terminal", label: "Terminal", icon: "terminal", kw: "طرفية telnet shell" },
  { view: "screen", label: "Screen", icon: "screen", kw: "شاشة live tv channels قنوات" },
  { view: "tools", label: "Tools", icon: "gear", kw: "أدوات" },
];

function paletteBuild() {
  const items = [];
  const add = (id, label, icon_, run, hint = "", kw = "") => items.push({ id, label, icon: icon_, run, hint, kw: `${label} ${hint} ${kw}`.toLowerCase() });
  add("go:devices", "Devices", "devices", () => showView("devices"), "Page", "الأجهزة");
  add("go:files", "Files", "files", () => showView("files"), "Page", "ملفات ftp");
  add("go:terminal", "Terminal", "terminal", () => showView("terminal"), "Page", "طرفية telnet");
  add("go:screen", "Screen", "screen", () => showView("screen"), "Page", "شاشة قنوات live");
  add("go:logs", "Logs", "bug", () => showView("logs"), "Page", "سجل");
  for (const t of TOOL_PAGES) {
    const kw = { editor: "قنوات باقات bouquets lamedb", backup: "نسخ احتياطي استرجاع restore points", ipk: "إضافة تثبيت plugin opkg", signal: "إشارة signal snr", packages: "حزم إضافات opkg" }[t.id] || "";
    add(`tool:${t.id}`, t.label, t.icon, () => showTool(t.id), "Tools", kw);
  }
  const guideTabs = [["epg", "Programmes", "برامج epg"], ["grid", "Grid", "جدول epg"], ["timers", "Timers", "مؤقتات تسجيل timer"], ["auto", "Auto timers", "مؤقتات تلقائية autotimer"], ["movies", "Recordings", "تسجيلات movies"]];
  for (const [tab, label, kw] of guideTabs) {
    add(`guide:${tab}`, `Guide: ${label}`, "calendar", () => { showView("screen"); window.setTimeout(() => openGuide(tab), 250); }, "Guide", kw);
  }
  add("act:add", "Add device", "plus", () => openDeviceDialog(), "Action", "إضافة جهاز");
  add("act:check", "Check all devices", "refresh", () => { showView("devices"); testAllDevices(); }, "Action", "فحص الأجهزة");
  add("act:keys", "Keyboard shortcuts", "keyboard", () => keysOpen(), "Action", "اختصارات لوحة المفاتيح shortcuts keys help");
  add("act:density", document.documentElement.dataset.density === "compact" ? "Use normal row height" : "Use compact rows", "rows", () => {
    const compact = document.documentElement.dataset.density === "compact";
    $(`#themeDialog .ap-seg[data-ap="density"] button[data-v="${compact ? "default" : "compact"}"]`)?.click();
  }, "Action", "مضغوط كثافة صفوف جداول density compact dense rows");
  add("act:theme", "Appearance", "palette", () => $("#themeOpen").click(), "Action", "مظهر ألوان");
  add("act:mode", "Switch light / dark theme", "sun", () => $("#themeToggle").click(), "Action", "داكن فاتح dark light");
  add("act:shot", "Save a screenshot of the receiver", "camera", () => { showView("screen"); window.setTimeout(() => { const b = $("#screenSave"); if (b && !b.disabled) b.click(); else toast("Open a channel on the Screen page first", "error"); }, 500); }, "Action", "لقطة شاشة screenshot");
  for (const profile of devices.list) {
    const name = profile.name;
    add(`dev:${profile.id}:select`, `${name} — make current`, "tv", () => setCurrentDevice(profile.id), profile.host, "جهاز");
    for (const v of PALETTE_VIEWS) add(`dev:${profile.id}:${v.view}`, `${name} — ${v.label}`, v.icon, () => openDevice(profile.id, v.view), profile.host, v.kw);
    add(`dev:${profile.id}:diag`, `${name} — Connection check`, "plug", () => openDiagnose(profile), profile.host, "فحص الاتصال diagnose test");
  }
  return items;
}

function paletteScore(item, tokens) {
  let score = 0;
  for (const token of tokens) {
    const at = item.kw.indexOf(token);
    if (at < 0) return -1;
    score += at === 0 || item.kw[at - 1] === " " ? 2 : 1;
    if (item.label.toLowerCase().startsWith(token)) score += 2;
  }
  return score;
}

function paletteFilter(query) {
  const tokens = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!tokens.length) {
    const recent = store.get("paletteRecent", []);
    const first = recent.map((id) => palette.items.find((i) => i.id === id)).filter(Boolean);
    const rest = palette.items.filter((i) => !i.id.startsWith("dev:") && !first.includes(i));
    return [...first, ...rest].slice(0, 30);
  }
  return palette.items.map((item) => [paletteScore(item, tokens), item]).filter(([s]) => s > 0)
    .sort((a, b) => b[0] - a[0]).slice(0, 40).map(([, item]) => item);
}

function paletteRender() {
  const list = $("#cmdList"), input = $("#cmdInput");
  palette.index = Math.max(0, Math.min(palette.index, palette.shown.length - 1));
  list.replaceChildren(...palette.shown.map((item, i) => h("li", {
    id: `cmdItem${i}`, role: "option", class: "cmd-item", "aria-selected": String(i === palette.index),
    onMousemove: () => { if (palette.index !== i) { palette.index = i; paletteMark(); } },
    onClick: () => paletteRun(item),
  }, icon(item.icon), h("span", { class: "cmd-label", text: item.label }), item.hint ? h("span", { class: "cmd-hint muted small", text: item.hint }) : null)));
  $("#cmdEmpty").hidden = palette.shown.length > 0;
  input.setAttribute("aria-activedescendant", palette.shown.length ? `cmdItem${palette.index}` : "");
}
function paletteMark() {
  for (const [i, node] of [...$("#cmdList").children].entries()) node.setAttribute("aria-selected", String(i === palette.index));
  $("#cmdInput").setAttribute("aria-activedescendant", `cmdItem${palette.index}`);
  $(`#cmdItem${palette.index}`)?.scrollIntoView({ block: "nearest" });
}
function paletteRun(item) {
  const recent = [item.id, ...store.get("paletteRecent", []).filter((id) => id !== item.id)].slice(0, 5);
  store.set("paletteRecent", recent);
  $("#cmdDialog").close();
  window.setTimeout(item.run, 0);
}
function paletteOpen() {
  const dialog = $("#cmdDialog");
  if (dialog.open) { dialog.close(); return; }
  if (document.querySelector("dialog[open]")) return; // never stack on top of another dialog
  palette.items = paletteBuild();
  $("#cmdInput").value = "";
  palette.shown = paletteFilter(""); palette.index = 0;
  paletteRender();
  openDialog(dialog);
  $("#cmdInput").focus();
}

$("#cmdInput").addEventListener("input", (event) => { palette.shown = paletteFilter(event.target.value); palette.index = 0; paletteRender(); });
$("#cmdInput").addEventListener("keydown", (event) => {
  if (event.key === "ArrowDown") { event.preventDefault(); palette.index = Math.min(palette.index + 1, palette.shown.length - 1); paletteMark(); }
  else if (event.key === "ArrowUp") { event.preventDefault(); palette.index = Math.max(palette.index - 1, 0); paletteMark(); }
  else if (event.key === "Enter" && palette.shown[palette.index]) { event.preventDefault(); paletteRun(palette.shown[palette.index]); }
});
$("#cmdDialog").addEventListener("click", (event) => { if (event.target === $("#cmdDialog")) $("#cmdDialog").close(); });
$("#cmdOpen")?.addEventListener("click", paletteOpen);
document.addEventListener("keydown", (event) => {
  if (!(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey || event.key.toLowerCase() !== "k") return;
  if (event.target.closest?.(".xterm")) return;
  event.preventDefault();
  paletteOpen();
});

function keysOpen() {
  const dialog = $("#keysDialog");
  if (dialog.open) { dialog.close(); return; }
  if (document.querySelector("dialog[open]")) return;
  openDialog(dialog);
}
$("#keysOpen")?.addEventListener("click", keysOpen);
$("#keysClose")?.addEventListener("click", () => $("#keysDialog").close());
$("#keysDialog")?.addEventListener("click", (event) => { if (event.target === $("#keysDialog")) $("#keysDialog").close(); });
document.addEventListener("keydown", (event) => {
  if (event.key !== "?" || event.ctrlKey || event.metaKey || event.altKey || event.defaultPrevented) return;
  if (event.target.closest?.("input, textarea, select, [contenteditable], .xterm")) return;
  const dialog = $("#keysDialog");
  if (document.querySelector("dialog[open]") && !dialog.open) return;
  event.preventDefault();
  keysOpen();
});
