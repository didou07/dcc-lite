"use strict";
/* ───────────── Terminal ───────────── */
const term = {
  tabs: new Map(), active: "",
  protocol: "telnet",
  fontSize: Math.max(9, Math.min(28, Number(store.get("termFont", 14)) || 14)),
  theme: ["midnight", "black", "light"].includes(store.get("termTheme", "midnight")) ? store.get("termTheme", "midnight") : "midnight",
};
const TERMINAL_KEYS = { "ctrl-c": "\u0003", "ctrl-d": "\u0004", tab: "\t", escape: "\u001b", up: "\u001b[A", down: "\u001b[B", right: "\u001b[C", left: "\u001b[D", home: "\u001b[H", end: "\u001b[F", pageup: "\u001b[5~", pagedown: "\u001b[6~" };
const DEFAULT_SNIPPETS = [
  { id: "uptime", name: "Uptime", command: "uptime" },
  { id: "disk", name: "Disk usage", command: "df -h" },
  { id: "memory", name: "Memory", command: "free -m" },
  { id: "ip", name: "IP addresses", command: "ip -4 addr" },
  { id: "image", name: "Image version", command: "cat /etc/image-version 2>/dev/null; cat /etc/os-release 2>/dev/null" },
  { id: "ps", name: "Processes", command: "ps" },
  { id: "mount", name: "Mounted drives", command: "mount" },
  { id: "kernel", name: "Kernel", command: "uname -a" },
  { id: "opkg", name: "Update package list", command: "opkg update" },
  { id: "cams", name: "Running softcams", command: "ps | grep -i -E 'oscam|ncam|cccam|gbox|mgcamd' | grep -v grep" },
  { id: "restart-gui", name: "Restart Enigma2 (GUI)", command: "init 4 && sleep 3 && init 3" },
  { id: "reload-bouquets", name: "Reload bouquets (OpenWebif)", command: "wget -qO- 'http://127.0.0.1/web/servicelistreload?mode=2'" },
  { id: "drop-caches", name: "Free memory caches", command: "sync; echo 3 > /proc/sys/vm/drop_caches" },
  { id: "hdd-free", name: "Recording disk space", command: "df -h /media/hdd /hdd 2>/dev/null" },
  { id: "reboot", name: "Reboot receiver", command: "reboot" },
];

/* Clear colours: every theme has a full ANSI palette chosen for contrast (the default blue on a dark
   background is unreadable), and xterm additionally lifts any text below a 4.5:1 contrast ratio.
   No violet or pink anywhere: ANSI "magenta" is a sand/orange. */
function terminalTheme(name) {
  const dark = {
    black: "#3a434b", red: "#ff6b6b", green: "#5fd38d", yellow: "#f4c95d", blue: "#5db7ff", magenta: "#e3a46c", cyan: "#4fd8d8", white: "#dfe6eb",
    brightBlack: "#8b98a3", brightRed: "#ff8f8f", brightGreen: "#86e8ab", brightYellow: "#ffe08a", brightBlue: "#8ccdff", brightMagenta: "#f4bb88", brightCyan: "#86eded", brightWhite: "#ffffff",
  };
  if (name === "light") return { background: "#fbfcfc", foreground: "#101820", cursor: "#075985", cursorAccent: "#ffffff", selectionBackground: "#7fbbdc99",
    black: "#101820", red: "#b42318", green: "#176b3a", yellow: "#8a5a00", blue: "#0a5a9c", magenta: "#9a5a12", cyan: "#0e6f7a", white: "#d7dde1",
    brightBlack: "#52606b", brightRed: "#d92d20", brightGreen: "#1f8a4c", brightYellow: "#a86b00", brightBlue: "#0d72bd", brightMagenta: "#b86d18", brightCyan: "#118799", brightWhite: "#ffffff" };
  if (name === "black") return { ...dark, background: "#000000", foreground: "#f4f8fa", cursor: "#5eead4", cursorAccent: "#000000", selectionBackground: "#2f86b3aa" };
  return { ...dark, background: "#0b0e10", foreground: "#eef3f6", cursor: "#5eead4", cursorAccent: "#0b0e10", selectionBackground: "#2f86b3aa" };
}
const activeTab = () => term.tabs.get(term.active) || null;

function terminalInputChunks(data, maxBytes = 4096) {
  const encoder = new TextEncoder(), chunks = [];
  let chunk = "", bytes = 0;
  for (const character of data) {
    const size = encoder.encode(character).length;
    if (chunk && bytes + size > maxBytes) { chunks.push(chunk); chunk = ""; bytes = 0; }
    chunk += character; bytes += size;
  }
  if (chunk) chunks.push(chunk);
  return chunks;
}
function termSend(tab, data) {
  if (!tab || tab.socket?.readyState !== WebSocket.OPEN || typeof data !== "string" || !data) return false;
  try { for (const chunk of terminalInputChunks(data)) tab.socket.send(JSON.stringify({ type: "input", data: chunk })); return true; }
  catch (error) { toastError(error); return false; }
}
function bufferText(tab) {
  const buffer = tab.terminal.buffer.active, lines = [];
  for (let row = 0; row < buffer.length; row++) lines.push(buffer.getLine(row)?.translateToString(true) ?? "");
  while (lines.length && !lines[lines.length - 1]) lines.pop();
  return lines.join("\n");
}
function tabDimensions(tab) {
  try {
    const proposed = tab.fit.proposeDimensions?.();
    if (proposed?.cols && proposed?.rows) return { cols: Math.max(20, Math.min(240, proposed.cols)), rows: Math.max(5, Math.min(100, proposed.rows)) };
  } catch { /* not measurable yet */ }
  return { cols: Math.max(20, Math.min(240, tab.terminal.cols || 100)), rows: Math.max(5, Math.min(100, tab.terminal.rows || 30)) };
}
function fitTab(tab) {
  if (!tab || tab.screen.hidden || !tab.screen.clientWidth || !tab.screen.clientHeight) return;
  try {
    tab.fit.fit();
    const size = { cols: tab.terminal.cols, rows: tab.terminal.rows };
    if (tab.state === "connected" && tab.protocol === "telnet" && tab.socket?.readyState === WebSocket.OPEN && (tab.lastSize?.cols !== size.cols || tab.lastSize?.rows !== size.rows)) {
      tab.lastSize = size;
      tab.socket.send(JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }));
    }
  } catch { /* hidden or detached */ }
}

