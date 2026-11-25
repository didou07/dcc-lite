"use strict";

(() => {
  const $id = (id) => document.getElementById(id);
  const state = {
    commands: [],
    path: "",
    loaded: false,
    loading: null,
    error: null,
    dragging: "",
    activePane: store.get("termCommandPane", "system"),
  };
  const editor = $id("commandEditDialog");
  const list = $id("commandList");
  const dock = $id("termCommandDock");
  const makeID = () => globalThis.crypto?.randomUUID?.() || `cmd-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 9)}`;
  const validCommand = (value) => typeof value === "string" && value.trim() && value.trim().length <= 1000 && !/[\r\n\0]/.test(value);

  // Two ways to browse commands.
  const classic = () => store.get("termCmdStyle", "panel") === "classic";
  const overlay = () => window.matchMedia("(max-width: 760px)").matches;

  function typeCommand(item, run = false) {
    const tab = activeTab();
    if (!tab || tab.state !== "connected") { toast("Connect a receiver first"); return; }
    if (!replaceTerminalInput(tab, item.command, run)) return;
    if (run) rememberCommand(tab.profileID, item.command);
    if (classic() || overlay()) toggleDock(false);
    focusTab(tab);
    toast(run ? "Command executed" : "Command typed — press Enter to execute");
  }

  function selectPanel(name, focusSearch = false) {
    const panes = ["system", "history", "library"];
    if (!panes.includes(name)) name = "system";
    state.activePane = name;
    store.set("termCommandPane", name);
    dock.hidden = false;
    document.body.classList.remove("term-dock-hidden");
    $id("termDockToggle").setAttribute("aria-expanded", "true");
    $id("termDockToggle").setAttribute("aria-label", "Hide command tools");
    $id("termDockToggle").title = "Hide command tools";
    document.querySelectorAll("[data-command-tab]").forEach((button) => {
      const active = button.dataset.commandTab === name;
      button.classList.toggle("active", active);
      button.setAttribute("aria-selected", String(active));
      button.tabIndex = active ? 0 : -1;
    });
    $id("termCommandDock").querySelector(".command-dock-header")?.setAttribute("data-title", { system: "System commands", history: "Previous commands", library: "Scripts" }[name]);
    for (const pane of panes) $id(`commandPane${pane[0].toUpperCase()}${pane.slice(1)}`).hidden = pane !== name;
    if (name === "system") renderSystemCommands(); // list depends on the device type
    if (name === "history") renderHistory();
    if (name === "library") {
      ensureLibrary().catch((error) => toast(error.message || "Could not load the command library", "error"));
      renderLibrary();
    }
    if (focusSearch) {
      const target = { system: "systemCommandFilter", history: "histFilter", library: "commandFilter" }[name];
      $id(target)?.focus();
    }
    requestAnimationFrame(() => activeTab()?.fit?.fit?.());
  }

  function toggleDock(force) {
    const open = typeof force === "boolean" ? force : dock.hidden;
    dock.hidden = !open;
    document.body.classList.toggle("term-dock-hidden", !open);
    $id("termDockToggle").setAttribute("aria-expanded", String(open));
    $id("termDockToggle").setAttribute("aria-label", open ? "Hide command tools" : "Show command tools");
    $id("termDockToggle").title = open ? "Hide command tools" : "Show command tools";
    if (!classic() && !overlay()) store.set("termDockOpen", open);
    if (open) selectPanel(state.activePane || "system");
    else requestAnimationFrame(() => activeTab()?.fit?.fit?.());
  }

  function renderSystemCommands() {
    const box = $id("systemCommandList");
    if (!box) return;
    const query = $id("systemCommandFilter").value.trim().toLowerCase();
    const items = builtinCommands().filter((item) => !query || `${item.name}\n${item.command}`.toLowerCase().includes(query));
    box.replaceChildren();
    if (!items.length) {
      box.append(h("div", { class: "command-empty", text: "No system commands match your search." }));
      return;
    }
    for (const item of items) {
      box.append(h("div", { class: "dock-command-row", role: "listitem" },
        h("button", { class: "dock-command-main", type: "button", title: `Type ${item.name}`, onClick: () => typeCommand(item) },
          h("span", { class: "dock-command-title", text: item.name }),
          h("code", { class: "dock-command-preview", text: item.command, title: item.command })),
        iconButton("play", "Run now", () => typeCommand(item, true))));
    }
  }

  async function readLibrary() {
    const data = await api("/api/v1/commands");
    state.commands = Array.isArray(data.commands) ? data.commands : [];
    state.path = typeof data.path === "string" ? data.path : "";
    state.error = null;

    const legacy = store.get("snippets", []);
    if (Array.isArray(legacy) && legacy.length) {
      const merged = [...state.commands];
      for (const item of legacy) {
        if (!item || typeof item.name !== "string" || !validCommand(item.command)) continue;
        if (merged.some((saved) => saved.name === item.name && saved.command === item.command)) continue;
        merged.push({ id: String(item.id || makeID()), name: item.name.trim().slice(0, 80), command: item.command.trim() });
      }
      if (merged.length !== state.commands.length) {
        await api("/api/v1/commands", { method: "PUT", body: JSON.stringify({ commands: merged }) });
        state.commands = merged;
      }
      store.set("snippets", []);
    }
    state.loaded = true;
    renderLibrary();
  }

  async function ensureLibrary() {
    if (state.loaded) return;
    if (!state.loading) {
      state.loading = readLibrary().catch((error) => {
        state.error = error;
        throw error;
      }).finally(() => { state.loading = null; });
    }
    await state.loading;
  }

  async function persist(next, successMessage = "") {
    try {
      const response = await api("/api/v1/commands", {
        method: "PUT",
        body: JSON.stringify({ commands: next }),
      });
      state.commands = next;
      state.loaded = true;
      state.error = null;
      if (response?.path) state.path = response.path;
      renderLibrary();
      if (successMessage) toast(successMessage);
      return true;
    } catch (error) {
      toast(error.message || "Could not save the command library", "error");
      return false;
    }
  }

  function renderLibrary() {
    if (!list) return;
    const query = $id("commandFilter").value.trim().toLowerCase();
    const visible = state.commands.filter((item) => !query || `${item.name}\n${item.command}`.toLowerCase().includes(query));
    list.replaceChildren();
    $id("commandCount").textContent = `${state.commands.length} saved`;
    $id("commandPath").textContent = state.error
      ? `Library unavailable: ${state.error.message}`
      : state.path ? state.path : "Saved in the DCC Lite settings folder";
    $id("commandExport").disabled = state.commands.length === 0;

    if (!visible.length) {
      list.append(h("div", { class: "command-empty", text: state.commands.length ? "No scripts match your search." : "No saved scripts. Add a command or import dccscript.ini." }));
      return;
    }

    visible.forEach((item) => {
      const index = state.commands.findIndex((entry) => entry.id === item.id);
      const row = h("div", {
        class: "saved-command-row",
        role: "listitem",
        draggable: true,
        dataset: { commandId: item.id },
        onDragstart: (event) => {
          state.dragging = item.id;
          event.dataTransfer?.setData("text/plain", item.id);
          if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
          row.classList.add("dragging");
        },
        onDragend: () => {
          state.dragging = "";
          $$(".saved-command-row", list).forEach((node) => node.classList.remove("dragging", "drag-over"));
        },
        onDragover: (event) => {
          if (!state.dragging || state.dragging === item.id) return;
          event.preventDefault();
          row.classList.add("drag-over");
        },
        onDragleave: (event) => {
          if (!row.contains(event.relatedTarget)) row.classList.remove("drag-over");
        },
        onDrop: async (event) => {
          event.preventDefault();
          row.classList.remove("drag-over");
          const movingID = state.dragging || event.dataTransfer?.getData("text/plain");
          if (!movingID || movingID === item.id) return;
          const next = [...state.commands];
          const from = next.findIndex((entry) => entry.id === movingID);
          if (from < 0) return;
          const [moving] = next.splice(from, 1);
          const target = next.findIndex((entry) => entry.id === item.id);
          if (target < 0) return;
          const after = event.clientY > row.getBoundingClientRect().top + row.offsetHeight / 2;
          next.splice(target + (after ? 1 : 0), 0, moving);
          await persist(next);
        },
      },
        h("span", { class: "command-drag", title: "Drag to reorder", "aria-hidden": "true" }, icon("more")),
        h("div", { class: "command-info" },
          h("strong", { class: "command-name", text: item.name }),
          h("code", { class: "command-preview", text: item.command, title: item.command })),
        h("div", { class: "command-actions" },
          iconButton("play", "Run now", () => typeCommand(item, true)),
          iconButton("chev-up", "Move up", () => moveCommand(item.id, -1), "command-move"),
          iconButton("chev-down", "Move down", () => moveCommand(item.id, 1), "command-move"),
          iconButton("edit", "Edit script", () => editSavedCommand(item)),
          iconButton("trash", "Delete script", () => deleteCommand(item.id), "danger")));
      row.querySelector('[aria-label="Move up"]').disabled = index <= 0;
      row.querySelector('[aria-label="Move down"]').disabled = index < 0 || index >= state.commands.length - 1;
      list.append(row);
    });
  }

  async function moveCommand(id, offset) {
    const next = [...state.commands];
    const from = next.findIndex((item) => item.id === id);
    const to = from + offset;
    if (from < 0 || to < 0 || to >= next.length) return;
    [next[from], next[to]] = [next[to], next[from]];
    await persist(next);
  }

  function editDialog(item = null) {
    $id("commandEditTitle").textContent = item ? "Edit script" : "Add script";
    $id("commandEditName").value = item?.name || "";
    $id("commandEditText").value = item?.command || "";
    $id("commandEditError").hidden = true;
    openDialog(editor);
    $id("commandEditName").focus();
    return new Promise((resolve) => {
      let finished = false;
      const finish = (value) => {
        if (finished) return;
        finished = true;
        $id("commandEditSave").removeEventListener("click", save);
        $id("commandEditCancel").removeEventListener("click", cancel);
        editor.removeEventListener("cancel", cancel);
        editor.removeEventListener("close", closed);
        if (editor.open) editor.close();
        resolve(value);
      };
      const save = () => {
        const name = $id("commandEditName").value.trim();
        const command = $id("commandEditText").value.trim();
        let problem = "";
        if (!name) problem = "Enter a name for this script.";
        else if (name.length > 80) problem = "The name must be 80 characters or fewer.";
        else if (!validCommand(command)) problem = "Enter a single-line command (up to 1,000 characters).";
        if (problem) {
          $id("commandEditError").textContent = problem;
          $id("commandEditError").hidden = false;
          return;
        }
        finish({ name, command });
      };
      const cancel = (event) => { event?.preventDefault?.(); finish(null); };
      const closed = () => finish(null);
      $id("commandEditSave").addEventListener("click", save);
      $id("commandEditCancel").addEventListener("click", cancel);
      editor.addEventListener("cancel", cancel);
      editor.addEventListener("close", closed);
    });
  }

  async function editSavedCommand(item = null) {
    const value = await editDialog(item);
    if (!value) return;
    const next = [...state.commands];
    if (item) {
      const index = next.findIndex((entry) => entry.id === item.id);
      if (index < 0) return;
      next[index] = { ...item, ...value };
    } else {
      next.push({ id: makeID(), ...value });
    }
    await persist(next, item ? "Script updated" : "Script added");
  }

  async function deleteCommand(id) {
    const item = state.commands.find((entry) => entry.id === id);
    if (!item) return;
    const ok = await ask({ title: `Delete “${item.name}”?`, message: "This removes the script from the library. It does not affect commands already sent to a receiver.", input: false, ok: "Delete", danger: true });
    if (!ok) return;
    await persist(state.commands.filter((entry) => entry.id !== id), "Script deleted");
  }

  function exportLibrary() {
    if (!state.commands.length) return;
    const text = globalThis.DccImport.serializeDccScripts(state.commands);
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
    const link = h("a", { href: url, download: "dccscript.ini" });
    document.body.append(link); link.click(); link.remove(); URL.revokeObjectURL(url);
    toast("Command library exported");
  }

  async function importLibrary(file) {
    if (!file) return;
    if (file.size > 256 * 1024) { toast("That file is too big to be a dccscript.ini", "error"); return; }
    let parsed;
    try { parsed = globalThis.DccImport.parseDccScripts(await file.text()); }
    catch (error) { toast(error.message || "Could not read the script file", "error"); return; }
    const scripts = parsed.scripts || [];
    if (!scripts.length) {
      toast(parsed.skipped?.length ? "No usable user scripts in that file" : "No [UserScripts] section found — is this a dccscript.ini?", "error");
      return;
    }
    const seen = new Set(state.commands.map((item) => `${item.name}\0${item.command}`));
    const imported = [];
    for (const script of scripts) {
      const key = `${script.name}\0${script.command}`;
      if (seen.has(key)) continue;
      seen.add(key);
      imported.push({ id: makeID(), name: script.name, command: script.command });
    }
    if (!imported.length) { toast("Those scripts are already in the library"); return; }
    const note = [`${scripts.length} command${scripts.length === 1 ? "" : "s"} found.`, `${state.commands.length} currently saved.`, "Merge adds new scripts and keeps the current library. Replace clears the current library first.", "Imported commands are typed into the terminal; they are never run automatically."];
    if (parsed.skipped?.length) note.push(`Skipped: ${parsed.skipped.map((entry) => `${entry.name} (${entry.reason})`).join(", ")}.`);
    const choice = await ask({ title: "Import dccscript.ini", message: note.join("\n"), input: false, ok: `Merge ${imported.length}`, alt: "Replace library" });
    if (!choice) return;
    const next = choice === "alt" ? imported : [...state.commands, ...imported];
    await persist(next, `${choice === "alt" ? imported.length : imported.length} script${imported.length === 1 ? "" : "s"} imported`);
  }

  document.querySelectorAll("[data-command-tab]").forEach((button) => {
    button.addEventListener("click", () => selectPanel(button.dataset.commandTab));
  });
  // Classic style (as in 1.1.4).
  function classicOpen(name, anchor) {
    const sub = (c) => (c.length > 22 ? `${c.slice(0, 22)}…` : c);
    const rect = anchor.getBoundingClientRect();
    if (name === "history") { if (!dock.hidden && state.activePane === "history") toggleDock(false); else selectPanel("history", true); return; }
    toggleDock(false);
    const row = (c, own) => ({
      label: c.name, icon: "bolt", sub: sub(c.command), searchable: true, find: c.command,
      run: () => typeCommand(c), runNow: () => typeCommand(c, true),
      actions: own ? [{ icon: "edit", title: "Edit", run: () => editSavedCommand(c) }, { icon: "trash", title: "Delete", danger: true, run: () => deleteCommand(c.id) }] : undefined,
    });
    const manage = [
      { label: "Add a script…", icon: "plus", run: () => editSavedCommand() },
      { label: "Import dccscript.ini…", icon: "upload", run: () => $id("commandImportFile").click() },
      { label: "Export as dccscript.ini", icon: "download", disabled: !state.commands.length, run: () => exportLibrary() },
      { label: "Manage scripts (reorder)…", icon: "list", run: () => selectPanel("library", true) },
    ];
    const build = () => {
      const items = [{ heading: `Scripts (${state.commands.length})` }];
      if (state.commands.length) items.push(...state.commands.map((c) => row(c, true)));
      else items.push({ label: "No scripts yet", disabled: true });
      items.push("-", ...manage);
      if (name === "system") items.push("-", { heading: "System commands" }, ...builtinCommands().map((c) => row(c, false)));
      openMenu(items, rect.left, rect.bottom + 4, { search: "Search scripts and commands…" });
    };
    readLibrary().catch((error) => { state.error = error; }).then(build);
  }
  function applyCommandStyle() {
    const on = classic();
    document.body.classList.toggle("cmd-classic", on);
    const toggle = $id("termDockToggle");
    toggle.querySelector("use")?.setAttribute("href", on ? "#i-bolt" : "#i-list");
    $id("termHistory").hidden = !on;
    $id("termScripts").hidden = !on;
    if (on) {
      dock.hidden = true;
      document.body.classList.add("term-dock-hidden");
      toggle.title = "System commands"; toggle.setAttribute("aria-label", "System commands"); toggle.removeAttribute("aria-expanded");
      requestAnimationFrame(() => activeTab()?.fit?.fit?.());
    } else {
      toggle.setAttribute("aria-expanded", "true");
      selectPanel(state.activePane || "system");
    }
  }
  $id("termHistory").addEventListener("click", (event) => classicOpen("history", event.currentTarget));
  $id("termScripts").addEventListener("click", (event) => classicOpen("scripts", event.currentTarget));
  $id("termDockToggle").addEventListener("click", (event) => { if (classic()) classicOpen("system", event.currentTarget); else toggleDock(); });
  $id("termCustomize").addEventListener("click", () => { const sel = $id("termCfgCmdStyle"); if (sel) sel.value = classic() ? "classic" : "panel"; });
  $id("termCfgCmdStyle")?.addEventListener("change", (event) => { store.set("termCmdStyle", event.target.value === "classic" ? "classic" : "panel"); applyCommandStyle(); });
  $id("termDockClose").addEventListener("click", () => toggleDock(false));
  $id("systemCommandFilter").addEventListener("input", renderSystemCommands);
  $id("commandAdd").addEventListener("click", () => editSavedCommand());
  $id("commandImport").addEventListener("click", () => $id("commandImportFile").click());
  $id("commandReload").addEventListener("click", async () => {
    state.loaded = false;
    state.loading = null;
    try {
      await ensureLibrary();
      toast("Command library reloaded from disk");
    } catch (error) {
      state.error = error;
      renderLibrary();
      toast(error.message || "Could not reload the command library", "error");
    }
  });
  $id("commandExport").addEventListener("click", exportLibrary);
  $id("commandFilter").addEventListener("input", renderLibrary);
  $id("commandImportFile").addEventListener("change", async (event) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    try { await ensureLibrary(); }
    catch (error) { toast(error.message || "Could not load the command library", "error"); return; }
    await importLibrary(file);
  });

  globalThis.DccCommandPanel = {
    show: (name, focusSearch = false) => selectPanel(name, focusSearch),
    toggle: toggleDock,
    refreshHistory: () => { if (state.activePane === "history") renderHistory(); },
    refreshSystem: () => renderSystemCommands(),
  };

  document.addEventListener("pointerdown", (event) => {
    if ((!classic() && !overlay()) || dock.hidden || event.target.closest("#termCommandDock, #termDockToggle, dialog, #menu")) return;
    toggleDock(false);
  });
  document.addEventListener("keydown", (event) => {
    if ((classic() || overlay()) && event.key === "Escape" && !dock.hidden && !document.querySelector("dialog[open]")) { toggleDock(false); activeTab() && focusTab(activeTab()); }
  });

  renderSystemCommands();
  if (classic()) applyCommandStyle();
  else if (overlay() || store.get("termDockOpen", true) === false) {
    dock.hidden = true;
    document.body.classList.add("term-dock-hidden");
    const toggle = $id("termDockToggle");
    toggle.setAttribute("aria-expanded", "false"); toggle.setAttribute("aria-label", "Show command tools"); toggle.title = "Show command tools";
  } else selectPanel(state.activePane, false);
  ensureLibrary().catch(() => {});
})();
