"use strict";
/* ───────────── Remote control ─────────────
   The on-screen remote sits beside the live picture. A key is sent by name to
   DCC Lite, which presses it on the saved receiver through its web interface; the
   page never sends a raw key code or an address. */
const GLYPHS = {
  rewind: '<path d="M11 6v12L3 12zM21 6v12l-8-6z"/>', play: '<path d="M7 5v14l12-7z"/>', pause: '<path d="M7 5h4v14H7zM13 5h4v14h-4z"/>',
  forward: '<path d="M13 6v12l8-6zM3 6v12l8-6z"/>', skipback: '<path d="M6 5h2.5v14H6zM20 5v14L9 12z"/>', record: '<circle cx="12" cy="12" r="6"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="1"/>', skipnext: '<path d="M15.5 5H18v14h-2.5zM4 5v14l11-7z"/>',
  up: '<path d="M12 7l7 10H5z"/>', down: '<path d="M12 17 5 7h14z"/>', left: '<path d="M7 12l10-7v14z"/>', right: '<path d="M17 12 7 19V5z"/>',
};
// [key, label or glyph, extra classes]
const REMOTE_ROWS = [
  [["1", "1"], ["2", "2"], ["3", "3"]], [["4", "4"], ["5", "5"], ["6", "6"]], [["7", "7"], ["8", "8"], ["9", "9"]],
  [["prev", "<", "nav"], ["0", "0"], ["next", ">", "nav"]],
  [["red", "", "red"], ["green", "", "green"], ["yellow", "", "yellow"], ["blue", "", "blue"]],
  [["menu", "Menu"], ["up", "@up", "nav"], ["exit", "Exit"]],
  [["left", "@left", "nav"], ["ok", "OK", "nav ok"], ["right", "@right", "nav"]],
  [["info", "Info"], ["down", "@down", "nav"], ["epg", "EPG"]],
  [["volup", "Vol+", "vol"], ["mute", "Mute"], ["chup", "CH+", "ch"]],
  [["voldown", "Vol-", "vol"], ["history", "History"], ["chdown", "CH-", "ch"]],
  [["rewind", "@rewind"], ["play", "@play"], ["pause", "@pause"], ["forward", "@forward"]],
  [["skipback", "@skipback"], ["record", "@record", "rec"], ["stop", "@stop"], ["skipnext", "@skipnext"]],
  [["text", "Text", "small"], ["audio", "Audio", "small"], ["subtitle", "Sub", "small"], ["video", "Video", "small"]],
  [["pvr", "PVR", "small"], ["tv", "TV", "small"], ["radio", "Radio", "small"], ["help", "Help", "small"]],
  [["setup", "Setup", "small"], ["portal", "Portal", "small"], ["sleep", "Sleep", "small"], ["timer", "Timer", "small"]],
  [["f1", "F1", "small"], ["f2", "F2", "small"], ["fav", "Fav", "small"], ["pip", "PIP", "small"]],
  [["back", "Back", "small"], ["opt", "Opt", "small"], ["context", "Context", "small"], ["aspect", "Aspect", "small"]],
];
const KEY_TITLES = { prev: "Previous", next: "Next", volup: "Volume up", voldown: "Volume down", chup: "Channel up", chdown: "Channel down", up: "Up", down: "Down", left: "Left", right: "Right",
  rewind: "Rewind", play: "Play", pause: "Pause", forward: "Fast forward", skipback: "Previous chapter", record: "Record", stop: "Stop", skipnext: "Next chapter", subtitle: "Subtitles", red: "Red", green: "Green", yellow: "Yellow", blue: "Blue" };

const remote = { queue: Promise.resolve(), pending: 0, hold: null, holdFired: false };

