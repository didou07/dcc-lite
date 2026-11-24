const fs = require("fs"), assert = require("assert");
const root = require("path").join(__dirname, "../src/web");
const js = fs.readFileSync(root + "/js/devices.js", "utf8");
const html = fs.readFileSync(root + "/index.html", "utf8");
const a = js.indexOf("function cleanHost"), b = js.indexOf("function openDeviceDialog");
const cleanHost = new Function(js.slice(a, b) + "; return cleanHost;")();
assert.deepStrictEqual(cleanHost("http://192.168.1.20:8080/"), { host: "192.168.1.20", port: 8080 });
assert.deepStrictEqual(cleanHost("  box.local "), { host: "box.local", port: 0 });
assert.deepStrictEqual(cleanHost("https://box.local/web?x=1"), { host: "box.local", port: 0 });
for (const id of ["devGroup", "devColor", "devNote", "devStartDir", "sitesSummary"]) assert(html.includes(`id="${id}"`), id);
assert(html.includes('id="i-eye-off"'));
// every icon the page asks for exists
const defs = new Set([...html.matchAll(/<symbol id="i-([\w-]+)"/g)].map((m) => m[1]));
for (const m of js.matchAll(/icon(?:Button)?\("([\w-]+)"/g)) assert(defs.has(m[1]), "missing icon " + m[1]);
for (const m of js.matchAll(/icon: "([\w-]+)"/g)) assert(defs.has(m[1]), "missing icon " + m[1]);
console.log("devices page: all checks passed");
