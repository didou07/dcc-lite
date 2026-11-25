"use strict";

const liveOsd = {
  on: store.get("live.osd", true) !== false,
  gen: 0, wake: null, last: 0,
  hash: "", shown: false, beginN: -1, sig: "", sigAt: 0, probeKey: "", burstUntil: 0,
  sawAlpha: false, opaqueRun: 0, failures: 0,
  blocked: "",
};
const liveOsdCanvas = document.createElement("canvas");
const liveOsdProbe = document.createElement("canvas");

const LIVE_OSD_MESSAGES = {
  noalpha: "This receiver's OSD picture has no transparency, so it cannot be laid over the video. The OSD button is off.",
  novlc: "This VLC cannot draw the OSD over the video (its logo filter is missing). The OSD button is off.",
  failed: "The receiver did not return its OSD picture. Press OSD to try again.",
};

const liveOsdSleep = (ms) => new Promise((resolve) => {
  const timer = window.setTimeout(resolve, ms);
  liveOsd.wake = () => { window.clearTimeout(timer); resolve(); };
});

function liveOsdActive() {
  return !!(liveNative && live.on && liveOsd.on && !liveOsd.blocked && live.hasPicture && live.video && live.video.w > 0 && live.video.h > 0
    && (live.state === "playing" || live.state === "paused") && !live.popup && !document.hidden
    && view.current === "screen" && screenView.profileID && !document.querySelector("dialog[open]"));
}

function liveOsdNow() {
  if (!liveOsdActive()) return false;
  liveOsd.burstUntil = performance.now() + 8000;
  liveOsd.wake?.();
  return true;
}

function liveOsdSync() {
  const button = $("#liveOsdBtn");
  if (!button) return;
  button.hidden = !liveNative;
  button.setAttribute("aria-pressed", String(liveOsd.on && !liveOsd.blocked));
  button.title = liveOsd.blocked ? (LIVE_OSD_MESSAGES[liveOsd.blocked] || "The OSD overlay is off.")
    : "Show the receiver menus (OSD) over the video";
}

function liveOsdClear() {
  liveOsd.hash = "";
  if (!liveOsd.shown) return;
  liveOsd.shown = false;
  liveCall("overlay", { clear: true }).catch(() => { });
}

function liveOsdBlock(reason) {
  if (liveOsd.blocked) return;
  liveOsd.blocked = reason;
  liveOsdClear();
  liveOsdSync();
  toast(LIVE_OSD_MESSAGES[reason] || "The OSD overlay is off.", "error");
}

async function liveOsdDigest(blob) {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  const stride = Math.max(1, bytes.length >> 17);
  let h = 2166136261 >>> 0;
  for (let i = 0; i < bytes.length; i += stride) h = Math.imul(h ^ bytes[i], 16777619) >>> 0;
  return `${bytes.length}.${h.toString(16)}`;
}

const liveOsdBase64 = (blob) => new Promise((resolve, reject) => {
  const reader = new FileReader();
  reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
  reader.onerror = () => reject(new Error("The OSD picture could not be read."));
  reader.readAsDataURL(blob);
});
const liveOsdPng = (canvas) => new Promise((resolve, reject) => canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error("The OSD picture could not be made."))), "image/png"));

const liveOsdStepCanvas = document.createElement("canvas");
function liveOsdDraw(ctx, img, w, h) {
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = "high";
  let src = img, sw = img.naturalWidth, sh = img.naturalHeight;
  while (sw >= w * 2 && sh >= h * 2) {
    const nw = Math.max(w, Math.floor(sw / 2)), nh = Math.max(h, Math.floor(sh / 2));
    liveOsdStepCanvas.width = nw; liveOsdStepCanvas.height = nh;
    const c = liveOsdStepCanvas.getContext("2d");
    c.clearRect(0, 0, nw, nh);
    c.imageSmoothingQuality = "high";
    c.drawImage(src, 0, 0, nw, nh);
    const copy = document.createElement("canvas");
    copy.width = nw; copy.height = nh;
    copy.getContext("2d").drawImage(liveOsdStepCanvas, 0, 0);
    src = copy; sw = nw; sh = nh;
  }
  ctx.drawImage(src, 0, 0, w, h);
}

