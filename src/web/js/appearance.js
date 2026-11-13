"use strict";
(() => {
  const KEY = "dcclite.appearance";
  const root = document.documentElement;
  const DEFAULTS = { accent: "green", accentHex: "", folder: "gold", folderHex: "", tone: "neutral", btn: "outline", edge: "default", radius: "sharp", density: "compact", icons: "thin", font: "system", motion: "reduced", v: 3 };
  const ACCENTS = { blue: [217, 88], violet: [262, 72], rose: [336, 78], green: [146, 62], slate: [215, 20], red: [0, 72] };
  const FOLDERS = { teal: [187, 72], violet: [262, 72], green: [146, 62], slate: [215, 20], gold: [48, 78] };
  const ATTRS = ["tone", "btn", "edge", "radius", "density", "icons", "font", "motion"];

  let state = { ...DEFAULTS };
  try {
    const saved = JSON.parse(localStorage.getItem(KEY) || "{}");
    if (saved.v === DEFAULTS.v) state = { ...DEFAULTS, ...saved };
    else { // new default look: dark mode too (one time)
      root.dataset.theme = "dark";
      localStorage.setItem("dcclite.theme", JSON.stringify("dark"));
      localStorage.setItem(KEY, JSON.stringify(state));
    }
  } catch { }
  if (state.radius === "default") state.radius = "sharp"; // sharp corners are the default look

  function hexToHS(hex) {
    const m = /^#?([0-9a-f]{6})$/i.exec(hex || ""); if (!m) return null;
    const n = parseInt(m[1], 16), r = (n >> 16) / 255, g = ((n >> 8) & 255) / 255, b = (n & 255) / 255;
    const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min, l = (max + min) / 2;
    let h = 0;
    if (d) { h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4; h = (h * 60 + 360) % 360; }
    const s = d ? d / (1 - Math.abs(2 * l - 1)) : 0;
    return [Math.round(h), Math.round(Math.min(95, Math.max(12, s * 100)))];
  }
  function save() { try { localStorage.setItem(KEY, JSON.stringify(state)); } catch { } }

  function apply() {
    for (const a of ATTRS) { if (state[a] && state[a] !== "default" && !(a === "btn" && state.btn === "outline")) root.dataset[a] = state[a]; else delete root.dataset[a]; }
    if (!root.dataset.radius) root.dataset.radius = "sharp";
    // accent
    let hs = state.accentHex ? hexToHS(state.accentHex) : ACCENTS[state.accent];
    if (hs) { root.dataset.accent = "custom"; root.style.setProperty("--acc-h", String(hs[0])); root.style.setProperty("--acc-s", `${hs[1]}%`); }
    else { delete root.dataset.accent; root.style.removeProperty("--acc-h"); root.style.removeProperty("--acc-s"); }
    // folders
    if (state.folder === "accent" && !state.folderHex) { root.dataset.folder = "accent"; }
    else {
      hs = state.folderHex ? hexToHS(state.folderHex) : FOLDERS[state.folder];
      if (hs) { root.dataset.folder = "custom"; root.style.setProperty("--fld-h", String(hs[0])); root.style.setProperty("--fld-s", `${hs[1]}%`); }
      else { delete root.dataset.folder; root.style.removeProperty("--fld-h"); root.style.removeProperty("--fld-s"); }
    }
    sync();
  }

  const dialog = document.getElementById("themeDialog");
  function sync() {
    if (!dialog) return;
    let mode = "dark"; try { mode = JSON.parse(localStorage.getItem("dcclite.theme") || '"dark"'); } catch { }
    if (!["auto", "light", "dark"].includes(mode)) mode = "auto";
    const values = { ...state, mode };
    for (const seg of dialog.querySelectorAll(".ap-seg")) {
      const cur = values[seg.dataset.ap];
      for (const b of seg.querySelectorAll("button")) b.setAttribute("aria-checked", String(b.dataset.v === cur));
    }
    for (const group of dialog.querySelectorAll(".ap-sws")) {
      const key = group.dataset.ap, hex = state[`${key}Hex`];
      for (const b of group.querySelectorAll("button.ap-sw")) b.setAttribute("aria-pressed", String(!hex && b.dataset.v === state[key]));
      group.querySelector(".ap-custom").classList.toggle("on", !!hex);
    }
    if (state.accentHex) dialog.querySelector("#apAccentCustom").value = state.accentHex;
    if (state.folderHex) dialog.querySelector("#apFolderCustom").value = state.folderHex;
  }

  apply();
  if (!dialog) return;

  document.getElementById("themeOpen")?.addEventListener("click", () => { sync(); dialog.showModal(); });
  for (const id of ["themeClose", "apDone"]) document.getElementById(id)?.addEventListener("click", () => dialog.close());
  dialog.addEventListener("click", (event) => { if (event.target === dialog) dialog.close(); });
  document.getElementById("themeToggle")?.addEventListener("click", () => setTimeout(sync, 0));

  dialog.addEventListener("click", (event) => {
    const seg = event.target.closest(".ap-seg button");
    if (seg) {
      const key = seg.closest(".ap-seg").dataset.ap;
      if (key === "mode") {
        root.dataset.theme = seg.dataset.v;
        try { localStorage.setItem("dcclite.theme", JSON.stringify(seg.dataset.v)); } catch { }
        sync(); return;
      }
      state[key] = seg.dataset.v; save(); apply(); return;
    }
    const sw = event.target.closest("button.ap-sw");
    if (sw) {
      const key = sw.closest(".ap-sws").dataset.ap;
      state[key] = sw.dataset.v; state[`${key}Hex`] = ""; save(); apply();
    }
  });
  for (const [id, key] of [["apAccentCustom", "accent"], ["apFolderCustom", "folder"]]) {
    document.getElementById(id).addEventListener("input", (event) => { state[`${key}Hex`] = event.target.value; save(); apply(); });
  }
  document.getElementById("apReset")?.addEventListener("click", () => { state = { ...DEFAULTS }; save(); apply(); });
})();
