"use strict";
// Right-click menu for text boxes: Cut, Copy, Paste, Select all.
(() => {
  const TEXT_TYPES = new Set(["", "text", "search", "url", "tel", "email", "password"]);
  const isTextField = (node) => node.tagName === "TEXTAREA" || (node.tagName === "INPUT" && TEXT_TYPES.has((node.getAttribute("type") || "").toLowerCase()));

  function textMenu(event) {
    if (event.defaultPrevented) return;
    const target = event.target instanceof Element ? event.target : null;
    if (!target || target.closest(".xterm, #menu")) return;
    const found = target.closest("input, textarea");
    const field = found && isTextField(found) && !found.disabled ? found : null;
    const picked = field ? "" : String(window.getSelection?.() || "");
    if (!field && !picked) return;
    event.preventDefault();

    if (!field) {
      openMenu([{ label: "Copy", icon: "copy", sub: "Ctrl+C", run: async () => { toast(await clipboardWrite(picked) ? "Copied" : "Could not copy", "info"); } }], event.clientX, event.clientY);
      return;
    }

    const secret = (field.getAttribute("type") || "").toLowerCase() === "password";
    const start = field.selectionStart ?? 0, end = field.selectionEnd ?? 0;
    const text = field.value.slice(start, end);
    const readOnly = field.readOnly;
    const refocus = () => { field.focus({ preventScroll: true }); try { field.setSelectionRange(start, end); } catch { } };
    const replace = (value) => {
      acted = true;
      refocus();
      field.setRangeText(value, start, end, "end");
      field.dispatchEvent(new Event("input", { bubbles: true }));
    };
    let acted = false;
    const copy = async () => { acted = true; const ok = await clipboardWrite(text); refocus(); field.setSelectionRange(end, end); toast(ok ? "Copied" : "Could not copy", ok ? "info" : "error"); return ok; };
    const cut = async () => { if (await copy()) replace(""); };
    const paste = async () => {
      acted = true;
      let value = await clipboardRead();
      if (value === null) { refocus(); toast("Press Ctrl+V to paste here"); return; }
      if (field.tagName === "INPUT") value = value.replace(/\r?\n/g, " ");
      replace(value);
    };
    openMenu([
      { label: "Cut", icon: "scissors", sub: "Ctrl+X", disabled: readOnly || secret || !text, run: cut },
      { label: "Copy", icon: "copy", sub: "Ctrl+C", disabled: secret || !text, run: copy },
      { label: "Paste", icon: "paste", sub: "Ctrl+V", disabled: readOnly, run: paste },
      "-",
      { label: "Select all", icon: "check", sub: "Ctrl+A", disabled: !field.value, run: () => { acted = true; field.focus({ preventScroll: true }); field.select(); } },
    ], event.clientX, event.clientY);
    const menu = $("#menu");
    const watch = new MutationObserver(() => {
      if (!menu.hidden) return;
      watch.disconnect();
      window.setTimeout(() => { if (!acted && field.selectionStart !== field.selectionEnd) field.setSelectionRange(end, end); }, 0);
    });
    watch.observe(menu, { attributes: true, attributeFilter: ["hidden"] });
  }

  document.addEventListener("contextmenu", textMenu);
  window.DCC_I18N?.add?.("ar", { "Cut": "قص", "Copy": "نسخ", "Paste": "لصق", "Copied": "تم النسخ", "Could not copy": "تعذّر النسخ", "Press Ctrl+V to paste here": "اضغط Ctrl+V للصق هنا" });
})();
