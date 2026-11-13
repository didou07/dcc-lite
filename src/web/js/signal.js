"use strict";
const SIG_WINDOW_MS = 120000;
const sig = { profileID: "", running: false, gen: 0, paused: false, controller: null, wake: null, history: [], best: null, worst: null, failures: 0, polls: 0, last: null, audio: null, sound: false, beepTimer: 0 };
const sigSleep = (ms) => new Promise((resolve) => { const timer = window.setTimeout(resolve, ms); sig.wake = () => { window.clearTimeout(timer); resolve(); }; });
const sigGoodFrom = () => Number($("#sigGood").value) || 60;
const sigLevel = (snr) => (snr >= sigGoodFrom() ? "ok" : snr >= sigGoodFrom() - 25 ? "mid" : "low");

function sigShowError(message) {
  const node = $("#sigError");
  node.hidden = !message;
  node.textContent = message || "";
}

const SIG_SEGMENTS = 50;
function sigBuildBar() {
  const bar = $("#sigBar");
  if (bar.querySelector("span")) return;
  const frag = document.createDocumentFragment();
  for (let i = 0; i < SIG_SEGMENTS; i++) frag.append(document.createElement("span"));
  bar.prepend(frag);
}
function sigPlaceThreshold() {
  const good = sigGoodFrom(), low = Math.max(0, good - 25);
  $("#sigBar").querySelectorAll("span").forEach((segment, i) => {
    const value = (i + 0.5) * (100 / SIG_SEGMENTS);
    segment.dataset.z = value < low ? "bad" : value < good ? "warn" : "ok";
  });
  $("#sigThr").style.left = `${good}%`;
}
function sigSetBar(value) {
  const lit = Math.round((value / 100) * SIG_SEGMENTS);
  $("#sigBar").querySelectorAll("span").forEach((segment, i) => segment.classList.toggle("on", i < lit));
}
function sigSetLive(state, text) {
  const node = $("#sigLive");
  if (!node) return;
  node.dataset.state = state;
  $("span", node).textContent = text;
  node.title = text;
}

function sigRender(reading) {
  sig.last = reading;
  const main = $("#sigMain");
  const state = $("#sigState span");
  const arc = (value) => sigSetBar(value);
  if (!reading || !reading.available) {
    main.dataset.level = "none";
    $("#sigValue").textContent = "—";
    state.textContent = reading ? "No reading — receiver in standby, or the channel has no tuner" : "Waiting for the first reading…";
    for (const id of ["#sigDb", "#sigAgc", "#sigBer", "#sigTuner"]) $(id).textContent = "—";
    $("#sigAgcBar").style.width = "0%";
    arc(0);
    sigRenderTrend(null);
    $("#sigMeter").setAttribute("aria-valuenow", "0");
    return;
  }
  const level = reading.locked ? sigLevel(reading.snr) : "none";
  main.dataset.level = level;
  $("#sigValue").textContent = String(reading.snr);
  state.textContent = !reading.locked ? (reading.standby ? "Receiver in standby" : "No lock — keep turning slowly")
    : level === "ok" ? "Good signal" : level === "mid" ? "Weak signal — fine-tune" : "Very weak signal";
  arc(reading.snr);
  sigRenderTrend(reading.locked ? reading.snr : null);
  $("#sigMeter").setAttribute("aria-valuenow", String(reading.snr));
  $("#sigDb").textContent = reading.has_db ? `${reading.snr_db.toFixed(1)} dB` : `${reading.snr} %`;
  $("#sigAgc").textContent = reading.has_agc ? `${reading.agc} %` : "—";
  $("#sigAgcBar").style.width = reading.has_agc ? `${reading.agc}%` : "0%";
  $("#sigBer").textContent = reading.has_ber ? String(reading.ber) : "—";
  $("#sigTuner").textContent = reading.tuner || "—";
  if (reading.channel) $("#sigChannel").textContent = reading.channel;
}

function sigRenderTrend(snr) {
  const node = $("#sigTrend"), text = $("#sigTrendText");
  if (snr === null) { node.dataset.dir = "none"; text.textContent = ""; return; }
  const now = performance.now();
  const older = sig.history.filter((p) => p.snr !== null && now - p.t >= 1500 && now - p.t <= 4500);
  let dir = "flat", label = "";
  if (older.length) {
    const delta = snr - older.reduce((sum, p) => sum + p.snr, 0) / older.length;
    if (delta >= 1.5) { dir = "up"; label = `▲ +${Math.round(delta)}`; }
    else if (delta <= -1.5) { dir = "down"; label = `▼ ${Math.round(delta)}`; }
    else label = "● steady";
  }
  if (sig.best !== null && snr < sig.best) label += `${label ? " · " : ""}${sig.best - snr} below best`;
  else if (sig.best !== null && snr >= sig.best && sig.history.length > 3) label += `${label ? " · " : ""}best so far`;
  node.dataset.dir = dir;
  text.textContent = label;
}