function liveOsdSharpen(ctx, w, h) {
  const img = ctx.getImageData(0, 0, w, h), d = img.data, n = w * h;
  const pre = new Float32Array(n * 4);
  for (let i = 0; i < n; i++) {
    const a = d[i * 4 + 3] / 255;
    pre[i * 4] = d[i * 4] * a; pre[i * 4 + 1] = d[i * 4 + 1] * a; pre[i * 4 + 2] = d[i * 4 + 2] * a; pre[i * 4 + 3] = d[i * 4 + 3];
  }
  const amount = 0.7;
  for (let y = 0; y < h; y++) {
    const y0 = Math.max(0, y - 1), y1 = Math.min(h - 1, y + 1);
    for (let x = 0; x < w; x++) {
      const x0 = Math.max(0, x - 1), x1 = Math.min(w - 1, x + 1), o = (y * w + x) * 4;
      let alpha = 0;
      const out = [0, 0, 0, 0];
      for (let c = 0; c < 4; c++) {
        const blur = (pre[(y0 * w + x0) * 4 + c] + pre[(y0 * w + x) * 4 + c] + pre[(y0 * w + x1) * 4 + c]
          + pre[(y * w + x0) * 4 + c] + pre[o + c] + pre[(y * w + x1) * 4 + c]
          + pre[(y1 * w + x0) * 4 + c] + pre[(y1 * w + x) * 4 + c] + pre[(y1 * w + x1) * 4 + c]) / 9;
        out[c] = Math.max(0, Math.min(255, pre[o + c] + amount * (pre[o + c] - blur)));
      }
      alpha = out[3];
      if (alpha < 1) { d[o] = d[o + 1] = d[o + 2] = d[o + 3] = 0; continue; }
      const k = 255 / alpha;
      d[o] = Math.min(255, out[0] * k); d[o + 1] = Math.min(255, out[1] * k); d[o + 2] = Math.min(255, out[2] * k); d[o + 3] = alpha;
    }
  }
  ctx.putImageData(img, 0, 0);
}

async function liveOsdStep(gen) {
  const id = screenView.profileID;
  const begun = live.beginN, vw = live.video.w, vh = live.video.h;
  const stale = () => gen !== liveOsd.gen || begun !== live.beginN || !liveOsdActive() || live.video.w !== vw || live.video.h !== vh;
  const quiet = !liveOsd.shown && performance.now() > liveOsd.burstUntil;
  const want = quiet ? 640 : (vw > 1280 ? 1920 : 1280);
  const params = new URLSearchParams({ mode: "osd", width: String(want) });
  const response = await fetchChecked(profileURL(id, `screen?${params}`), { cache: "no-store" });
  const blob = await response.blob();
  if (id !== screenView.profileID || stale()) return;
  const key = `${await liveOsdDigest(blob)}:${vw}x${vh}`;
  if (quiet ? key === liveOsd.probeKey : key === liveOsd.hash) return; // nothing changed

  const url = URL.createObjectURL(blob);
  try {
    const img = new Image();
    img.src = url;
    try { await img.decode(); } catch { throw new Error("The receiver returned an invalid OSD picture."); }
    if (stale()) return;

    const pw = 192, ph = 108;
    liveOsdProbe.width = pw; liveOsdProbe.height = ph;
    const pctx = liveOsdProbe.getContext("2d", { willReadFrequently: true });
    pctx.clearRect(0, 0, pw, ph);
    pctx.drawImage(img, 0, 0, pw, ph);
    const px = pctx.getImageData(0, 0, pw, ph).data;
    let visible = 0, opaque = 0;
    for (let i = 3; i < px.length; i += 4) { if (px[i] > 6) visible++; if (px[i] >= 250) opaque++; }
    const total = pw * ph;
    if (total - opaque > total * 0.015) { liveOsd.sawAlpha = true; liveOsd.opaqueRun = 0; }
    else if (!liveOsd.sawAlpha) {
      if (++liveOsd.opaqueRun >= 3) liveOsdBlock("noalpha");
      return;
    }

    if (quiet) {
      liveOsd.probeKey = key;
      if (visible) liveOsd.burstUntil = performance.now() + 8000;
      return;
    }

    if (!visible) { // no OSD on the receiver now
      liveOsd.probeKey = "";
      if (liveOsd.shown) { liveOsd.shown = false; await liveCall("overlay", { clear: true }); }
      liveOsd.hash = key;
      return;
    }

    liveOsdCanvas.width = vw; liveOsdCanvas.height = vh;
    const ctx = liveOsdCanvas.getContext("2d");
    ctx.clearRect(0, 0, vw, vh);
    liveOsdDraw(ctx, img, vw, vh);
    if (vh <= 576 && img.naturalWidth > vw) liveOsdSharpen(ctx, vw, vh);
    const png = await liveOsdBase64(await liveOsdPng(liveOsdCanvas));
    if (stale()) return;
    const result = await liveCall("overlay", { png });
    if (result.unsupported) { liveOsdBlock("novlc"); return; }
    liveOsd.hash = key;
    liveOsd.shown = true;
  } finally { URL.revokeObjectURL(url); }
}

