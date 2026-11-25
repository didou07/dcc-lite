"use strict";
(() => {
  let mode = "light";
  try {
    const saved = JSON.parse(localStorage.getItem("dcclite.theme"));
    if (saved === "auto" || saved === "light" || saved === "dark") mode = saved;
  } catch { }
  document.documentElement.dataset.theme = mode;
})();
