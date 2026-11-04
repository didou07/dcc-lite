"use strict";
/* DCC Lite — remote access sign-in page. */
(() => {
  try {
    const theme = JSON.parse((localStorage.getItem("dcclite.theme") ?? localStorage.getItem("boxdeck.theme") ?? localStorage.getItem("dccnext.theme")) || "\"auto\"");
    if (["auto", "light", "dark"].includes(theme)) document.documentElement.dataset.theme = theme;
  } catch { /* storage unavailable: keep automatic theme */ }

  const form = document.getElementById("loginForm");
  const password = document.getElementById("loginPassword");
  const error = document.getElementById("loginError");
  const submit = document.getElementById("loginSubmit");

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.hidden = true;
    submit.disabled = true;
    try {
      const response = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: password.value }),
      });
      const data = await response.json().catch(() => ({}));
      if (response.ok) { window.location.replace("/"); return; }
      error.textContent = data.error || `Sign-in failed (${response.status})`;
      error.hidden = false;
      password.select();
    } catch {
      error.textContent = "Could not reach the server.";
      error.hidden = false;
    } finally {
      submit.disabled = false;
    }
  });
})();
