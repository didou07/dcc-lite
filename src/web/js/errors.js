"use strict";
(function reportBrowserErrors() {
  const recent = new Map();
  function send(payload) {
    const key = `${payload.message}|${payload.source}|${payload.line}`;
    const now = Date.now();
    if (now - (recent.get(key) || 0) < 10000) return;
    recent.set(key, now);
    if (recent.size > 50) recent.clear();
    try {
      fetch("/api/v1/logs/client", {
        method: "POST", keepalive: true,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...payload, view: document.body?.dataset?.view || "" }),
      }).catch(() => {});
    } catch { }
  }
  window.addEventListener("error", (event) => {
    if (event.target && event.target !== window) return;
    send({
      message: event.message || "Script error", source: event.filename || "",
      line: event.lineno || 0, column: event.colno || 0, stack: event.error?.stack || "",
    });
  });
  window.addEventListener("unhandledrejection", (event) => {
    const reason = event.reason;
    send({ message: `Unhandled promise rejection: ${reason?.message || String(reason)}`, stack: reason?.stack || "" });
  });
})();
