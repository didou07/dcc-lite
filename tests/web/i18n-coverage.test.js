const assert = require("assert");
const { loadData } = require("./helpers/i18n-load");

const data = loadData();

const slots = (s) => (s.match(/\{\d\}/g) || []).sort().join();
// Every language must have every string that any language has. (The old check only compared
// against Arabic, so a string translated in six languages but not in Arabic went unnoticed.)
const keys = [...new Set(Object.values(data).flatMap((dict) => Object.keys(dict)))];
for (const code of Object.keys(data)) {
  const missing = keys.filter((k) => !(k in data[code]));
  assert.strictEqual(missing.length, 0, `${code} lacks ${missing.length} strings, e.g. ${missing[0]}`);
  for (const [k, v] of Object.entries(data[code])) {
    assert.ok(v && v.trim(), `${code}: empty translation for "${k}"`);
    assert.strictEqual(slots(v), slots(k), `${code}: placeholders differ in "${k}"`);
  }
}
console.log("i18n coverage ok:", Object.keys(data).join(" "), keys.length, "strings");
