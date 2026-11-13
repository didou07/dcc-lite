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

  const watch = (node, options, fn) => { if (node) new MutationObserver(fn).observe(node, options); };
  watch(document.body, { attributes: true, attributeFilter: ["data-view"] }, syncView);
  watch(dot, { attributes: true, attributeFilter: ["data-state", "title"] }, syncDevice);
  watch(select, { childList: true, subtree: true, attributes: true }, syncDevice);
  watch(summary, { childList: true, characterData: true, subtree: true }, syncTransfers);
  if (select) select.addEventListener("change", syncDevice);
  if (sbTransfers) sbTransfers.addEventListener("click", () => { if (typeof showView === "function") showView("files"); });

  const DIALOG_ICONS = {
    deviceDialog: "tv", histDialog: "history", termCfgDialog: "sliders", pasteDialog: "paste", raDialog: "lock",
    scanDialog: "search", bqAddDialog: "plus", bqStreamDialog: "link", editorDialog: "edit", powerDialog: "power",
    guideDialog: "calendar", timerDialog: "bell", askDialog: "info", themeDialog: "palette",
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
    head.append(close);
    // Keep initial focus on the first real field, not on the close button.
    const field = dialog.querySelector('input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]):not([type="file"]), select, textarea');
    if (field && !dialog.querySelector("[autofocus]")) field.setAttribute("autofocus", "");
  }

  syncView(); syncDevice(); syncTransfers();
})();
