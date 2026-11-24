"use strict";
// Runs before the stylesheet paints: applies the saved light/dark/auto mode so the page never flashes the wrong theme.
// Light is the default when nothing valid is stored.
(() => {
  let mode = "light";
  try {
    const saved = JSON.parse(localStorage.getItem("dcclite.theme"));
    if (saved === "auto" || saved === "light" || saved === "dark") mode = saved;
  } catch { }
  document.documentElement.dataset.theme = mode;
})();
