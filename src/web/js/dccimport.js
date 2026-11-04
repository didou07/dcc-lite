"use strict";
/* Reads the user scripts of the original Dreambox Control Center (dccscript.ini). */
(function () {
  const MAX_NAME = 80;
  const MAX_COMMAND = 1000;

  /*
   * dccscript.ini lists the scripts to show in [UserScripts] (one "Name=1" line
   * each). Every script has its own [Name] section with Line1..LineN, run one
   * after the other. The built-in mount/format sections that ship in the file use
   * device paths of very old receivers (/dev/ide/..., /dev/scsi/...), so they are
   * deliberately not imported: guessing a device name for mkfs is dangerous.
   */
  function parseDccScripts(text) {
    const sections = new Map();
    let current = null;
    for (const raw of String(text).split(/\r\n|\n|\r/)) {
      const line = raw.trim();
      if (!line || line.startsWith(";") || line.startsWith("#")) continue;
      const header = /^\[(.+)\]$/.exec(line);
      if (header) { current = { name: header[1].trim(), values: new Map() }; sections.set(current.name.toLowerCase(), current); continue; }
      if (!current) continue;
      const eq = line.indexOf("=");
      if (eq < 0) continue;
      current.values.set(line.slice(0, eq).trim().toLowerCase(), line.slice(eq + 1).trim());
    }
    const listed = sections.get("userscripts");
    const result = { scripts: [], skipped: [] };
    if (!listed) return result;
    for (const [key, flag] of listed.values) {
      const section = sections.get(key);
      const displayName = section ? section.name : key;
      if (!section) { result.skipped.push({ name: displayName, reason: "no script section" }); continue; }
      if (flag === "0") { result.skipped.push({ name: displayName, reason: "disabled" }); continue; }
      const commands = [];
      for (let index = 1; section.values.has(`line${index}`); index++) {
        const command = section.values.get(`line${index}`).replace(/[\0\r\n]/g, "").trim();
        if (command) commands.push(command);
      }
      if (!commands.length) { result.skipped.push({ name: displayName, reason: "no commands" }); continue; }
      const command = commands.join(" ; ");
      if (command.length > MAX_COMMAND) { result.skipped.push({ name: displayName, reason: "too long" }); continue; }
      result.scripts.push({ name: displayName.slice(0, MAX_NAME), command, lines: commands.length });
    }
    return result;
  }

  const api = { parseDccScripts };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else globalThis.DccImport = api;
})();