function tabTitle(tab) {
  if (tab.name) return tab.name;
  const device = deviceByID(tab.profileID);
  const same = [...term.tabs.values()].filter((t) => t.profileID === tab.profileID && t.protocol === tab.protocol);
  const index = same.indexOf(tab) + 1;
  return `${device ? device.name : "New tab"} · ${tab.protocol.toUpperCase()}${same.length > 1 ? ` #${index}` : ""}`;
}
function renderTabs() {
  const list = $("#terminalTabs");
  list.replaceChildren();
  for (const tab of term.tabs.values()) {
    const state = tab.state === "connected" ? "online" : tab.state === "connecting" ? "checking" : tab.state === "error" ? "error" : "unknown";
    list.append(h("div", { class: `tt${tab.id === term.active ? " active" : ""}`, role: "tab", "aria-selected": String(tab.id === term.active), dataset: { id: tab.id }, onClick: () => activateTab(tab.id),
      onDblclick: (event) => { event.preventDefault(); renameTab(tab); },
      onContextmenu: (event) => { event.preventDefault(); openTabMenu(tab, event.clientX, event.clientY); } },
      h("span", { class: "dot", dataset: { state } }),
      h("button", { class: "tt-label", type: "button", title: `${tabTitle(tab)} — ${tab.protocol.toUpperCase()} · ${tab.host || "not connected"} (double-click to rename)`, text: tabTitle(tab) }),
      iconButton("x", "Close tab", (event) => { event.stopPropagation(); closeTab(tab.id); })));
    tab.screen.hidden = tab.id !== term.active;
  }
  updateTerminalBar();
}
function updateTerminalBar() {
  const tab = activeTab();
  const state = tab?.state || "idle";
  const connected = state === "connected", busy = state === "connecting";
  $("#terminalConnect").hidden = connected;
  $("#terminalConnect").disabled = !tab || busy || !deviceByID(tab?.profileID);
  $("#terminalDisconnect").hidden = !(connected || busy);
  $("#terminalPort").value = String(tab?.port || defaultPort(currentDevice(), term.protocol));
  $("#terminalPort").disabled = connected || busy;
  const pill = $("#terminalStatus");
  pill.dataset.state = state === "disconnected" ? "idle" : state;
  pill.textContent = !tab ? "No session" : connected ? `${tab.host || "Connected"} · ${tab.protocol.toUpperCase()}` : busy ? (tab.reconnecting ? "Reconnecting…" : "Connecting…") : state === "error" ? (tab.error || "Connection error") : "Disconnected";
  pill.title = pill.textContent;
  $("#terminalFiles").disabled = !tab || !deviceByID(tab.profileID);
  if (tab) tab.terminal.options.disableStdin = !connected;
}

/* xterm receives the keyboard through a hidden text box. Microsoft Edge decorates any
   text box with its own helpers (writing suggestions, Copilot/Editor, autofill, the
   text-cursor indicator); on the terminal they show up as a stray blue dot. The box is
   marked so that none of them attach to it. */
function hardenTerminalInput(terminal) {
  const box = terminal.textarea;
  if (!box) return;
  const hints = {
    autocomplete: "off", autocorrect: "off", autocapitalize: "off", spellcheck: "false", writingsuggestions: "false",
    "aria-autocomplete": "none", "data-gramm": "false", "data-gramm_editor": "false", "data-enable-grammarly": "false",
    "data-lpignore": "true", "data-1p-ignore": "true", "data-form-type": "other", "data-bwignore": "true", name: "terminal-input",
  };
  for (const [name, value] of Object.entries(hints)) box.setAttribute(name, value);
}

