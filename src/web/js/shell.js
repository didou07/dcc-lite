"use strict";
(function shell() {
  const TITLES = {
    devices: "Devices", files: "File commander", terminal: "Terminal", screen: "Live screen",
    lists: "Channel editor", ipk: "Install plugin", signal: "Signal meter", logs: "Logs",
  };
  const title = document.getElementById("viewTitle");
  const dot = document.getElementById("deviceDot");
  const select = document.getElementById("deviceSelect");
  const sbDot = document.getElementById("sbDot");
  const sbDevice = document.getElementById("sbDevice");
  const sbMessage = document.getElementById("sbMessage");
  const sbTransfers = document.getElementById("sbTransfers");
  const sbTransfersText = document.getElementById("sbTransfersText");
  const summary = document.getElementById("transferSummary");

  function syncView() {
    const name = document.body.dataset.view || "devices";
    if (title) title.textContent = TITLES[name] || "DCC Lite";
  }

  function syncDevice() {
    if (!dot || !sbDot) return;
    sbDot.dataset.state = dot.dataset.state || "unknown";
    const option = select && select.selectedOptions && select.selectedOptions[0];
    const value = select ? select.value : "";
    if (sbDevice) sbDevice.textContent = value && option ? option.textContent.trim() : "No device";
    if (sbMessage) sbMessage.textContent = value ? (dot.title || "") : "Add a device to get started";
  }

  function syncTransfers() {
    if (!summary || !sbTransfersText) return;
    const text = summary.textContent.trim();
    sbTransfersText.textContent = text || "No transfers";
    sbTransfers.dataset.busy = /\b[1-9]\d* active\b/.test(text) ? "true" : "false";
  }


  // Small screens: utility buttons move into the top bar.
  const rail = document.querySelector(".rail");
  const railFoot = document.querySelector(".rail-foot");
  const topbar = document.querySelector(".topbar");
  const compactQuery = window.matchMedia("(max-width: 900px), (max-height: 500px)");
  function placeRailFoot() {
    if (!rail || !railFoot || !topbar) return;
    if (compactQuery.matches) { if (railFoot.parentElement !== topbar) topbar.append(railFoot); }
    else if (railFoot.parentElement !== rail) rail.append(railFoot);
  }
  const narrowQuery = window.matchMedia("(max-width: 599px)");
  let langRow = null;
  function placeLanguage() {
    const select = document.querySelector(".lang-btn");
    const body = document.querySelector("#themeDialog .ap-body");
    if (!select || !body) return;
    if (narrowQuery.matches) {
      if (!langRow) {
        langRow = document.createElement("div");
        langRow.className = "ap-row";
        const label = document.createElement("span");
        label.className = "ap-l";
        label.textContent = "Language";
        langRow.append(label);
      }
      langRow.append(select);
      if (langRow.parentElement !== body) body.prepend(langRow);
    } else if (select.parentElement === langRow) {
      const anchor = document.getElementById("themeOpen");
      if (anchor) anchor.before(select);
    }
  }
  placeRailFoot();
  document.addEventListener("DOMContentLoaded", placeLanguage);
  if (document.readyState !== "loading") placeLanguage();
  if (narrowQuery.addEventListener) narrowQuery.addEventListener("change", placeLanguage); else narrowQuery.addListener(placeLanguage);
  for (const button of document.querySelectorAll(".view-actions .btn, .logs-tools .btn, .screen-tools > .btn")) {
    const text = button.textContent.trim();
    if (text && !button.title) button.title = text;
    if (text && !button.getAttribute("aria-label")) button.setAttribute("aria-label", text);
  }
  if (compactQuery.addEventListener) compactQuery.addEventListener("change", placeRailFoot); else compactQuery.addListener(placeRailFoot);
  for (const tab of document.querySelectorAll("#mainTabs .tab")) {
    const label = tab.querySelector("span");
    if (label && !tab.title) tab.title = label.textContent.trim();
  }

  const watch = (node, options, fn) => { if (node) new MutationObserver(fn).observe(node, options); };
  watch(document.body, { attributes: true, attributeFilter: ["data-view"] }, syncView);
  watch(dot, { attributes: true, attributeFilter: ["data-state", "title"] }, syncDevice);
  watch(select, { childList: true, subtree: true, attributes: true }, syncDevice);
  watch(summary, { childList: true, characterData: true, subtree: true }, syncTransfers);
  if (select) select.addEventListener("change", syncDevice);
  if (sbTransfers) sbTransfers.addEventListener("click", () => { if (typeof showView === "function") showView("files"); });

  const DIALOG_ICONS = {
    deviceDialog: "tv", termCfgDialog: "sliders", pasteDialog: "paste", raDialog: "lock",
    scanDialog: "search", bqAddDialog: "plus", bqStreamDialog: "link", editorDialog: "edit", powerDialog: "power",
    guideDialog: "calendar", timerDialog: "bell", atDialog: "bolt", erDialog: "calendar", svcDialog: "info", askDialog: "info", themeDialog: "palette", diagDialog: "plug", keysDialog: "keyboard",
    updDialog: "refresh", chmodDialog: "lock", commandEditDialog: "terminal", previewDialog: "eye",
  };
  // Information-only windows close when the backdrop is clicked (press and release both outside, so a drag that
  // starts inside a field and ends outside never closes anything).
  for (const id of ["diagDialog", "svcDialog"]) {
    const dialog = document.getElementById(id);
    if (!dialog) continue;
    let pressed = false;
    dialog.addEventListener("pointerdown", (event) => { pressed = event.target === dialog; });
    dialog.addEventListener("click", (event) => { if (pressed && event.target === dialog) dialog.close(); pressed = false; });
  }
  for (const dialog of document.querySelectorAll("dialog[id]")) {
    const heading = dialog.querySelector(".dialog-card h2");
    if (!heading) continue;
    let anchor = heading;
    while (anchor.parentElement && !anchor.parentElement.classList.contains("dialog-card")) anchor = anchor.parentElement;
    let head;
    if (anchor === heading) {
      head = document.createElement("div");
      anchor.before(head);
      head.append(heading);
    } else head = anchor;
    head.classList.add("dlg-head");
    head.prepend(icon(DIALOG_ICONS[dialog.id] || "info"));
    const own = head.querySelector('[aria-label="Close"], [id$="Close"]');
    const hasClose = Boolean(own);
    if (own) own.classList.add("dlg-close");
    const close = document.createElement("button");
    close.type = "button";
    close.className = "icon-btn dlg-close";
    close.title = "Close";
    close.setAttribute("aria-label", "Close");
    close.append(icon("x"));
    close.addEventListener("click", () => {
      const existing = dialog.querySelector('.dialog-actions [id$="Cancel"], .dialog-actions [id$="Close"]');
      if (existing) existing.click();
      else if (dialog.dispatchEvent(new Event("cancel", { cancelable: true }))) dialog.close();
    });
    if (!hasClose) head.append(close);
    // The header close already does this: a second "Close" in the footer is only noise.
    for (const dup of dialog.querySelectorAll('.dialog-actions [id$="Close"]')) {
      dup.classList.add("dlg-dup");
      const bar = dup.closest(".dialog-actions");
      if (bar && ![...bar.querySelectorAll("button")].some((b) => !b.classList.contains("dlg-dup"))) bar.classList.add("dlg-dup");
    }
    const field = dialog.querySelector('input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]):not([type="file"]), select, textarea');
    if (field && !dialog.querySelector("[autofocus]")) field.setAttribute("autofocus", "");
  }

  // Action buttons in toolbars show only their icon; the name moves into the tooltip.
  const ICONIFY = [
    ".view-actions .btn", ".device-actions .btn", ".connbar .btn", "#btnDownload", "#btnUpload", "button[data-cmd]",
    ".bq-tools .btn", "#liveToggle", "#remoteShow", "#channelShow", "#remoteMore", "#bqLoad", "#listsConnect", "#bqReload", "#bqSave", "#guideShow", ".tools-sec .btn", ".ipk-card .btn", "#pkUpdate", "#pkUpgrade", ".pk-item .btn", ".ft-actions .btn", "#faultsAckAll", ".tools-actions .btn",
  ].join(",");
  const SKIP = "#sigSound, [data-ico]";
  function iconify(button) {
    if (button.matches(SKIP) || button.closest("dialog")) return;
    const svg = button.querySelector(":scope > svg");
    if (!svg) return;
    const kbd = button.querySelector(":scope > kbd");
    const label = [...button.childNodes].filter((n) => n !== svg && n !== kbd).map((n) => n.textContent).join(" ").replace(/\s+/g, " ").trim();
    if (!label) return;
    button.dataset.ico = "1";
    button.classList.add("ico-only");
    const tip = kbd ? `${label} (${kbd.textContent.trim()})` : label;
    button.title = !button.title || button.title === label ? tip : (button.title.includes(label) ? button.title : `${label}: ${button.title}`);
    button.setAttribute("aria-label", label);
    for (const node of [...button.childNodes]) {
      if (node.nodeType === 3 && node.textContent.trim()) { const span = document.createElement("span"); span.className = "lbl-txt"; span.textContent = node.textContent; node.replaceWith(span); }
    }
  }
  let iconifyQueued = false;
  function iconifyAll() { iconifyQueued = false; for (const button of document.querySelectorAll(ICONIFY)) iconify(button); }
  new MutationObserver(() => { if (!iconifyQueued) { iconifyQueued = true; requestAnimationFrame(iconifyAll); } })
    .observe(document.querySelector("main") || document.body, { childList: true, subtree: true });
  iconifyAll();

  // Long explanations live behind a small (i) button instead of sitting in the layout.
  let helpSeq = 0;
  function helpify(el) {
    const id = el.id || (el.id = `help-${++helpSeq}`);
    el.setAttribute("popover", "auto");
    el.classList.add("help-pop");
    el.classList.remove("muted", "small");
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "icon-btn help-btn";
    btn.title = "More information";
    btn.setAttribute("aria-label", "More information");
    btn.setAttribute("popovertarget", id);
    btn.append(icon("info"));
    el.addEventListener("toggle", (event) => {
      if (event.newState !== "open") return;
      const r = btn.getBoundingClientRect(), w = el.offsetWidth, hgt = el.offsetHeight;
      el.style.left = `${Math.max(8, Math.min(r.left, window.innerWidth - w - 8))}px`;
      const below = r.bottom + 6;
      el.style.top = `${below + hgt > window.innerHeight - 8 ? Math.max(8, r.top - hgt - 6) : below}px`;
    });
    new MutationObserver(() => { btn.hidden = el.hidden; }).observe(el, { attributes: true, attributeFilter: ["hidden"] });
    btn.hidden = el.hidden;
    const before = el.dataset.helpBefore && document.querySelector(el.dataset.helpBefore);
    const after = el.dataset.helpAfter && document.querySelector(el.dataset.helpAfter);
    if (before) { before.before(btn); return; }
    if (after) { after.after(btn); return; }
    let heading = null;
    for (let node = el.parentElement; node && node !== document.body && !heading; node = node.parentElement) {
      heading = node.querySelector("h2, h3");
    }
    if (!heading) { el.before(btn); return; }
    if (heading.parentElement.classList.contains("dlg-head")) { heading.after(btn); return; }
    const row = document.createElement("div");
    row.className = "help-row";
    heading.before(row);
    row.append(heading, btn);
  }
  for (const el of document.querySelectorAll("[data-help]")) helpify(el);

  syncView(); syncDevice(); syncTransfers();
})();
