const fs = require("fs");
const path = require("path");
const assert = require("assert");

const dir = path.join(__dirname, "../src/web/js");
globalThis.window = {};
globalThis.localStorage = { getItem: () => null, setItem() {} };
globalThis.navigator = { language: "en" };
globalThis.document = { readyState: "complete", addEventListener() {}, documentElement: {}, getElementById: () => null, createElement: () => ({}), querySelector: () => null };
const run = (f) => (0, eval)(fs.readFileSync(path.join(dir, f), "utf8"));

run("i18n-data.js");
const data = window.DCC_I18N_DATA;
globalThis.DCC_I18N = window.DCC_I18N = { add(code, dict) { data[code] = { ...(data[code] || {}), ...dict }; } };
for (const f of ["i18n-extra.js", "i18n-more.js", "i18n-fill.js"]) run(f);

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

const slots = (s) => (s.match(/\{\d\}/g) || []).sort().join();
const keys = Object.keys(data.ar);
for (const code of Object.keys(data)) {
  const missing = keys.filter((k) => !(k in data[code]));
  assert.strictEqual(missing.length, 0, `${code} lacks ${missing.length} strings, e.g. ${missing[0]}`);
  for (const [k, v] of Object.entries(data[code])) {
    assert.ok(v && v.trim(), `${code}: empty translation for "${k}"`);
    assert.strictEqual(slots(v), slots(k), `${code}: placeholders differ in "${k}"`);
  }
}
console.log("i18n coverage ok:", Object.keys(data).join(" "), keys.length, "strings");