function createTerminalTab({ profileID = "", protocol = term.protocol, port = null, directory = "", initialCommand = "", autoConnect = true } = {}) {
  if (!window.DCCXterm?.Terminal || !window.DCCXterm?.FitAddon) { toast("The bundled terminal engine could not be loaded.", "error"); return null; }
  const id = (crypto.randomUUID?.() || `t${Date.now()}${Math.random().toString(16).slice(2)}`).replace(/[^a-zA-Z0-9_-]/g, "");
  const screen = h("div", { class: "terminal-screen", id: `screen-${id}`, role: "tabpanel", hidden: true });
  $("#terminalScreens").append(screen);
  const terminal = new window.DCCXterm.Terminal({
    cols: 100, rows: 30, cursorBlink: true, cursorStyle: "bar", cursorWidth: 2, cursorInactiveStyle: "bar", scrollback: 8000,
    fontFamily: '"Cascadia Code", "Cascadia Mono", Consolas, "Liberation Mono", monospace', fontSize: term.fontSize, lineHeight: 1.25, fontWeight: "500", fontWeightBold: "700", minimumContrastRatio: 4.5,
    theme: terminalTheme(term.theme), rightClickSelectsWord: true, allowTransparency: false,
  });
  const fit = new window.DCCXterm.FitAddon();
  terminal.loadAddon(fit);
  terminal.open(screen);
  hardenTerminalInput(terminal);
  terminal.options.disableStdin = true;
  const tab = {
    id, profileID, protocol, port: port || defaultPort(deviceByID(profileID), protocol), directory, initialCommand,
    screen, terminal, fit, decoder: new TextDecoder("utf-8"), socket: null, sessionID: "", state: "disconnected", host: "", error: "",
    lastSize: null, manualOff: !autoConnect, subs: [], observer: null, findRow: -1, name: "", line: "", dirty: false, histIndex: -1, histDraft: "", selTimer: 0,
    wsPath: "", rx: 0, sessionEnded: false, reconnecting: false, watchdog: 0, wake: null, lastRx: 0, pingSentAt: 0,
  };
  tab.subs.push(terminal.onData((data) => { if (tab.state === "connected") { trackTypedInput(tab, data); termSend(tab, data); } }));
  tab.subs.push(terminal.onResize(({ cols, rows }) => {
    if (tab.state !== "connected" || tab.protocol !== "telnet" || tab.socket?.readyState !== WebSocket.OPEN) return;
    if (tab.lastSize?.cols === cols && tab.lastSize?.rows === rows) return;
    tab.lastSize = { cols, rows };
    tab.socket.send(JSON.stringify({ type: "resize", cols, rows }));
  }));
  terminal.attachCustomKeyEventHandler((event) => {
    if (event.type !== "keydown") return true;
    if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "t") { event.preventDefault(); newTabFromActive(); return false; }
    const key = event.key.toLowerCase();
    if ((event.key === "ArrowUp" || event.key === "ArrowDown") && !event.ctrlKey && !event.altKey && !event.shiftKey && !event.metaKey && canRecallHistory(tab)) {
      event.preventDefault(); historyStep(tab, event.key === "ArrowUp" ? 1 : -1); return false;
    }
    if (event.ctrlKey && event.shiftKey && key === "c") { event.preventDefault(); copyTerminal(); return false; }
    if (event.ctrlKey && event.shiftKey && key === "h") { event.preventDefault(); openHistory(); return false; }
    // Copy with Ctrl+C or Ctrl+Insert when text is selected; otherwise Ctrl+C stays "interrupt".
    if (event.ctrlKey && !event.shiftKey && !event.altKey && (key === "c" || key === "insert") && terminal.hasSelection()) { event.preventDefault(); copyTerminal(); return false; }
    // Let the browser's own paste event reach xterm (it also works on plain HTTP); xterm would otherwise send ^V.
    if ((event.ctrlKey && key === "v") || (event.shiftKey && key === "insert")) return false;
    if (event.altKey && !event.ctrlKey && !event.shiftKey && /^[1-9]$/.test(event.key)) { event.preventDefault(); switchTabByNumber(Number(event.key)); return false; }
    return true;
  });
  if (typeof ResizeObserver !== "undefined") { tab.observer = new ResizeObserver(() => fitTab(tab)); tab.observer.observe(screen); }
  screen.addEventListener("click", () => focusTab(tab));
  // Capture phase: xterm handles right-click itself (it moves its input box under the pointer) and the page menu must win.
  screen.addEventListener("contextmenu", (event) => { event.preventDefault(); event.stopPropagation(); openTerminalMenu(tab, event.clientX, event.clientY); }, true);
  tab.subs.push(terminal.onSelectionChange(() => {
    if (!store.get("termCopyOnSelect", false)) return;
    window.clearTimeout(tab.selTimer);
    tab.selTimer = window.setTimeout(() => { const text = terminal.getSelection(); if (text) clipboardWrite(text); }, 200);
  }));
  screen.addEventListener("wheel", (event) => {
    if (!event.ctrlKey) return;
    event.preventDefault();
    term.fontSize = Math.max(9, Math.min(28, term.fontSize + (event.deltaY < 0 ? 1 : -1)));
    applyTerminalLook();
  }, { passive: false });
  terminal.writeln("DCC Lite · Terminal");
  term.tabs.set(id, tab);
  term.active = id;
  renderTabs();
  requestAnimationFrame(() => { fitTab(tab); if (profileID && autoConnect) connectTab(tab); else terminal.writeln(profileID ? "Press Connect to sign in." : "Choose a device, then press Connect."); });
  return tab;
}
function newTabFromActive() {
  const tab = activeTab(), device = currentDevice();
  const profileID = tab?.profileID || device?.id || "";
  if (!profileID) { toast("Add a device first"); showView("devices"); return; }
  createTerminalTab({ profileID, protocol: tab?.protocol || term.protocol, port: tab?.port });
}
function focusTab(tab = activeTab()) {
  if (!tab || tab.id !== term.active || view.current !== "terminal") return;
  tab.terminal.options.disableStdin = tab.state !== "connected";
  tab.terminal.focus();
}
function activateTab(id) {
  if (!term.tabs.has(id)) return;
  term.active = id;
  const tab = activeTab();
  const device = deviceByID(tab.profileID);
  if (device && device.id !== devices.currentID) { devices.currentID = device.id; store.set("device", device.id); $("#deviceSelect").value = device.id; updateDeviceDot(); renderDeviceGrid(); fileDisconnect(true); }
  renderTabs();
  requestAnimationFrame(() => { fitTab(tab); focusTab(tab); });
}
async function closeTab(id) {
  const tab = term.tabs.get(id);
  if (!tab) return;
  await dropSession(tab);
  tab.observer?.disconnect();
  tab.subs.forEach((s) => s?.dispose?.());
  tab.terminal.dispose();
  tab.screen.remove();
  const ids = [...term.tabs.keys()], index = ids.indexOf(id);
  term.tabs.delete(id);
  if (term.active === id) term.active = ids[index + 1] || ids[index - 1] || "";
  if (!term.tabs.size) { const d = currentDevice(); createTerminalTab({ profileID: d?.id || "", protocol: term.protocol, autoConnect: false }); return; }
  renderTabs();
  requestAnimationFrame(() => { fitTab(activeTab()); focusTab(); });
}
function closeTabsOfDevice(profileID) {
  for (const tab of [...term.tabs.values()]) if (tab.profileID === profileID) closeTab(tab.id);
}
async function dropSession(tab) {
  const socket = tab.socket, sessionID = tab.sessionID;
  tab.socket = null; tab.sessionID = ""; tab.reconnecting = false;
  window.clearInterval(tab.watchdog); tab.wake?.();
  if (socket && socket.readyState === WebSocket.OPEN) { try { socket.send(JSON.stringify({ type: "close" })); } catch { /* closing */ } socket.close(); }
  else if (socket && socket.readyState !== WebSocket.CLOSED) socket.close();
  if (sessionID) await api(`/api/v1/terminal/sessions/${encodeURIComponent(sessionID)}`, { method: "DELETE" }).catch(() => {});
}
function setTabState(tab, state, message = "") {
  tab.state = state;
  tab.error = state === "error" ? message : "";
  if (message) tab.terminal.writeln(`\r\n[${message}]`);
  renderTabs();
  if (state === "error" && message) toast(message, "error");
}

/* Terminal connection that survives a dropped WebSocket.
   The shell keeps running on the server for a while after the browser connection
   is lost (sleep, Wi-Fi change, proxy timeout). The page therefore reconnects to the
   same session and asks for the output after the last byte it received, so nothing
   is lost and nothing appears twice. */
const RECONNECT_DELAYS = [400, 1000, 2000, 3000, 5000];
const RECONNECT_WINDOW_MS = 9 * 60 * 1000; // a little under the server's grace period
const PING_EVERY_MS = 15000;