async function liveOsdLoop(gen) {
  while (gen === liveOsd.gen) {
    if (!live.on) {
      if (liveOsd.shown) liveOsdClear();
      liveOsd.hash = ""; liveOsd.failures = 0; liveOsd.opaqueRun = 0;
    }
    if (!liveOsdActive()) {
      if (live.on && !live.hasPicture) { liveOsdClear(); liveOsd.sig = ""; }
      await liveOsdSleep(500);
      continue;
    }
    if (liveOsd.beginN !== live.beginN) { liveOsd.beginN = live.beginN; liveOsd.hash = ""; liveOsd.probeKey = ""; liveOsd.shown = false; liveOsd.burstUntil = performance.now() + 8000; } // a new pipeline lost the overlay
    const sig = `${live.beginN}:${live.video.w}x${live.video.h}`;
    if (sig !== liveOsd.sig) { liveOsd.sig = sig; liveOsd.sigAt = performance.now(); liveOsdClear(); }
    if (performance.now() - liveOsd.sigAt < 900) { await liveOsdSleep(150); continue; } // let the new picture settle
    const began = performance.now();
    if (began - liveOsd.last < 350) { await liveOsdSleep(350 - (began - liveOsd.last)); continue; }
    liveOsd.last = began;
    let delay = 450;
    try {
      await liveOsdStep(gen);
      liveOsd.failures = 0;
      delay = !liveOsd.shown && performance.now() > liveOsd.burstUntil ? 1500 : 450;
    } catch (error) {
      if (gen !== liveOsd.gen) return;
      if (error.name === "AbortError") continue;
      liveOsd.failures += 1;
      if (error.auth || error.fatal || liveOsd.failures >= 6) { liveOsdBlock("failed"); continue; }
      delay = Math.min(15000, 1500 * 2 ** Math.min(liveOsd.failures, 4));
    }
    await liveOsdSleep(Math.max(150, delay - (performance.now() - began)));
  }
}

function liveOsdCtxItems(then) {
  return [livePopAction(liveOsd.on && !liveOsd.blocked ? "Hide the receiver menus over the video" : "Show the receiver menus over the video", then(liveOsdToggle))];
}

function liveOsdToggle() {
  liveOsd.on = !liveOsd.on || !!liveOsd.blocked;
  liveOsd.blocked = "";
  liveOsd.failures = 0;
  liveOsd.opaqueRun = 0;
  store.set("live.osd", liveOsd.on);
  liveOsdSync();
  if (liveOsd.on) liveOsdNow(); else liveOsdClear();
}

if (liveNative) {
  $("#liveOsdBtn").addEventListener("click", liveOsdToggle);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) liveOsdNow(); });
  liveOsdSync();
  liveOsdLoop(liveOsd.gen);
} else {
  const button = $("#liveOsdBtn");
  if (button) button.hidden = true;
}

if (window.DCC_I18N?.add) {
  DCC_I18N.add("ar", {
    "OSD": "OSD",
    "Receiver menus over the video": "قوائم الرسيفر فوق الفيديو",
    "Show the receiver menus (OSD) over the video": "عرض قوائم الرسيفر (OSD) فوق الفيديو",
    "Hide the receiver menus over the video": "إخفاء قوائم الرسيفر فوق الفيديو",
    "Show the receiver menus over the video": "عرض قوائم الرسيفر فوق الفيديو",
    "This receiver's OSD picture has no transparency, so it cannot be laid over the video. The OSD button is off.": "صورة OSD من هذا الرسيفر بلا شفافية، فلا يمكن وضعها فوق الفيديو. زر OSD متوقف.",
    "This VLC cannot draw the OSD over the video (its logo filter is missing). The OSD button is off.": "لا يستطيع VLC هذا رسم الـ OSD فوق الفيديو (فلتر الشعار غير موجود). زر OSD متوقف.",
    "The receiver did not return its OSD picture. Press OSD to try again.": "لم يُرجع الرسيفر صورة الـ OSD. اضغط OSD للمحاولة مجدداً.",
    "The receiver returned an invalid OSD picture.": "أرجع الرسيفر صورة OSD غير صالحة.",
  });
}
