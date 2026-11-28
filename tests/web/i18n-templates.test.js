// Template translations ("{1} items moved") must keep the values that the app puts
// into the sentence. This runs the real src/web/js/i18n.js engine for every language.
const assert = require("assert");
const { loadData, engine } = require("./helpers/i18n-load");

const data = loadData();
const langs = Object.keys(data);

// 1. Property check: every template, filled with unique tokens, must come back with
//    each token that its own translation refers to, and without any "{n}" left over.
const TOKENS = ["§A§", "§B§", "§C§", "§D§"];
let checked = 0;
for (const lang of langs) {
  const i18n = engine(lang, data);
  for (const [key, value] of Object.entries(data[lang])) {
    if (!key.includes("{")) continue;
    let n = 0;
    const order = [];
    const sample = key.replace(/\{(\d)\}/g, (_, d) => { order.push(d); return TOKENS[n++]; });
    const out = i18n.t(sample);
    assert.ok(!/\{\d\}/.test(out), `${lang}: placeholder left in output for "${key}" -> "${out}"`);
    for (const d of new Set([...value.matchAll(/\{(\d)\}/g)].map((m) => m[1]))) {
      const token = TOKENS[order.indexOf(d)];
      assert.ok(out.includes(token), `${lang}: value {${d}} was dropped for "${key}" -> "${out}"`);
    }
    checked++;
  }
}

// 2. Real messages that the app shows after common actions.
const ar = engine("ar", data);
assert.ok(ar.t("3 items moved · Ctrl+Z to undo").includes("3"), "ar: the number of moved items must stay in the message");
assert.ok(ar.t("Could not undo: boom").includes("boom"), "ar: the reason an undo failed must stay in the message");
assert.ok(ar.t("Undone: Rename “a.txt”").includes("a.txt"), "ar: the file name must stay in the undo message");

// 3. An English message must never come out half-translated (a template that starts with
//    a placeholder can swallow the front of an unrelated sentence).
const LOST = "Cannot reach DCC Lite. Check that it is still running.";
for (const lang of langs) {
  const out = engine(lang, data).t(LOST);
  assert.ok(!/Cannot reach|Check that it/.test(out), `${lang}: "${LOST}" is untranslated or half-translated -> "${out}"`);
}

console.log(`i18n templates ok: ${langs.join(" ")} (${checked} template entries)`);