function openTerminalSocket(tab) {
  const url = new URL(tab.wsPath, window.location.href);
  url.protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  if (tab.rx > 0) url.searchParams.set("offset", String(tab.rx));
  const socket = new WebSocket(url);
  tab.socket = socket;
  return new Promise((resolve, reject) => {
    const fail = (message) => { window.clearTimeout(timeout); if (tab.socket === socket) tab.socket = null; try { socket.close(); } catch { /* already closed */ } reject(new Error(message)); };
    const timeout = window.setTimeout(() => fail("The terminal connection timed out."), 8000);
    socket.addEventListener("open", () => { window.clearTimeout(timeout); resolve(socket); }, { once: true });
    socket.addEventListener("error", () => fail("Could not open the terminal connection."), { once: true });
  });
}

function wireTerminalSocket(tab, socket) {
  tab.lastRx = performance.now(); tab.pingSentAt = 0;
  socket.addEventListener("message", (event) => {
    if (tab.socket !== socket) return;
    tab.lastRx = performance.now();
    let message;
    try { message = JSON.parse(event.data); } catch { return; }
    if (message.type === "output") {
      try {
        const bytes = Uint8Array.from(atob(message.data || ""), (c) => c.charCodeAt(0));
        tab.rx = Number.isFinite(message.end) ? message.end : tab.rx + bytes.length;
        tab.terminal.write(tab.decoder.decode(bytes, { stream: true }));
      } catch { tab.terminal.writeln("\r\n[Invalid terminal output]"); }
    } else if (message.type === "notice") toast(message.message);
    else if (message.type === "closed") { tab.sessionEnded = true; setTabState(tab, "disconnected", "Session closed by the device"); }
  });
  socket.addEventListener("close", () => {
    if (tab.socket !== socket) return;
    tab.socket = null;
    window.clearInterval(tab.watchdog);
    tab.terminal.write(tab.decoder.decode());
    if (tab.state === "connected" && !tab.sessionEnded && !tab.manualOff) reconnectTab(tab);
    else { tab.sessionID = ""; if (tab.state === "connected") setTabState(tab, "disconnected", "Disconnected"); }
  });
  // Browsers cannot see WebSocket pings, so the page pings too. A connection that
  // stays silent after a ping (cable pulled, laptop slept) is replaced at once
  // instead of waiting for the operating system to notice.
  window.clearInterval(tab.watchdog);
  tab.watchdog = window.setInterval(() => probeTab(tab, socket), PING_EVERY_MS);
}
function probeTab(tab, socket) {
  if (tab.socket !== socket) { window.clearInterval(tab.watchdog); return; }
  const now = performance.now();
  if (tab.pingSentAt && tab.lastRx < tab.pingSentAt && now - tab.pingSentAt > 8000) { abandonSocket(tab); return; }
  if (socket.readyState !== WebSocket.OPEN) return;
  tab.pingSentAt = now;
  try { socket.send(JSON.stringify({ type: "ping" })); } catch { abandonSocket(tab); }
}
function abandonSocket(tab) {
  const socket = tab.socket;
  tab.socket = null;
  window.clearInterval(tab.watchdog);
  try { socket?.close(); } catch { /* already closed */ }
  if (tab.state === "connected" && !tab.sessionEnded && !tab.manualOff) reconnectTab(tab);
}
function reconnectWait(tab, ms) {
  return new Promise((resolve) => {
    const timer = window.setTimeout(done, ms);
    function done() { window.clearTimeout(timer); tab.wake = null; resolve(); }
    tab.wake = done;
  });
}
async function reconnectTab(tab) {
  if (tab.reconnecting) return;
  tab.reconnecting = true;
  const began = Date.now();
  tab.terminal.write("\r\n[Connection lost — reconnecting…]\r\n");
  setTabState(tab, "connecting");
  for (let attempt = 0; ; attempt += 1) {
    if (!term.tabs.has(tab.id) || tab.manualOff || !tab.sessionID) { tab.reconnecting = false; return; }
    if (Date.now() - began > RECONNECT_WINDOW_MS) {
      tab.reconnecting = false; tab.sessionID = "";
      setTabState(tab, "disconnected", "Could not reconnect. Press Connect to start a new session");
      return;
    }
    await reconnectWait(tab, RECONNECT_DELAYS[Math.min(attempt, RECONNECT_DELAYS.length - 1)]);
    if (!term.tabs.has(tab.id) || tab.manualOff || !tab.sessionID) { tab.reconnecting = false; return; }
    try {
      const status = await fetch(`/api/v1/terminal/sessions/${encodeURIComponent(tab.sessionID)}`, { cache: "no-store" });
      if (status.status === 401) { window.location.assign("/login"); return; }
      if (status.status === 404) {
        tab.reconnecting = false; tab.sessionID = "";
        setTabState(tab, "disconnected", "The session no longer exists on the server. Press Connect to start a new one");
        return;
      }
      if (!status.ok) throw new Error(`Status ${status.status}`);
      const socket = await openTerminalSocket(tab);
      if (!term.tabs.has(tab.id) || tab.manualOff || tab.socket !== socket) { socket.close(); tab.reconnecting = false; return; }
      tab.reconnecting = false; tab.lastSize = null;
      wireTerminalSocket(tab, socket);
      setTabState(tab, "connected", "Reconnected");
      fitTab(tab);
      requestAnimationFrame(() => focusTab(tab));
      return;
    } catch { /* the network is still down: try again */ }
  }
}
function wakeReconnects() { for (const tab of term.tabs.values()) tab.wake?.(); }
window.addEventListener("online", wakeReconnects);
document.addEventListener("visibilitychange", () => {
  if (document.hidden) return;
  wakeReconnects();
  // Back from sleep or another tab: check the live connections right away.
  for (const tab of term.tabs.values()) if (tab.socket?.readyState === WebSocket.OPEN) { tab.pingSentAt = 0; probeTab(tab, tab.socket); }
});

