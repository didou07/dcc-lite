"use strict";
// Run with: node test/dccimport.test.js
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { parseDccScripts } = require("../src/web/js/dccimport.js");

const sample = [
  "[UserScripts]", "Samba starten=1", "USB Stick free size=1", "Off=0", "Missing=1", "Empty=1",
  "[MOUNT CF]", "Lines=2", "Line1=mkdir /var/mnt/cf", "Line2=mount /dev/ide/host0/bus0/target0/lun0/part1 /var/mnt/cf",
  "[Samba starten]", "Line1=smbd",
  "[USB Stick free size]", "Line1=df -k", "Line2=  ", "Line3=echo done",
  "[Off]", "Line1=reboot", "[Empty]", "Lines=0",
].join("\r\n");
const r = parseDccScripts(sample);
assert.deepEqual(r.scripts.map((s) => [s.name, s.command]), [["Samba starten", "smbd"], ["USB Stick free size", "df -k ; echo done"]]);
assert.deepEqual(r.skipped.map((s) => s.reason).sort(), ["disabled", "no commands", "no script section"]);
assert.ok(!r.scripts.some((s) => /mkfs|mount/.test(s.command)), "standard device scripts must not be imported");
assert.deepEqual(parseDccScripts("garbage").scripts, []);
assert.deepEqual(parseDccScripts("").scripts, []);
assert.equal(parseDccScripts("[UserScripts]\nX=1\n[X]\nLine1=" + "a".repeat(2000)).skipped[0].reason, "too long");

// The real file shipped with the original program, when it is next to the repository.
const real = process.argv[2];
if (real && fs.existsSync(real)) {
  const out = parseDccScripts(fs.readFileSync(real, "latin1"));
  assert.deepEqual(out.scripts.map((s) => s.name), ["Samba starten", "USB Stick free size", "Speed up lan"]);
  console.log("real dccscript.ini:", out.scripts.map((s) => `${s.name} => ${s.command}`).join(" | "));
}
console.log("dccimport tests passed");
