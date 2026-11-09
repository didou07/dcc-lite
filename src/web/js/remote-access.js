"use strict";
function raShowError(message) {
  const node = $("#raError");
  node.hidden = !message;
  node.textContent = message || "";
}
function raShowSecret(password) {
  $("#raPass").value = password || "";
}
function raRender(status) {
  if (!status) return;
  const managed = status.managed !== false;
  $("#raEnabled").checked = Boolean(status.enabled);
  $("#raListen").value = status.listen || "0.0.0.0:8788";
  $("#raHosts").value = status.tls_hosts || "";
  $("#raMode").value = status.plain_http ? "http" : "https";
  raModeChanged();
  for (const id of ["#raEnabled", "#raListen", "#raMode", "#raHosts", "#raSave"]) $(id).disabled = !managed;
  const state = $("#raState");
  state.dataset.state = status.running ? "on" : "off";
  state.textContent = !managed ? "Started from the command line (--remote-listen). Restart without it to manage this here."
    : status.running ? `On · ${status.plain_http ? "HTTP" : "HTTPS"} · ${status.listen}` : "Off";
  $("#raInfo").hidden = !status.running;
  $("#raCert").hidden = Boolean(status.plain_http);
  const list = $("#raUrls");
  list.replaceChildren(...(status.urls || []).map((u) => h("li", {}, h("code", { text: u }),
    h("button", { class: "icon-btn", type: "button", title: "Copy link", "aria-label": "Copy link", onClick: async () => { try { await navigator.clipboard.writeText(u); toast("Link copied"); } catch { toast("Press Ctrl+C to copy", "error"); } } }, icon("copy")))));
  $("#raFingerprint").textContent = status.fingerprint || "";
  $("#raPass").placeholder = status.password_set ? "Password is set. Type a new one (10+ characters)" : "Password (10+ characters)";
  raShowError(status.error || "");
}
function raModeChanged() {
  $("#raPlainWarn").hidden = $("#raMode").value !== "http";
}
$("#raMode").addEventListener("change", raModeChanged);
async function raLoad() {
  raShowSecret("");
  raShowError("");
  const data = await api("/api/v1/remote-access");
  raRender(data.status);
}
$("#remoteAccessBtn").addEventListener("click", async () => {
  try { await raLoad(); openDialog($("#raDialog")); } catch (error) { toastError(error); }
});
$("#raClose").addEventListener("click", () => { raShowSecret(""); $("#raDialog").close(); });
$("#raForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  raShowError("");
  $("#raSave").disabled = true;
  try {
    const plain = $("#raMode").value === "http";
    const body = { enabled: $("#raEnabled").checked, listen: $("#raListen").value.trim(), tls_hosts: $("#raHosts").value.trim(), plain_http: plain };
    if (plain && body.enabled && !(await ask({ title: "Use HTTP without encryption?", input: false, ok: "Use HTTP", danger: true,
      message: "The password and everything you do remotely can be read on the network. Continue only if you accept this and use a long random password." }))) { return; }
    const data = await api("/api/v1/remote-access", { method: "PUT", body: JSON.stringify(body) });
    raRender(data.status);
    raShowSecret(data.new_password || "");
    toast(data.status.running ? "Remote access is on" : "Remote access is off");
  } catch (error) { raShowError(error.message); }
  finally { $("#raSave").disabled = false; }
});
$("#raPassSet").addEventListener("click", async () => {
  raShowError("");
  try {
    const data = await post("/api/v1/remote-access/password", { password: $("#raPass").value });
    raRender(data.status);
    toast("Password saved; signed-in remote sessions were ended");
  } catch (error) { raShowError(error.message); }
});
$("#raPassGen").addEventListener("click", async () => {
  raShowError("");
  try {
    const data = await post("/api/v1/remote-access/password", { generate: true });
    raRender(data.status);
    raShowSecret(data.new_password);
  } catch (error) { raShowError(error.message); }
});
$("#raSecretCopy").addEventListener("click", async () => {
  try { await navigator.clipboard.writeText($("#raPass").value); toast("Password copied"); }
  catch { $("#raPass").select(); toast("Press Ctrl+C to copy", "error"); }
});
