"use strict";
// Language support: translates UI text by exact English match, including text added later by the app.
(() => {
  const KEY = "dcclite.lang";
  const NAMES = { en: "English", ar: "العربية", fr: "Français", es: "Español", de: "Deutsch", tr: "Türkçe", ru: "Русский", zh: "中文" };
  const RTL = new Set(["ar", "he", "fa", "ur"]);
  const data = window.DCC_I18N_DATA || {};
  const ATTRS = ["title", "placeholder", "aria-label"];
  const SKIP = "script,style,textarea,pre,code,.xterm,[data-no-i18n]";
  const seen = new WeakMap(); // node or "el|attr" -> { src, out }
  let lang = "en";
  try {
    const saved = localStorage.getItem(KEY) || (navigator.language || "en").slice(0, 2).toLowerCase();
    if (saved === "en" || data[saved]) lang = saved;
  } catch { }

  const tpl = {}; // per language: [regex, translation] built from keys containing {1}, {2}...
  function templates(code) {
    return tpl[code] ||= Object.entries(data[code] || {}).filter(([k]) => k.includes("{")).map(([k, v]) => {
      let n = 0; // {n} placeholders are numbered by position: first {..} is {1}, second {2}...
      const esc = k.replace(/[.*+?^$()|[\]\\]/g, "\\$&").replace(/\{\d\}/g, () => `(?<g${++n}>[\\s\\S]+?)`);
      return [new RegExp("^" + esc + "$"), v];
    });
  }
  function lookup(text) {
    const m = /^(\s*)([\s\S]*?)(\s*)$/.exec(text);
    if (lang === "en" || !m[2]) return text;
    let hit = data[lang]?.[m[2]];
    if (!hit) for (const [re, v] of templates(lang)) { const x = re.exec(m[2]); if (x) { hit = v.replace(/\{(\d)\}/g, (_, n) => x.groups["g" + n] ?? ""); break; } }
    return hit ? m[1] + hit + m[3] : text;
  }
  function text(node) {
    if (node.parentElement?.closest(SKIP)) return;
    const rec = seen.get(node), src = rec && node.data === rec.out ? rec.src : node.data, out = lookup(src);
    seen.set(node, { src, out });
    if (node.data !== out) node.data = out;
  }
  const attrKeys = new WeakMap();
  function attr(el, name) {
    if (!el.hasAttribute(name) || el.closest(SKIP)) return;
    let map = attrKeys.get(el); if (!map) attrKeys.set(el, map = {});
    const cur = el.getAttribute(name), rec = map[name], src = rec && cur === rec.out ? rec.src : cur, out = lookup(src);
    map[name] = { src, out };
    if (cur !== out) el.setAttribute(name, out);
  }
  function walk(root) {
    if (root.nodeType === 3) return text(root);
    if (root.nodeType !== 1) return;
    const tw = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (let n = root; n; n = tw.nextNode()) {
      if (n.nodeType === 3) text(n); else for (const a of ATTRS) attr(n, a);
    }
  }
  const observer = new MutationObserver((records) => {
    for (const r of records) {
      if (r.type === "characterData") text(r.target);
      else if (r.type === "attributes") attr(r.target, r.attributeName);
      else r.addedNodes.forEach(walk);
    }
  });
  function start() {
    walk(document.body);
    document.title = lookup(document.title);
    observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ATTRS });
  }
  function set(code) {
    if (code !== "en" && !data[code]) return;
    lang = code;
    try { localStorage.setItem(KEY, code); } catch { }
    document.documentElement.lang = code;
    document.documentElement.dir = RTL.has(code) ? "rtl" : "ltr";
    walk(document.body);
  }
  function picker() {
    const sel = document.createElement("select");
    sel.className = "i18n-sel"; sel.setAttribute("data-no-i18n", ""); sel.setAttribute("aria-label", "Language"); sel.title = "Language";
    for (const code of ["en", ...Object.keys(data)]) sel.append(new Option(NAMES[code] || code, code));
    sel.value = lang;
    sel.addEventListener("change", () => set(sel.value));
    const anchor = document.getElementById("themeOpen");
    if (anchor) anchor.before(sel); else (document.querySelector(".login-card") || document.body).append(sel);
  }
  for (const fn of ["alert", "confirm", "prompt"]) { // native dialogs
    const orig = window[fn].bind(window);
    window[fn] = (m, ...rest) => orig(typeof m === "string" ? lookup(m) : m, ...rest);
  }
  window.DCC_I18N = {
    get lang() { return lang; }, set,
    t: lookup,
    locale: () => ({ ar: "ar", fr: "fr", es: "es", de: "de", tr: "tr", ru: "ru", zh: "zh-CN" })[lang] || (window.DCC_I18N?.locale() || "en-GB"),
    add(code, dict) { data[code] = { ...(data[code] || {}), ...dict }; delete tpl[code]; if (code === lang) { walk(document.body); document.title = lookup(document.title); } },
  };
  document.documentElement.lang = lang;
  document.documentElement.dir = RTL.has(lang) ? "rtl" : "ltr";
  const init = () => { picker(); start(); };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
})();
