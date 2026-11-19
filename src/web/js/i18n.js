"use strict";
// UI translation by exact English match (also covers text added later).
(() => {
  const KEY = "dcclite.lang";
  const NAMES = { en: "English", ar: "العربية", fr: "Français", es: "Español", de: "Deutsch", tr: "Türkçe", ru: "Русский", zh: "中文" };
  const RTL = new Set(["ar", "he", "fa", "ur"]);
  const MIRROR_LAYOUT = false;
  const dirFor = (code) => (MIRROR_LAYOUT && RTL.has(code) ? "rtl" : "ltr");
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
    document.documentElement.dir = dirFor(code);
    walk(document.body);
  }
  const FLAGS = {"en": "<rect width=\"30\" height=\"20\" fill=\"#012169\"/><path d=\"M0 0L30 20M30 0L0 20\" stroke=\"#fff\" stroke-width=\"4\"/><path d=\"M0 0L30 20M30 0L0 20\" stroke=\"#C8102E\" stroke-width=\"1.6\"/><path d=\"M15 0V20M0 10H30\" stroke=\"#fff\" stroke-width=\"6\"/><path d=\"M15 0V20M0 10H30\" stroke=\"#C8102E\" stroke-width=\"3.4\"/>", "fr": "<rect width=\"10\" height=\"20\" fill=\"#0055A4\"/><rect x=\"10\" width=\"10\" height=\"20\" fill=\"#fff\"/><rect x=\"20\" width=\"10\" height=\"20\" fill=\"#EF4135\"/>", "es": "<rect width=\"30\" height=\"20\" fill=\"#AA151B\"/><rect y=\"5\" width=\"30\" height=\"10\" fill=\"#F1BF00\"/>", "de": "<rect width=\"30\" height=\"7\" fill=\"#000\"/><rect y=\"7\" width=\"30\" height=\"6.5\" fill=\"#DD0000\"/><rect y=\"13.4\" width=\"30\" height=\"6.6\" fill=\"#FFCE00\"/>", "ru": "<rect width=\"30\" height=\"20\" fill=\"#fff\"/><rect y=\"6.7\" width=\"30\" height=\"6.7\" fill=\"#0039A6\"/><rect y=\"13.4\" width=\"30\" height=\"6.6\" fill=\"#D52B1E\"/>", "tr": "<rect width=\"30\" height=\"20\" fill=\"#E30A17\"/><path fill=\"#fff\" d=\"M14.40 6.33A5 5 0 1 0 14.40 13.67A4 4 0 1 1 14.40 6.33Z\"/><polygon fill=\"#fff\" points=\"16.40,7.50 16.96,9.23 18.78,9.23 17.31,10.30 17.87,12.02 16.40,10.96 14.93,12.02 15.49,10.30 14.02,9.23 15.84,9.23\"/>", "zh": "<rect width=\"30\" height=\"20\" fill=\"#DE2910\"/><polygon fill=\"#FFDE00\" points=\"5.00,2.00 5.67,4.07 7.85,4.07 6.09,5.35 6.76,7.43 5.00,6.15 3.24,7.43 3.91,5.35 2.15,4.07 4.33,4.07\"/><polygon fill=\"#FFDE00\" points=\"10.28,1.04 10.30,1.76 11.00,1.97 10.32,2.21 10.34,2.94 9.89,2.37 9.21,2.62 9.62,2.01 9.17,1.44 9.87,1.64\"/><polygon fill=\"#FFDE00\" points=\"12.64,3.63 12.37,4.31 12.93,4.77 12.20,4.72 11.93,5.40 11.75,4.69 11.03,4.64 11.65,4.26 11.47,3.55 12.03,4.02\"/><polygon fill=\"#FFDE00\" points=\"12.94,7.06 12.37,7.51 12.62,8.19 12.01,7.78 11.44,8.23 11.64,7.53 11.04,7.12 11.76,7.10 11.97,6.40 12.21,7.08\"/><polygon fill=\"#FFDE00\" points=\"11.00,9.53 10.32,9.80 10.37,10.53 9.91,9.97 9.23,10.24 9.62,9.63 9.15,9.07 9.86,9.25 10.24,8.63 10.29,9.35\"/>", "ar": "<rect width=\"15\" height=\"20\" fill=\"#006233\"/><rect x=\"15\" width=\"15\" height=\"20\" fill=\"#fff\"/><path fill=\"#D21034\" d=\"M18.42 6.09A5.2 5.2 0 1 0 18.42 13.91A4.2 4.2 0 1 1 18.42 6.09Z\"/><polygon fill=\"#D21034\" points=\"17.70,7.53 17.70,9.42 19.50,10.00 17.70,10.58 17.70,12.47 16.59,10.94 14.80,11.53 15.91,10.00 14.80,8.47 16.59,9.06\"/>"};
  const flagSvg = (code) => `<svg class="flag" viewBox="0 0 30 20" aria-hidden="true">${FLAGS[code] || '<rect width="30" height="20" fill="#8794a3"/>'}</svg>`;
  function picker() {
    const codes = ["en", ...Object.keys(data)];
    const anchor = document.getElementById("themeOpen");
    const make = (code, cls) => {
      const b = document.createElement("button");
      b.type = "button"; b.className = `flag-btn ${cls || ""}`.trim(); b.dataset.lang = code;
      b.setAttribute("data-no-i18n", ""); b.title = NAMES[code] || code; b.setAttribute("aria-label", NAMES[code] || code);
      b.innerHTML = flagSvg(code);
      return b;
    };
    const mark = (root) => root.querySelectorAll(".flag-btn[data-lang]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.lang === lang)));
    if (!anchor) { // sign-in page: one flag per language, side by side
      const row = document.createElement("div");
      row.className = "lang-row"; row.setAttribute("role", "group"); row.setAttribute("aria-label", "Language");
      for (const code of codes) row.append(make(code));
      row.addEventListener("click", (event) => { const b = event.target.closest(".flag-btn"); if (b) { set(b.dataset.lang); mark(row); } });
      mark(row);
      (document.querySelector(".login-card") || document.body).append(row);
      return;
    }
    // Side bar: the current flag; click it to pick another flag from a small grid.
    const button = make(lang, "lang-btn");
    button.setAttribute("aria-haspopup", "true");
    const pop = document.createElement("div");
    pop.className = "lang-pop"; pop.hidden = true; pop.setAttribute("role", "group"); pop.setAttribute("aria-label", "Language");
    for (const code of codes) pop.append(make(code));
    document.body.append(pop);
    const close = () => { pop.hidden = true; };
    const open = () => {
      mark(pop); pop.hidden = false;
      const r = button.getBoundingClientRect(), w = pop.offsetWidth, h = pop.offsetHeight;
      let x = r.right + 8; if (x + w > window.innerWidth - 6) x = window.innerWidth - w - 6;
      let y = r.bottom - h; if (y < 6) y = r.bottom + 6;
      pop.style.left = `${Math.max(6, x)}px`; pop.style.top = `${Math.max(6, Math.min(y, window.innerHeight - h - 6))}px`;
    };
    button.addEventListener("click", () => (pop.hidden ? open() : close()));
    pop.addEventListener("click", (event) => {
      const b = event.target.closest(".flag-btn"); if (!b) return;
      set(b.dataset.lang);
      button.dataset.lang = b.dataset.lang; button.title = NAMES[b.dataset.lang] || b.dataset.lang; button.setAttribute("aria-label", button.title);
      button.innerHTML = flagSvg(b.dataset.lang);
      close();
    });
    document.addEventListener("pointerdown", (event) => { if (!pop.hidden && !event.target.closest(".lang-pop, .lang-btn")) close(); });
    document.addEventListener("keydown", (event) => { if (event.key === "Escape") close(); });
    window.addEventListener("resize", close);
    anchor.before(button);
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
  document.documentElement.dir = dirFor(lang);
  const init = () => { picker(); start(); };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
})();