async function connectTab(tab) {
  if (!tab || tab.state === "connecting" || tab.state === "connected") return;
  const device = deviceByID(tab.profileID);
  if (!device) { toast("Choose a device first"); return; }
  const port = Number(tab.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) { toast("Enter a valid port", "error"); return; }
  tab.manualOff = false;
  tab.decoder = new TextDecoder("utf-8"); tab.lastSize = null;
  tab.terminal.reset();
  tab.terminal.writeln(`Connecting to ${device.host} (${tab.protocol.toUpperCase()} ${port})…`);
  setTabState(tab, "connecting");
  const size = tabDimensions(tab);
  const payload = { profile_id: device.id, protocol: tab.protocol, port, columns: size.cols, rows: size.rows };
  if (tab.directory) payload.initial_directory = tab.directory;
  if (tab.initialCommand) payload.initial_command = tab.initialCommand;
  try {
    const created = await post("/api/v1/terminal/sessions", payload);
    if (!term.tabs.has(tab.id)) { api(`/api/v1/terminal/sessions/${encodeURIComponent(created.session.id)}`, { method: "DELETE" }).catch(() => {}); return; }
    tab.sessionID = created.session.id;
    tab.host = created.session.host || device.host;
    tab.wsPath = created.websocket;
    tab.rx = 0; tab.sessionEnded = false; tab.reconnecting = false;
    const socket = await openTerminalSocket(tab);
    if (!term.tabs.has(tab.id) || tab.socket !== socket) { socket.close(); return; }
    tab.initialCommand = ""; tab.directory = "";
    setTabState(tab, "connected");
    fitTab(tab);
    wireTerminalSocket(tab, socket);
    requestAnimationFrame(() => focusTab(tab));
  } catch (error) {
    await dropSession(tab);
    setTabState(tab, "error", error.message);
  }
}
async function disconnectTab(tab) {
  if (!tab) return;
  tab.manualOff = true;
  await dropSession(tab);
  setTabState(tab, "disconnected", "Disconnected");
}
/** Called whenever the Terminal view is shown or the device changes. */
function terminalEnter() {
  const device = currentDevice();
  if (!window.DCCXterm) { window.setTimeout(terminalEnter, 80); return; }
  if (device) {
    const own = [...term.tabs.values()].filter((t) => t.profileID === device.id);
    const current = activeTab();
    if (!current || current.profileID !== device.id) {
      if (own.length) { term.active = own[own.length - 1].id; renderTabs(); }
      else createTerminalTab({ profileID: device.id, protocol: term.protocol, port: defaultPort(device, term.protocol) });
    }
  } else if (!term.tabs.size) createTerminalTab({ profileID: "", autoConnect: false });
  renderTabs();
  requestAnimationFrame(() => { fitTab(activeTab()); focusTab(); });
}

/* Terminal toolbar */
$("#terminalNewTab").addEventListener("click", newTabFromActive);
$("#terminalConnect").addEventListener("click", () => connectTab(activeTab()));
$("#terminalDisconnect").addEventListener("click", () => disconnectTab(activeTab()));
$("#terminalPort").addEventListener("change", () => { const tab = activeTab(); if (tab) tab.port = Number($("#terminalPort").value); });
$("#terminalFiles").addEventListener("click", () => { const tab = activeTab(); if (tab?.profileID) openDevice(tab.profileID, "files"); });
$$("#quickKeys button").forEach((button) => button.addEventListener("click", () => {
  const tab = activeTab(), key = button.dataset.key;
  if ((key === "up" || key === "down") && canRecallHistory(tab)) historyStep(tab, key === "up" ? 1 : -1);
  else termSend(tab, TERMINAL_KEYS[key]);
  focusTab(tab);
}));

/* Clipboard. navigator.clipboard only exists on HTTPS or localhost, so when the site is opened over
   plain HTTP (public IP / No-IP) copying falls back to a hidden text box + execCommand("copy"),
   and pasting to Ctrl+V in the terminal or to a small paste box. */
async function clipboardWrite(text) {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(text); return true; } catch { /* fall back */ }
  }
  const active = document.activeElement;
  const box = h("textarea", { "aria-hidden": "true", tabindex: "-1", readonly: true, style: "position:fixed;left:-9999px;top:0;opacity:0" });
  box.value = text;
  document.body.append(box);
  box.select();
  box.setSelectionRange(0, text.length);
  let ok = false;
  try { ok = document.execCommand("copy"); } catch { ok = false; }
  box.remove();
  active?.focus?.({ preventScroll: true });
  return ok;
}
async function clipboardRead() {
  if (window.isSecureContext && navigator.clipboard?.readText) {
    try { return await navigator.clipboard.readText(); } catch { return null; }
  }
  return null;
}
async function copyTerminal() {
  const tab = activeTab();
  if (!tab) return;
  const selected = tab.terminal.getSelection();
  const ok = await clipboardWrite(selected || bufferText(tab));
  if (ok) { toast(selected ? "Selection copied" : "All output copied"); tab.terminal.clearSelection(); }
  else toast("Copy was blocked by the browser. Select the text and press Ctrl+C.", "error");
  focusTab(tab);
}
function openPasteBox(tab) {
  const dialog = $("#pasteDialog");
  $("#pasteText").value = "";
  openDialog(dialog);
  $("#pasteText").focus();
  const done = () => { dialog.removeEventListener("close", done); focusTab(tab); };
  dialog.addEventListener("close", done);
}
$("#pasteForm").addEventListener("submit", () => {
  const tab = activeTab(), text = $("#pasteText").value;
  if (tab && tab.state === "connected" && text) tab.terminal.paste(text);
});
$("#pasteCancel").addEventListener("click", () => $("#pasteDialog").close());
async function pasteIntoTerminal(tab = activeTab()) {
  if (!tab || tab.state !== "connected") { toast("Connect first"); return; }
  const text = await clipboardRead();
  if (text === null) { openPasteBox(tab); return; }
  if (text) tab.terminal.paste(text);
  focusTab(tab);
}
$("#termCopy").addEventListener("click", copyTerminal);
$("#termPaste").addEventListener("click", () => pasteIntoTerminal());

