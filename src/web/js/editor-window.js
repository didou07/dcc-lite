"use strict";
(() => {
  const dialog = document.getElementById("editorDialog");
  const head = document.getElementById("editorHead");
  const maxBtn = document.getElementById("editorMax");
  if (!dialog || !head || !maxBtn) return;

  const KEY = "editorWindow";
  const MIN_W = 460, MIN_H = 300, GAP = 8;
  const DEFAULT_W = 1000, DEFAULT_H = 820;
  const state = { x: null, y: null, w: 0, h: 0, max: false };
  const saved = (typeof store !== "undefined" && store.get(KEY, null)) || null;
  if (saved && typeof saved === "object") {
    for (const k of ["x", "y", "w", "h"]) if (Number.isFinite(saved[k])) state[k] = saved[k];
    state.max = saved.max === true;
  }
  const persist = () => { if (typeof store !== "undefined") store.set(KEY, { ...state }); };
  const clamp = (v, lo, hi) => Math.min(Math.max(v, lo), Math.max(lo, hi));
  const vw = () => document.documentElement.clientWidth || window.innerWidth;
  const vh = () => window.innerHeight;
  const small = () => window.matchMedia("(max-width: 640px)").matches;
  const ui = () => parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--ui")) || 1;

  function setBox(x, y, w, h) {
    const s = dialog.style;
    s.margin = "0"; s.inset = "auto"; s.maxWidth = "none"; s.maxHeight = "none";
    s.left = `${Math.round(x)}px`; s.top = `${Math.round(y)}px`;
    s.width = `${Math.round(w)}px`; s.height = `${Math.round(h)}px`;
  }

  function setMaxIcon() {
    const label = state.max ? "Restore size" : "Maximize";
    maxBtn.title = label;
    maxBtn.setAttribute("aria-label", label);
    maxBtn.setAttribute("aria-pressed", String(state.max));
    const use = maxBtn.querySelector("use");
    if (use) use.setAttribute("href", state.max ? "#i-collapse" : "#i-expand");
    dialog.dataset.maximized = state.max || small() ? "1" : "0";
  }

  function apply() {
    const W = vw(), H = vh(), k = ui();
    if (small()) { setBox(0, 0, W, H); setMaxIcon(); return; }
    if (state.max) { setBox(GAP, GAP, W - GAP * 2, H - GAP * 2); setMaxIcon(); return; }
    const w = clamp(state.w || DEFAULT_W * k, MIN_W * k, W - GAP * 2);
    const h = clamp(state.h || DEFAULT_H * k, MIN_H * k, H - GAP * 2);
    const x = clamp(state.x ?? (W - w) / 2, 0, W - w);
    const y = clamp(state.y ?? (H - h) / 2, 0, H - h);
    setBox(x, y, w, h);
    setMaxIcon();
  }

  function toggleMax(force) {
    state.max = typeof force === "boolean" ? force : !state.max;
    apply(); persist();
  }
  function resetSize() { state.w = state.h = 0; state.x = state.y = null; state.max = false; apply(); persist(); }

  maxBtn.addEventListener("click", () => toggleMax());
  head.addEventListener("dblclick", (event) => { if (!event.target.closest("button, input, select, a")) toggleMax(); });

  head.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || state.max || small() || event.target.closest("button, input, select, a")) return;
    const rect = dialog.getBoundingClientRect();
    const dx = event.clientX - rect.left, dy = event.clientY - rect.top;
    head.setPointerCapture(event.pointerId);
    dialog.classList.add("ew-dragging");
    const move = (e) => {
      state.x = clamp(e.clientX - dx, 0, vw() - rect.width);
      state.y = clamp(e.clientY - dy, 0, vh() - 46);
      state.w = rect.width; state.h = rect.height;
      setBox(state.x, state.y, rect.width, rect.height);
    };
    const up = () => {
      head.removeEventListener("pointermove", move);
      head.removeEventListener("pointerup", up);
      head.removeEventListener("pointercancel", up);
      dialog.classList.remove("ew-dragging");
      persist();
    };
    head.addEventListener("pointermove", move);
    head.addEventListener("pointerup", up);
    head.addEventListener("pointercancel", up);
  });

  for (const dir of ["n", "s", "e", "w", "ne", "nw", "se", "sw"]) {
    const grip = document.createElement("div");
    grip.className = `editor-rs rs-${dir}`;
    grip.setAttribute("aria-hidden", "true");
    if (dir === "se") grip.title = "Double-click to reset the size";
    grip.addEventListener("dblclick", resetSize);
    grip.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || state.max || small()) return;
      event.preventDefault();
      const r = dialog.getBoundingClientRect(), k = ui();
      const start = { x: event.clientX, y: event.clientY, left: r.left, top: r.top, w: r.width, h: r.height };
      const minW = MIN_W * k, minH = MIN_H * k;
      grip.setPointerCapture(event.pointerId);
      dialog.classList.add("ew-dragging");
      const move = (e) => {
        let { left, top, w, h } = start;
        const dx = e.clientX - start.x, dy = e.clientY - start.y;
        if (dir.includes("e")) w = clamp(start.w + dx, minW, vw() - start.left);
        if (dir.includes("s")) h = clamp(start.h + dy, minH, vh() - start.top);
        if (dir.includes("w")) { const nw = clamp(start.w - dx, minW, start.left + start.w); left = start.left + start.w - nw; w = nw; }
        if (dir.includes("n")) { const nh = clamp(start.h - dy, minH, start.top + start.h); top = start.top + start.h - nh; h = nh; }
        Object.assign(state, { x: left, y: top, w, h });
        setBox(left, top, w, h);
      };
      const up = () => {
        grip.removeEventListener("pointermove", move);
        grip.removeEventListener("pointerup", up);
        grip.removeEventListener("pointercancel", up);
        dialog.classList.remove("ew-dragging");
        persist();
      };
      grip.addEventListener("pointermove", move);
      grip.addEventListener("pointerup", up);
      grip.addEventListener("pointercancel", up);
    });
    dialog.append(grip);
  }

  new MutationObserver(() => { if (dialog.open) apply(); }).observe(dialog, { attributes: true, attributeFilter: ["open"] });
  window.addEventListener("resize", () => { if (dialog.open) apply(); });
  // F11 inside the editor: maximise / restore.
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "F11") { event.preventDefault(); toggleMax(); }
  });
  setMaxIcon();
})();
