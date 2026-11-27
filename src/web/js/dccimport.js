"use strict";
(function () {
  const MAX_NAME = 80;
  const MAX_COMMAND = 1000;

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
      const hidden = flag === "0";
      const mode = Math.min(9, Math.max(1, parseInt(flag, 10) || 1));
      const commands = [];
      for (let index = 1; section.values.has(`line${index}`); index++) {
        const command = section.values.get(`line${index}`).replace(/[\0\r\n]/g, "").trim();
        if (command) commands.push(command);
      }
      if (!commands.length) { result.skipped.push({ name: displayName, reason: "no commands" }); continue; }
      const alternatives = mode >= 2 && commands.length > 1;
      const command = alternatives ? commands[0] : commands.join(" ; ");
      if (commands.some((c) => c.length > MAX_COMMAND) || command.length > MAX_COMMAND) { result.skipped.push({ name: displayName, reason: "too long" }); continue; }
      const script = { name: displayName.slice(0, MAX_NAME), command, mode, hidden };
      if (commands.length > 1) script.lines = commands;
      result.scripts.push(script);
    }
    return result;
  }

  function serializeDccScripts(items) {
    const used = new Set(["userscripts"]);
    const names = [], sections = [];
    for (const item of items || []) {
      const clean = (v) => String(v ?? "").replace(/[\0\r\n]/g, " ").trim();
      const lines = Array.isArray(item?.lines) && item.lines.length ? item.lines.map(clean).filter(Boolean) : [clean(item?.command)].filter(Boolean);
      if (!lines.length || lines.some((l) => l.length > MAX_COMMAND)) continue;
      const base = String(item?.name ?? "Command")
        .replace(/[\[\]=\0\r\n]/g, "-")
        .trim().replace(/^[;#\s]+/, "").slice(0, MAX_NAME).trim() || "Command";
      let name = base, suffix = 2;
      while (used.has(name.toLowerCase())) name = `${base.slice(0, MAX_NAME - String(suffix).length - 3)} (${suffix++})`;
      used.add(name.toLowerCase());
      const mode = Math.min(9, Math.max(1, Number(item?.mode) || 1));
      names.push(`${name}=${item?.hidden ? 0 : mode}`);
      sections.push(`[${name}]\r\n${lines.map((l, i) => `line${i + 1}=${l}`).join("\r\n")}`);
    }
    return `[UserScripts]\r\n${names.join("\r\n")}\r\n\r\n${sections.join("\r\n\r\n")}\r\n`;
  }

  const api = { parseDccScripts, serializeDccScripts };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else globalThis.DccImport = api;
})();
