// Shared helper for the i18n tests: loads every translation dictionary
// (the i18n-*.js files plus the per-page DCC_I18N.add(...) blocks) into one object.
const fs = require("fs");
const path = require("path");

const dir = path.join(__dirname, "../../../src/web/js");
const DICTIONARY_FILES = ["i18n-extra.js", "i18n-more.js", "i18n-fill.js", "i18n-additions.js"];

function loadData() {
  globalThis.window = {};
  globalThis.localStorage = { getItem: () => null, setItem() {} };
  globalThis.navigator = { language: "en" };
  globalThis.document = { readyState: "complete", addEventListener() {}, documentElement: {}, getElementById: () => null, createElement: () => ({}), querySelector: () => null };
  const run = (f) => (0, eval)(fs.readFileSync(path.join(dir, f), "utf8"));

  run("i18n-data.js");
  const data = window.DCC_I18N_DATA;
  globalThis.DCC_I18N = window.DCC_I18N = { add(code, dict) { data[code] = { ...(data[code] || {}), ...dict }; } };
  for (const f of DICTIONARY_FILES) if (fs.existsSync(path.join(dir, f))) run(f);

  for (const f of fs.readdirSync(dir)) {
    if (f.startsWith("i18n")) continue;
    const src = fs.readFileSync(path.join(dir, f), "utf8");
    const re = /add\??\.?\(\s*"([a-z]{2})"\s*,\s*\{/g;
    let m;
    while ((m = re.exec(src))) {
      let depth = 0, str = null, esc = false, j = re.lastIndex - 1;
      for (; j < src.length; j++) {
        const c = src[j];
        if (str) { if (esc) esc = false; else if (c === "\\") esc = true; else if (c === str) str = null; continue; }
        if (c === '"' || c === "'" || c === "`") { str = c; continue; }
        if (c === "{") depth++; else if (c === "}" && --depth === 0) break;
      }
      Object.assign(data[m[1]] ||= {}, eval("(" + src.slice(re.lastIndex - 1, j + 1) + ")"));
    }
  }
  return data;
}

// Boots the real i18n.js engine for one language and returns its public API
// (DCC_I18N.t is the same lookup the page uses). The DOM is stubbed; init never runs.
function engine(lang, data) {
  globalThis.window = { DCC_I18N_DATA: data, alert() {}, confirm() {}, prompt() {} };
  globalThis.localStorage = { getItem: () => lang, setItem() {} };
  globalThis.navigator = { language: lang };
  globalThis.document = { readyState: "loading", addEventListener() {}, documentElement: {}, getElementById: () => null };
  globalThis.MutationObserver = class { observe() {} };
  (0, eval)(fs.readFileSync(path.join(dir, "i18n.js"), "utf8"));
  return window.DCC_I18N;
}

module.exports = { loadData, engine, dir };
