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
  // Very narrow phones: language picker moves into the Appearance window.
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
  // Buttons that lose their text on small screens keep it as a tooltip / accessible name.
  for (const button of document.querySelectorAll(".view-actions .btn, .logs-tools .btn, .screen-tools > .btn")) {
    const text = button.textContent.trim();
    if (text && !button.title) button.title = text;
    if (text && !button.getAttribute("aria-label")) button.setAttribute("aria-label", text);
  }
  if (compactQuery.addEventListener) compactQuery.addEventListener("change", placeRailFoot); else compactQuery.addListener(placeRailFoot);
  // The side rail shows icons only: keep the names as tooltips / accessible names.
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
  };
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
    // A header that already carries its own close button (the guide) must not get a second one.
    const hasClose = Boolean(head.querySelector('[aria-label="Close"], [id$="Close"]'));
    const close = document.createElement("button");
    close.type = "button";
    close.className = "icon-btn dlg-close";
    close.title = "Close";
    close.setAttribute("aria-label", "Close");
    close.append(icon("x"));
    close.addEventListener("click", () => {
      const existing = dialog.querySelector('[id$="Cancel"], [id$="Close"]');
      if (existing) existing.click(); else dialog.close();
    });
    if (!hasClose) head.append(close);
    // Keep initial focus on the first real field, not on the close button.
    const field = dialog.querySelector('input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]):not([type="file"]), select, textarea');
    if (field && !dialog.querySelector("[autofocus]")) field.setAttribute("autofocus", "");
  }

  syncView(); syncDevice(); syncTransfers();
})();