function remoteError(message) {
  const node = $("#remoteError");
  node.hidden = !message;
  node.textContent = message || "";
}
function sendRemoteKey(key, long = false, button = null) {
  if (!screenView.profileID || remote.pending >= 6) return Promise.resolve();
  remote.pending += 1;
  button?.classList.add("sending");
  const profileID = screenView.profileID;
  remote.queue = remote.queue.then(async () => {
    try {
      await post(`/api/v1/profiles/${encodeURIComponent(profileID)}/remote-key`, long ? { key, long: true } : { key });
      remoteError("");
      // Give the receiver a moment to redraw, then show the result.
      if ($("#remoteRefresh").checked && profileID === screenView.profileID) window.setTimeout(() => { if (profileID === screenView.profileID && !screenView.paused) screenRestart(); }, 350);
    } catch (error) { remoteError(error.message); }
    finally { remote.pending -= 1; button?.classList.remove("sending"); }
  });
  return remote.queue;
}
function buildRemote() {
  const holder = $("#remoteKeys");
  holder.replaceChildren();
  REMOTE_ROWS.forEach((row, rowIndex) => {
    holder.append(h("div", { class: rowIndex >= 8 ? "remote-row extra" : "remote-row" }, row.map(([key, label, extra = ""]) => {
      const button = h("button", { class: `rk ${extra}`.trim(), type: "button", title: KEY_TITLES[key] || label || key, dataset: { key }, "aria-label": KEY_TITLES[key] || label || key });
      if (label.startsWith("@")) {
        button.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${GLYPHS[label.slice(1)]}</svg>`;
      } else button.textContent = label;
      return button;
    })));
  });
  // Phones: only the main keys are shown under the picture; "More keys" opens the rest.
  const more = $("#remoteMore");
  more.onclick = () => {
    const open = $("#remotePad").classList.toggle("more");
    more.setAttribute("aria-expanded", String(open));
    more.querySelector("span").textContent = open ? "Fewer keys" : "More keys";
  };
}
function remoteBind() {
  const holder = $("#remoteKeys");
  // A long press: shift + click, or holding the key for about 0.7 s.
  holder.addEventListener("pointerdown", (event) => {
    const button = event.target.closest(".rk");
    if (!button || event.button > 0) return;
    remote.holdFired = false;
    window.clearTimeout(remote.hold);
    remote.hold = window.setTimeout(() => { remote.holdFired = true; sendRemoteKey(button.dataset.key, true, button); }, 700);
  });
  for (const name of ["pointerup", "pointerleave", "pointercancel"]) holder.addEventListener(name, () => window.clearTimeout(remote.hold));
  holder.addEventListener("click", (event) => {
    const button = event.target.closest(".rk");
    if (!button) return;
    if (remote.holdFired) { remote.holdFired = false; return; }
    sendRemoteKey(button.dataset.key, event.shiftKey, button);
  });
  holder.addEventListener("contextmenu", (event) => { if (event.target.closest(".rk")) event.preventDefault(); }); // touch long press
  $("#remotePower").addEventListener("click", (event) => {
    if (event.shiftKey) { sendRemoteKey("power", true, event.currentTarget); return; }
    openPowerDialog();
  });
  $("#remoteMessage").addEventListener("click", async () => {
    const text = await ask({ title: "Message on the TV", message: "The text is shown on the TV connected to the receiver for 10 seconds.", label: "Message", ok: "Send",
      validate: (value) => (value.length > 300 ? "Use at most 300 characters." : "") });
    if (!text) return;
    try { await post(`/api/v1/profiles/${encodeURIComponent(screenView.profileID)}/message`, { text, seconds: 10 }); toast("Message sent to the TV"); }
    catch (error) { remoteError(error.message); }
  });
  $("#remoteText").addEventListener("click", async () => {
    const text = await ask({ title: "Type on the receiver", message: "Open a search box or keyboard on the receiver first. Plain letters, digits and symbols only.", label: "Text", ok: "Type",
      validate: (value) => (!value ? "Type something." : value.length > 200 ? "Use at most 200 characters." : /[^\x20-\x7e]/.test(value) ? "Only plain letters, digits and symbols can be typed on the receiver." : "") });
    if (!text) return;
    try { await post(`/api/v1/profiles/${encodeURIComponent(screenView.profileID)}/remote-text`, { text }); toast("Text sent to the receiver"); }
    catch (error) { remoteError(error.message); }
  });
  $("#remoteHide").addEventListener("click", () => setRemoteOpen(false));
  $("#remoteShow").addEventListener("click", () => setRemoteOpen(true));
  $("#remoteRefresh").addEventListener("change", () => store.set("remote.refresh", $("#remoteRefresh").checked));
}
function setRemoteOpen(open) {
  $("#remotePad").hidden = !open;
  $("#remoteShow").hidden = open;
  store.set("remote.open", open);
}
buildRemote();
remoteBind();