function sigRenderPeaks() {
  const fmt = (v) => (v === null ? "—" : `${v} %`);
  $("#sigBest").textContent = fmt(sig.best);
  $("#sigWorst").textContent = fmt(sig.worst);
  const mark = $("#sigPeakMark");
  mark.style.display = sig.best === null ? "none" : "";
  if (sig.best !== null) mark.style.left = `${sig.best}%`;
}

function sigDraw() {
  const canvas = $("#sigCanvas");
  const ratio = window.devicePixelRatio || 1;
  const width = Math.round(canvas.clientWidth * ratio), height = Math.round(canvas.clientHeight * ratio);
  if (width < 40 || height < 40) return;
  if (canvas.width !== width) canvas.width = width;
  if (canvas.height !== height) canvas.height = height;
  const ctx = canvas.getContext("2d");
  const css = getComputedStyle(document.documentElement);
  const color = (name) => css.getPropertyValue(name).trim() || "#888";
  const mix = (name, alpha) => { ctx.save(); ctx.globalAlpha = alpha; ctx.fillStyle = color(name); return ctx; };
  ctx.clearRect(0, 0, width, height);
  const left = 30 * ratio, top = 8 * ratio, bottom = 22 * ratio, right = 8 * ratio;
  const w = width - left - right, h = height - top - bottom;
  const y = (value) => top + (1 - value / 100) * h;
  const good = sigGoodFrom();
  for (const [from, to, name] of [[good, 100, "--ok"], [Math.max(0, good - 25), good, "--warn"], [0, Math.max(0, good - 25), "--bad"]]) {
    const c = mix(name, 0.09); c.fillRect(left, y(to), w, y(from) - y(to)); c.restore();
  }
  ctx.font = `${11 * ratio}px system-ui, sans-serif`;
  ctx.textBaseline = "middle";
  ctx.lineWidth = Math.max(1, ratio);
  for (const line of [0, 25, 50, 75, 100]) {
    ctx.strokeStyle = color("--border"); ctx.beginPath(); ctx.moveTo(left, y(line)); ctx.lineTo(left + w, y(line)); ctx.stroke();
    ctx.fillStyle = color("--faint"); ctx.textAlign = "right"; ctx.fillText(String(line), left - 6 * ratio, y(line));
  }
  ctx.textAlign = "center"; ctx.textBaseline = "alphabetic";
  const now = performance.now();
  const x = (t) => left + (1 - (now - t) / SIG_WINDOW_MS) * w;
  for (const [label, at] of [["2:00", 0], ["1:30", 0.25], ["1:00", 0.5], ["0:30", 0.75], ["now", 1]]) {
    ctx.fillStyle = color("--faint"); ctx.fillText(label, Math.min(left + w - 10 * ratio, Math.max(left + 8 * ratio, left + at * w)), height - 5 * ratio);
  }
  if (sig.best !== null) {
    ctx.save(); ctx.setLineDash([5 * ratio, 4 * ratio]); ctx.strokeStyle = color("--ok"); ctx.lineWidth = Math.max(1, ratio);
    ctx.beginPath(); ctx.moveTo(left, y(sig.best)); ctx.lineTo(left + w, y(sig.best)); ctx.stroke(); ctx.restore();
  }
  const trace = (key) => {
    const runs = []; let run = [];
    for (const point of sig.history) {
      if (point[key] === null) { if (run.length) runs.push(run); run = []; } else run.push([x(point.t), y(point[key])]);
    }
    if (run.length) runs.push(run);
    return runs;
  };
  const stroke = (runs, strokeColor, lineWidth) => {
    ctx.strokeStyle = strokeColor; ctx.lineWidth = lineWidth * ratio; ctx.lineJoin = "round"; ctx.lineCap = "round";
    for (const r of runs) { ctx.beginPath(); r.forEach(([px, py], i) => (i ? ctx.lineTo(px, py) : ctx.moveTo(px, py))); ctx.stroke(); }
  };
  stroke(trace("agc"), color("--faint"), 1.4);
  const snrRuns = trace("snr");
  const withAlpha = (name, alpha) => {
    ctx.fillStyle = "#888888"; ctx.fillStyle = color(name);
    const v = String(ctx.fillStyle), m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(v);
    return m ? `rgba(${parseInt(m[1], 16)},${parseInt(m[2], 16)},${parseInt(m[3], 16)},${alpha})` : v;
  };
  const gradient = ctx.createLinearGradient(0, top, 0, top + h);
  gradient.addColorStop(0, withAlpha("--accent", 0.33)); gradient.addColorStop(1, withAlpha("--accent", 0));
  {
    ctx.fillStyle = gradient;
    for (const r of snrRuns) {
      if (r.length < 2) continue;
      ctx.beginPath(); ctx.moveTo(r[0][0], top + h); r.forEach(([px, py]) => ctx.lineTo(px, py)); ctx.lineTo(r[r.length - 1][0], top + h); ctx.closePath(); ctx.fill();
    }
  }
  stroke(snrRuns, color("--accent"), 2.6);
  const lastRun = snrRuns[snrRuns.length - 1];
  const latest = sig.history[sig.history.length - 1];
  if (lastRun && latest && latest.snr !== null) {
    const [px, py] = lastRun[lastRun.length - 1];
    ctx.fillStyle = color("--accent"); ctx.beginPath(); ctx.arc(px, py, 4.5 * ratio, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = color("--surface"); ctx.lineWidth = 2 * ratio; ctx.stroke();
  }
}

function sigPush(reading) {
  const now = performance.now();
  const point = { t: now, snr: reading?.locked ? reading.snr : null, agc: reading?.available && reading.has_agc ? reading.agc : null };
  sig.history.push(point);
  while (sig.history.length && now - sig.history[0].t > SIG_WINDOW_MS) sig.history.shift();
  if (reading?.locked) {
    sig.best = sig.best === null ? reading.snr : Math.max(sig.best, reading.snr);
    sig.worst = sig.worst === null ? reading.snr : Math.min(sig.worst, reading.snr);
  }
}

function sigBeep() {
  window.clearTimeout(sig.beepTimer);
  if (!sig.sound || !sig.audio) return;
  const r = sig.last;
  const locked = Boolean(r?.available && r.locked);
  const snr = locked ? r.snr : 0;
  const ctx = sig.audio;
  const gain = ctx.createGain();
  const osc = ctx.createOscillator();
  const length = locked ? 0.09 : 0.25;
  osc.type = "sine";
  osc.frequency.value = locked ? 300 + snr * 10 : 140;
  gain.gain.setValueAtTime(0.0001, ctx.currentTime);
  gain.gain.exponentialRampToValueAtTime(0.25, ctx.currentTime + 0.01);
  gain.gain.exponentialRampToValueAtTime(0.0001, ctx.currentTime + length);
  osc.connect(gain).connect(ctx.destination);
  osc.start();
  osc.stop(ctx.currentTime + length + 0.02);
  const gap = locked ? 900 - snr * 8 : 700;
  sig.beepTimer = window.setTimeout(sigBeep, Math.max(100, gap));
}
function sigSetSound(on) {
  sig.sound = on;
  $("#sigSound").setAttribute("aria-pressed", String(on));
  $("#sigSound span").textContent = on ? "Sound: on" : "Sound: off";
  $("#sigSound").classList.toggle("primary", on);
  window.clearTimeout(sig.beepTimer);
  if (on) {
    try {
      sig.audio = sig.audio || new (window.AudioContext || window.webkitAudioContext)();
      sig.audio.resume?.();
      sigBeep();
    } catch { sig.sound = false; $("#sigSound").setAttribute("aria-pressed", "false"); $("#sigSound span").textContent = "Sound: off"; $("#sigSound").classList.remove("primary"); toast("This browser cannot play sound", "error"); }
  }
}

async function sigLoop(gen) {
  while (gen === sig.gen) {
    if (sig.paused || document.hidden) { await sigSleep(400); continue; }
    const began = performance.now();
    const controller = new AbortController();
    sig.controller = controller;
    let delay = Number($("#sigSpeed").value) || 700;
    try {
      const withChannel = sig.polls % 20 === 0 ? "?channel=1" : "";
      sig.polls += 1;
      const response = await fetchChecked(profileURL(sig.profileID, `signal${withChannel}`), { signal: controller.signal, cache: "no-store" });
      const reading = await response.json();
      if (gen !== sig.gen) return;
      sig.failures = 0;
      sigShowError("");
      sigSetLive("live", "Live");
      sigPush(reading);
      sigRender(reading);
      sigRenderPeaks();
      sigDraw();
    } catch (error) {
      if (gen !== sig.gen) return;
      if (error.name === "AbortError") continue;
      if (error.auth) return;
      sig.failures += 1;
      sigShowError(error.message);
      sigSetLive("error", "No link");
      if (error.fatal) { sig.running = false; return; }
      delay = Math.min(20000, 2000 * sig.failures);
    }
    await sigSleep(Math.max(100, delay - (performance.now() - began)));
  }
}

function sigSetPauseLabel(paused) {
  $("#sigPause span").textContent = paused ? "Resume" : "Pause";
  $("#sigPause use").setAttribute("href", paused ? "#i-play" : "#i-pause");
}
function sigResetPeaks() {
  sig.best = null; sig.worst = null;
  if (sig.last?.locked) { sig.best = sig.worst = sig.last.snr; }
  sigRenderPeaks();
}
function signalStop() {
  sig.running = false;
  sig.gen += 1;
  sig.controller?.abort();
  sig.wake?.();
  window.clearTimeout(sig.beepTimer);
  sigSetSound(false);
  sigSetLive("wait", "Idle");
  sigWake(false);
  if (document.fullscreenElement === $("#sigBody")) document.exitFullscreen?.().catch(() => {});
}
let sigLock = null;
async function sigWake(on) {
  try {
    if (on && navigator.wakeLock && !sigLock) {
      sigLock = await navigator.wakeLock.request("screen");
      sigLock.addEventListener("release", () => { sigLock = null; });
    } else if (!on && sigLock) { const lock = sigLock; sigLock = null; await lock.release(); }
  } catch { }
}
function signalEnter() {
  const device = currentDevice();
  if (!device) {
    signalStop();
    sig.profileID = "";
    $("#sigSub").textContent = "No device selected";
    sigRender(null);
    sigShowError("Choose a device in the list at the top.");
    return;
  }
  if (device.id === sig.profileID && sig.running) return;
  signalStop();
  Object.assign(sig, { profileID: device.id, paused: false, history: [], best: null, worst: null, failures: 0, polls: 0, last: null, running: true });
  $("#sigSub").textContent = `${device.name} · ${device.host}`;
  for (const [key, select, fallback] of [["signal.speed", "#sigSpeed", "700"], ["signal.good", "#sigGood", "60"]]) {
    const saved = String(store.get(key, fallback));
    $(select).value = [...$(select).options].some((o) => o.value === saved) ? saved : fallback;
  }
  $("#sigChannel").textContent = "";
  sigSetPauseLabel(false);
  sigShowError("");
  sigBuildBar();
  sigPlaceThreshold();
  sigSetLive("wait", "Waiting");
  sigRender(null);
  sigRenderPeaks();
  window.requestAnimationFrame(sigDraw);
  sigWake(true);
  sigLoop(sig.gen);
}

for (const [select, key] of [["#sigSpeed", "signal.speed"], ["#sigGood", "signal.good"]]) {
  $(select).addEventListener("change", () => { store.set(key, $(select).value); if (select === "#sigGood") { sigPlaceThreshold(); if (sig.last) sigRender(sig.last); } sigDraw(); sig.wake?.(); });
}
$("#sigPause").addEventListener("click", () => {
  sig.paused = !sig.paused;
  sigSetPauseLabel(sig.paused);
  sigSetLive(sig.paused ? "paused" : "wait", sig.paused ? "Paused" : "Waiting");
  if (sig.paused) window.clearTimeout(sig.beepTimer); else { sig.wake?.(); sigBeep(); }
});
$("#sigPeakReset").addEventListener("click", sigResetPeaks);
$("#sigSound").addEventListener("click", () => sigSetSound(!sig.sound));
window.addEventListener("resize", () => { if (view.current === "signal") sigDraw(); });
document.addEventListener("visibilitychange", () => { if (!document.hidden && view.current === "signal") { sig.wake?.(); sigWake(true); } });
document.addEventListener("fullscreenchange", () => { if (view.current === "signal") window.requestAnimationFrame(sigDraw); });
$("#sigFull").addEventListener("click", () => {
  const body = $("#sigBody");
  if (document.fullscreenElement === body) document.exitFullscreen?.(); else body.requestFullscreen?.().catch(() => toast("Full screen is not available here", "error"));
});
new ResizeObserver(() => { if (view.current === "signal") sigDraw(); }).observe($("#sigCanvas"));