function openTerminalMenu(tab, x, y) {
  const connected = tab.state === "connected";
  openMenu([
    { label: "Copy", icon: "copy", sub: "Ctrl+Shift+C", disabled: !tab.terminal.hasSelection(), run: copyTerminal },
    { label: "Paste", icon: "paste", sub: "Ctrl+V", disabled: !connected, run: () => pasteIntoTerminal(tab) },
    { label: "Select all", icon: "check", run: () => { tab.terminal.selectAll(); } },
    { label: store.get("termCopyOnSelect", false) ? "✓ Copy when selecting" : "Copy when selecting", icon: "copy", run: () => { store.set("termCopyOnSelect", !store.get("termCopyOnSelect", false)); toast(store.get("termCopyOnSelect", false) ? "Selected text is copied automatically" : "Automatic copy is off"); } },
    "-",
    { label: "Previous commands…", icon: "history", sub: "Ctrl+Shift+H", run: openHistory },
    { label: "Copy all output", icon: "copy", run: () => { tab.terminal.clearSelection(); copyTerminal(); } },
    { label: "Clear screen", icon: "eraser", run: () => { tab.terminal.clear(); focusTab(tab); } },
  ], x, y);
}

/* Tabs: rename, switch by name or number */
async function renameTab(tab) {
  const value = await ask({ title: "Rename terminal tab", label: "Name (leave empty for the automatic name)", value: tab.name, ok: "Rename", validate: (v) => (v.length > 40 ? "Use at most 40 characters." : "") });
  if (value === null || value === undefined) return;
  tab.name = value.trim().slice(0, 40);
  renderTabs();
  focusTab(tab);
}
function openTabMenu(tab, x, y) {
  const others = [...term.tabs.values()].filter((t) => t.id !== tab.id);
  openMenu([
    { label: "Rename…", icon: "edit", run: () => renameTab(tab) },
    { label: "Duplicate tab", icon: "plus", disabled: !tab.profileID, run: () => createTerminalTab({ profileID: tab.profileID, protocol: tab.protocol, port: tab.port }) },
    "-",
    { label: "Close tab", icon: "x", run: () => closeTab(tab.id) },
    { label: "Close other tabs", icon: "x", disabled: !others.length, danger: true, run: () => others.forEach((t) => closeTab(t.id)) },
  ], x, y);
}
function switchTabByNumber(number) {
  const tab = [...term.tabs.values()][number - 1];
  if (tab) activateTab(tab.id);
}
$("#terminalTabList").addEventListener("click", (event) => {
  const tabs = [...term.tabs.values()];
  const rect = event.currentTarget.getBoundingClientRect();
  openMenu([{ heading: "Terminal tabs" }, ...tabs.map((tab, index) => ({
    label: `${tab.id === term.active ? "● " : ""}${tabTitle(tab)}`,
    icon: "terminal", sub: index < 9 ? `Alt+${index + 1}` : tab.state === "connected" ? "connected" : "",
    run: () => activateTab(tab.id),
  }))], rect.left, rect.bottom + 4);
});

/* Previous commands, remembered per device in this browser. */
const HISTORY_MAX = 300;
const historyKey = (profileID) => `cmdhist.${profileID || "none"}`;
function loadHistory(profileID) {
  const list = store.get(historyKey(profileID), []);
  return Array.isArray(list) ? list.filter((c) => typeof c === "string" && c) : [];
}
function rememberCommand(profileID, command) {
  if (!profileID || !command || command.length > 500) return;
  const list = loadHistory(profileID).filter((c) => c !== command);
  list.unshift(command);
  store.set(historyKey(profileID), list.slice(0, HISTORY_MAX));
}
/** Follows what is typed so that Enter can store the finished line. Cursor keys, Tab and history
    recall make the line unknown, so those lines are not stored (the shell has them already). */
function trackTypedInput(tab, data) {
  if (data.startsWith("\u001b")) { tab.dirty = true; return; }
  tab.histIndex = -1;
  const single = data === "\r" || data === "\n";
  for (const ch of data) {
    if (ch === "\r" || ch === "\n") {
      const command = tab.line.trim();
      const wasDirty = tab.dirty || tab.line !== tab.line.trimStart();
      tab.line = ""; tab.dirty = false;
      if (!command || wasDirty || command.length < 2) continue;
      if (single && !echoedOnScreen(tab, command)) continue; // nothing echoed: a password or hidden input
      if (/^(passw(or)?d|login)\s*:?$/i.test(command)) continue;
      rememberCommand(tab.profileID, command);
    } else if (ch === "\u007f" || ch === "\b") tab.line = tab.line.slice(0, -1);
    else if (ch === "\u0003" || ch === "\u0015" || ch === "\u0004") { tab.line = ""; tab.dirty = false; }
    else if (ch === "\t") tab.dirty = true;
    else if (ch >= " ") tab.line += ch;
  }
}
function echoedOnScreen(tab, command) {
  try {
    const buffer = tab.terminal.buffer.active;
    const text = (buffer.getLine(buffer.baseY + buffer.cursorY)?.translateToString(true) || "").trimEnd();
    if (/(pass(word|phrase|wd)?|login|user ?name)[^#$>%]*:\s*$/i.test(text)) return false;
    return text.endsWith(command.slice(-Math.min(command.length, 4)));
  } catch { return false; }
}
/** Up / Down walk through the commands of this device like in a real terminal; the line being typed is kept as a draft. */
function canRecallHistory(tab) {
  return Boolean(tab) && tab.state === "connected" && view.current === "terminal" && tab.terminal.buffer.active.type !== "alternate";
}
function historyStep(tab, direction) {
  const list = loadHistory(tab.profileID);
  if (tab.histIndex < 0 && direction > 0) tab.histDraft = tab.dirty ? "" : tab.line;
  const next = tab.histIndex + direction;
  if (next < -1 || next >= list.length) return;
  const text = next === -1 ? tab.histDraft : list[next];
  const erase = tab.dirty ? "\u0005\u0015" : "\u007f".repeat([...tab.line].length); // end + kill line, or one Backspace per typed character
  termSend(tab, erase + text);
  tab.line = text; tab.dirty = false; tab.histIndex = next;
}
function renderHistory() {
  const tab = activeTab();
  const filter = $("#histFilter").value.trim().toLowerCase();
  const all = loadHistory(tab?.profileID);
  const list = (filter ? all.filter((c) => c.toLowerCase().includes(filter)) : all).slice(0, 100);
  const box = $("#histList");
  box.replaceChildren();
  if (!list.length) { box.append(h("div", { class: "hist-empty", text: all.length ? "No command matches." : "No commands yet. Commands you run in this terminal appear here." })); return list; }
  for (const command of list) {
    box.append(h("div", { class: "hist-item", role: "listitem" },
      h("button", { class: "hist-cmd", type: "button", title: command, text: command, onClick: () => useHistoryCommand(command, false) }),
      iconButton("play", "Run now", () => useHistoryCommand(command, true)),
      iconButton("trash", "Remove from history", () => {
        const profileID = activeTab()?.profileID;
        store.set(historyKey(profileID), loadHistory(profileID).filter((c) => c !== command));
        renderHistory();
      })));
  }
  return list;
}
function useHistoryCommand(command, run) {
  const tab = activeTab();
  if (!tab || tab.state !== "connected") { toast("Connect first"); return; }
  $("#histDialog").close();
  termSend(tab, command + (run ? "\r" : ""));
  if (run) rememberCommand(tab.profileID, command);
  focusTab(tab);
}
function openHistory() {
  const tab = activeTab();
  if (!tab) return;
  $("#histSub").textContent = `${deviceByID(tab.profileID)?.name || "This tab"} — most recent first`;
  $("#histFilter").value = "";
  renderHistory();
  openDialog($("#histDialog"));
  $("#histFilter").focus();
}
$("#termHistory").addEventListener("click", openHistory);
$("#histClose").addEventListener("click", () => $("#histDialog").close());
$("#histFilter").addEventListener("input", renderHistory);
$("#histFilter").addEventListener("keydown", (event) => {
  if (event.key !== "Enter") return;
  event.preventDefault();
  const first = $("#histList .hist-cmd")?.title;
  if (first) useHistoryCommand(first, event.shiftKey);
});
$("#histClear").addEventListener("click", async () => {
  const tab = activeTab();
  if (!tab || !loadHistory(tab.profileID).length) return;
  if (await ask({ title: "Clear the command history of this device?", input: false, ok: "Clear", danger: true })) { store.set(historyKey(tab.profileID), []); renderHistory(); }
});
$("#termClear").addEventListener("click", () => { const tab = activeTab(); if (tab) { tab.terminal.clear(); focusTab(tab); } });
$("#termSave").addEventListener("click", () => {
  const tab = activeTab();
  if (!tab) return;
  const url = URL.createObjectURL(new Blob([bufferText(tab)], { type: "text/plain;charset=utf-8" }));
  const link = h("a", { href: url, download: `dcc-lite-${(tab.host || "terminal").replace(/[^a-zA-Z0-9_.-]/g, "_")}-${new Date().toISOString().replace(/[:.]/g, "-")}.txt` });
  document.body.append(link); link.click(); link.remove(); URL.revokeObjectURL(url);
  toast("Output saved");
});
function applyTerminalLook() {
  $("#termFontLabel").textContent = String(term.fontSize);
  $("#termTheme").value = term.theme;
  $("#terminalFrame").dataset.theme = term.theme;
  for (const tab of term.tabs.values()) { tab.terminal.options.fontSize = term.fontSize; tab.terminal.options.theme = terminalTheme(term.theme); }
  fitTab(activeTab());
  store.set("termFont", term.fontSize); store.set("termTheme", term.theme);
}
$("#termFontDown").addEventListener("click", () => { term.fontSize = Math.max(9, term.fontSize - 1); applyTerminalLook(); });
$("#termFontUp").addEventListener("click", () => { term.fontSize = Math.min(28, term.fontSize + 1); applyTerminalLook(); });
$("#termTheme").addEventListener("change", (event) => { term.theme = event.target.value; applyTerminalLook(); });
$("#termFullscreen").addEventListener("click", async () => {
  const frame = $("#terminalFrame");
  try { if (document.fullscreenElement === frame) await document.exitFullscreen(); else await frame.requestFullscreen(); }
  catch { toast("Full screen is not available in this window", "error"); }
});
document.addEventListener("fullscreenchange", () => requestAnimationFrame(() => fitTab(activeTab())));
$("#termFind").addEventListener("keydown", (event) => {
  if (event.key !== "Enter") return;
  event.preventDefault();
  const tab = activeTab(), query = event.currentTarget.value;
  if (!tab || !query) return;
  const buffer = tab.terminal.buffer.active, needle = query.toLocaleLowerCase(), step = event.shiftKey ? 1 : -1;
  let row = tab.findRow >= 0 ? tab.findRow + step : (step < 0 ? buffer.length - 1 : 0);
  for (let n = 0; n < buffer.length; n++, row += step) {
    if (row < 0) row = buffer.length - 1; if (row >= buffer.length) row = 0;
    const text = buffer.getLine(row)?.translateToString(false) || "", at = text.toLocaleLowerCase().indexOf(needle);
    if (at < 0) continue;
    tab.findRow = row; tab.terminal.scrollToLine(Math.max(0, row - 3)); tab.terminal.select(at, row, query.length);
    return;
  }
  tab.findRow = -1; toast("Not found in the terminal output");
});
$("#termFind").addEventListener("input", () => { const tab = activeTab(); if (tab) tab.findRow = -1; });

/* Network scan ("Search IP address") */
const scan = { busy: false };
async function openScanDialog() {
  $("#scanError").hidden = true;
  $("#scanStatus").textContent = "";
  $("#scanResults").replaceChildren();
  openDialog($("#scanDialog"));
  try {
    const { ranges } = await api("/api/v1/network/ranges");
    $("#scanRanges").replaceChildren(...ranges.map((range) => Object.assign(document.createElement("option"), { value: range })));
    if (!$("#scanRange").value && ranges.length) $("#scanRange").value = ranges[0];
  } catch { /* the field still works when typed by hand */ }
  $("#scanRange").focus();
}
function renderScanResults(hosts) {
  const known = new Set(devices.list.map((device) => device.host));
  const list = $("#scanResults");
  list.replaceChildren();
  for (const host of hosts) {
    const item = document.createElement("li");
    const info = document.createElement("div");
    info.className = "scan-host";
    const ip = document.createElement("b");
    ip.textContent = host.ip;
    const detail = document.createElement("small");
    detail.textContent = host.banner || "No banner";
    info.append(ip, detail);
    const tags = document.createElement("div");
    tags.className = "scan-tags";
    for (const [flag, label] of [[host.ftp, "FTP"], [host.telnet, "Telnet"], [host.http, "Web"]]) {
      if (!flag) continue;
      const tag = document.createElement("span");
      tag.className = "badge";
      tag.textContent = label;
      tags.append(tag);
    }
    const action = document.createElement("button");
    action.type = "button";
    action.className = "btn sm";
    if (known.has(host.ip)) { action.textContent = "Saved"; action.disabled = true; }
    else {
      action.textContent = "Add";
      action.addEventListener("click", () => {
        $("#scanDialog").close();
        openDeviceDialog();
        $("#devHost").value = host.ip;
        $("#devName").value = `Receiver ${host.ip.split(".").pop()}`;
      });
    }
    item.append(info, tags, action);
    list.append(item);
  }
}
$("#devicesScan").addEventListener("click", openScanDialog);
$("#scanClose").addEventListener("click", () => $("#scanDialog").close());
$("#scanForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (scan.busy) return;
  scan.busy = true;
  $("#scanStart").disabled = true;
  $("#scanError").hidden = true;
  $("#scanResults").replaceChildren();
  $("#scanStatus").textContent = "Scanning… this can take up to a minute for large ranges.";
  try {
    const data = await api("/api/v1/network/scan", { method: "POST", body: JSON.stringify({ range: $("#scanRange").value.trim() }) });
    renderScanResults(data.hosts);
    $("#scanStatus").textContent = `${data.hosts.length} device${data.hosts.length === 1 ? "" : "s"} found among ${data.scanned} addresses in ${(data.duration_ms / 1000).toFixed(1)} s${data.complete ? "" : " (stopped early)"}.`;
  } catch (error) {
    $("#scanStatus").textContent = "";
    $("#scanError").textContent = error.message;
    $("#scanError").hidden = false;
  } finally {
    scan.busy = false;
    $("#scanStart").disabled = false;
  }
});

/* Saved commands */
function loadCustomSnippets() {
  const list = store.get("snippets", []);
  return Array.isArray(list) ? list.filter((s) => s && typeof s.name === "string" && typeof s.command === "string" && s.id) : [];
}
$("#termSnippets").addEventListener("click", (event) => {
  const custom = loadCustomSnippets();
  const insert = (snippet) => () => {
    const tab = activeTab();
    if (!tab || tab.state !== "connected") { toast("Connect first"); return; }
    termSend(tab, snippet.command); focusTab(tab); toast("Command typed — press Enter to run it");
  };
  const items = [{ heading: "Saved commands" }, ...DEFAULT_SNIPPETS.map((s) => ({ label: s.name, icon: "bolt", sub: s.command.slice(0, 22), run: insert(s) }))];
  if (custom.length) items.push("-", { heading: "Mine" }, ...custom.map((s) => ({ label: s.name, icon: "bolt", sub: s.command.slice(0, 22), run: insert(s) })));
  items.push("-", { label: "Add a command…", icon: "plus", run: async () => {
    const name = await ask({ title: "New saved command", label: "Name", ok: "Next", validate: (v) => (v ? "" : "Enter a name.") });
    if (!name) return;
    const command = await ask({ title: name, label: "Command (typed into the terminal, you press Enter)", ok: "Save", validate: (v) => (!v ? "Enter a command." : /[\r\n\0]/.test(v) ? "Use a single line." : "") });
    if (!command) return;
    if (custom.length >= 30) { toast("You can save up to 30 commands", "error"); return; }
    store.set("snippets", [...custom, { id: `c${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`, name: name.slice(0, 80), command: command.slice(0, 1000) }]);
    toast("Command saved");
  } });
  items.push({ label: "Import DCC scripts (dccscript.ini)…", icon: "upload", run: () => $("#dccScriptFile").click() });
  for (const s of custom) items.push({ label: `Remove “${s.name}”`, icon: "trash", danger: true, run: () => { store.set("snippets", custom.filter((c) => c.id !== s.id)); toast("Command removed"); } });
  const rect = event.currentTarget.getBoundingClientRect();
  openMenu(items, rect.left, rect.bottom + 4);
});

$("#dccScriptFile").addEventListener("change", async (event) => {
  const file = event.target.files[0];
  event.target.value = "";
  if (!file) return;
  if (file.size > 256 * 1024) { toast("That file is too big to be a dccscript.ini", "error"); return; }
  const { scripts, skipped } = globalThis.DccImport.parseDccScripts(await file.text());
  if (!scripts.length) { toast(skipped.length ? "No usable user scripts in that file" : "No [UserScripts] section found — is this a dccscript.ini?", "error"); return; }
  const custom = loadCustomSnippets();
  const fresh = scripts.filter((script) => !custom.some((c) => c.name === script.name && c.command === script.command));
  const room = Math.max(0, 30 - custom.length);
  const take = fresh.slice(0, room);
  if (!take.length) { toast(fresh.length ? "You already have 30 saved commands" : "Those scripts are already saved"); return; }
  const note = [`${take.length} script${take.length === 1 ? "" : "s"} will be added to Saved commands:`, ...take.map((script) => `• ${script.name}: ${script.command}`)];
  if (fresh.length > take.length) note.push(`(${fresh.length - take.length} more do not fit: the limit is 30 saved commands.)`);
  if (skipped.length) note.push(`Skipped: ${skipped.map((item) => `${item.name} (${item.reason})`).join(", ")}.`);
  note.push("They are only typed into the terminal; you press Enter to run them.");
  const ok = await ask({ title: "Import DCC scripts", message: note.join("\n"), input: false, ok: "Import" });
  if (!ok) return;
  store.set("snippets", [...custom, ...take.map((script) => ({ id: `c${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`, name: script.name, command: script.command }))]);
  toast(`${take.length} command${take.length === 1 ? "" : "s"} imported`);
});

/* Global shortcuts */
document.addEventListener("keydown", (event) => {
  if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "t" && view.current === "terminal") { event.preventDefault(); newTabFromActive(); }
});
window.addEventListener("resize", () => fitTab(activeTab()));
// Leaving the page ends its shells at once; they would otherwise wait on the server
// for a reconnect that cannot come. (Not on bfcache: the page may come back.)
window.addEventListener("pagehide", (event) => {
  if (event.persisted) return;
  for (const tab of term.tabs.values()) {
    if (tab.sessionID) fetch(`/api/v1/terminal/sessions/${encodeURIComponent(tab.sessionID)}`, { method: "DELETE", keepalive: true }).catch(() => {});
    if (tab.socket?.readyState === WebSocket.OPEN) tab.socket.close();
  }
});
