"use strict";

const dk = { profileID: "", gen: 0, busy: false, info: null, timer: 0, last: 0, formatAck: "", target: null, mountEdited: false };
const DK_POLL_MS = 15000;
const DK_FORMAT_POLL_MS = 3000;
const DK_SHARE_PANES = { cifs: "SMB version", nfs: "NFS version" };

const dkMountOK = (path) => /^\/(media|mnt)\/[A-Za-z0-9._-]{1,48}(\/[A-Za-z0-9._-]{1,48}){0,2}$/.test(path) && !path.includes("..");
const dkSize = (kb) => formatBytes((Number(kb) || 0) * 1024);
const dkPercent = (used, total) => (total > 0 ? Math.min(100, Math.round((used * 100) / total)) : 0);
const dkSlug = (text) => String(text || "").toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
const dkFill = (text, ...values) => values.reduce((out, value, index) => out.replace(`{${index + 1}}`, value), text);

function dkError(message) {
  const node = $("#dkError");
  node.hidden = !message;
  node.textContent = message || "";
}

function dkResult(message, bad = false) {
  const node = $("#dkResult");
  const parts = [].concat(message || []).filter(Boolean);
  node.hidden = !parts.length;
  node.replaceChildren(...parts.flatMap((part, index) => [index ? " " : "", h("span", { text: part })]).filter(Boolean));
  node.classList.toggle("is-error", Boolean(bad));
}

function dkStatus(text) {
  const spin = $("#dkSpin");
  spin.hidden = !text;
  $("#dkSpinText").textContent = text || "";
}

function dkSetBusy(busy, text = "") {
  dk.busy = busy;
  const root = $("#view-disks");
  root.classList.toggle("is-busy", busy);
  $("#dkReload").classList.toggle("busy", busy);
  $("#view-disks .sc-wait")?.classList.toggle("on", busy);
  syncToolsBusy();
  for (const button of $$("#view-disks button[data-dk]")) button.disabled = busy;
  dkStatus(busy ? text : dkFormatText());
}

const dkFormatting = () => dk.info?.format?.state === "running" ? dk.info.format.device : "";
const dkFormatText = () => (dkFormatting() ? dkFill("Formatting {1}… this can take several minutes.", dkFormatting()) : "");

function dkTag(text, kind = "") {
  return h("span", { class: `sc-tag ${kind}`.trim(), text });
}

function dkButton(iconName, text, title, action, extra = "") {
  return h("button", {
    class: `btn sm ${extra}`.trim(), type: "button", title, "aria-label": text ? undefined : title,
    dataset: { dk: "1" }, disabled: dk.busy, onClick: action,
  }, icon(iconName), text);
}

function dkBar(used, total) {
  const percent = dkPercent(used, total);
  return h("div", { class: `dk-bar${percent >= 90 ? " hot" : ""}`, role: "img", "aria-label": `${percent}%` }, h("i", { style: `width:${percent}%` }));
}

function dkMeter(used, total, text) {
  return h("div", { class: "dk-meter" }, dkBar(used, total), h("span", { class: "dk-usage", text }));
}

const dkUsageText = (used, avail, size) => dkFill("{1} used · {2} free of {3}", dkSize(used), dkSize(avail), dkSize(size));

function dkItem({ mounted, name, place, tags, actions, meter, size }) {
  return h("div", { class: "dk-item", dataset: { on: String(mounted) } },
    h("i", { class: "sc-dot", dataset: { on: String(mounted) }, "aria-hidden": "true" }),
    h("div", { class: "dk-item-main" },
      h("div", { class: "dk-item-name" }, h("b", { text: name }), ...tags),
      place ? h("div", { class: "dk-item-place muted small", text: place }) : null),
    size ? h("span", { class: "dk-usage dk-item-size", text: size }) : null,
    actions.length ? h("div", { class: "sc-actions" }, ...actions) : null,
    meter ? h("div", { class: "dk-item-meter" }, meter) : null);
}

function dkPartRow(part) {
  const mounted = Boolean(part.mount);
  const busyFormat = dkFormatting() === part.device;
  const tags = [];
  if (part.fs) tags.push(dkTag(part.fs));
  if (part.read_only) tags.push(dkTag("read-only", "warn"));
  if (part.swap) tags.push(dkTag("swap"));
  if (part.system) tags.push(dkTag("system", "warn"));

  const actions = [];
  if (busyFormat) {
    actions.push(dkTag("Formatting…", "active"));
  } else if (!part.swap) {
    if (!mounted) actions.push(dkButton("plug", "Mount", dkFill("Mount {1}", part.device), () => dkMount(part)));
    if (mounted && dkMountOK(part.mount)) actions.push(dkButton("plug-off", "Unmount", dkFill("Unmount {1}", part.mount), () => dkUnmount(part.mount)));
    if (!mounted && !part.system) actions.push(dkButton("eraser", "Format", dkFill("Format {1}", part.device), () => dkFormat(part), "danger"));
  }
  const meter = mounted && part.size_kb > 0 ? dkMeter(part.used_kb, part.size_kb, dkUsageText(part.used_kb, part.avail_kb, part.size_kb)) : null;
  const named = part.label && part.label !== part.name;
  const place = mounted ? `${part.device} · ${part.mount}` : named ? part.device : "";
  return dkItem({ mounted, name: part.label || part.name, place, tags, actions, meter, size: meter ? "" : dkSize(part.size_kb) });
}

function dkDiskBlock(disk) {
  const iconName = disk.removable ? "plug" : disk.system ? "ft-chip" : "ft-disc";
  const head = h("div", { class: "dk-card-head" },
    h("span", { class: "dk-card-ic" }, icon(iconName)),
    h("div", { class: "dk-card-title" },
      h("b", { text: disk.model || disk.device }),
      h("span", { class: "muted small", text: disk.model ? `${disk.device} · ${dkSize(disk.size_kb)}` : dkSize(disk.size_kb) })),
    disk.removable ? dkTag("USB / removable") : null,
    disk.system ? dkTag("system disk", "warn") : null);
  const body = disk.parts.length
    ? disk.parts.map(dkPartRow)
    : [h("p", { class: "muted small dk-nopart", text: "No partition found. Create one in the Terminal (fdisk) before it can be formatted." })];
  return h("div", { class: `dk-card${disk.parts.length > 3 ? " is-wide" : ""}` }, head, h("div", { class: "dk-card-body" }, ...body));
}

function dkShareRow({ source, mount, fs, boot, mounted, size_kb: size, used_kb: used, avail_kb: avail }) {
  const actions = [];
  const manageable = dkMountOK(mount);
  if (mounted && manageable) actions.push(dkButton("plug-off", "Unmount", dkFill("Unmount {1}", mount), () => dkUnmount(mount)));
  if (!mounted && manageable) actions.push(dkButton("plug", "Mount", dkFill("Mount {1}", mount), () => dkMountSaved(mount)));
  if (boot && manageable) actions.push(dkButton("trash", "", dkFill("Do not mount {1} at start", mount), () => dkForget(mount), "sq"));
  const kind = fs === "cifs" || fs === "smb3" || fs === "smbfs" ? "CIFS" : "NFS";
  const tags = [dkTag(kind), boot ? dkTag("at start", "active") : null, !mounted ? dkTag("not mounted") : null];
  const meter = mounted && size > 0 ? dkMeter(used, size, dkUsageText(used, avail, size)) : null;
  const head = h("div", { class: "dk-card-head" },
    h("span", { class: "dk-card-ic" }, icon("link")),
    h("div", { class: "dk-card-title" }, h("b", { text: source }), h("span", { class: "muted small", text: mount })),
    ...tags.filter(Boolean));
  return h("div", { class: "dk-card" }, head,
    h("div", { class: "dk-card-body" },
      meter ? h("div", { class: "dk-item-meter" }, meter) : null,
      actions.length ? h("div", { class: "sc-actions" }, ...actions) : null));
}

function dkStat(label, used, total, note = "") {
  return h("div", { class: "dk-stat" },
    h("div", { class: "dk-stat-head" }, h("span", { class: "muted small", text: label }), h("b", { text: `${dkPercent(used, total)}%` })),
    dkBar(used, total),
    h("span", { class: "dk-usage", text: note || dkFill("{1} used · {2} total", dkSize(used), dkSize(total)) }));
}

function dkRender(info) {
  dk.info = info;
  $("#dkDrives").replaceChildren(...info.disks.map(dkDiskBlock));
  $("#dkNoDrives").hidden = info.disks.length > 0;

  const mounted = info.shares.map((share) => ({ ...share, mounted: true }));
  const saved = info.fstab.filter((entry) => entry.network && !entry.mounted).map((entry) => ({ source: entry.source, mount: entry.mount, fs: entry.fs, boot: true, mounted: false }));
  const rows = [...mounted, ...saved];
  $("#dkShares").replaceChildren(...rows.map(dkShareRow));
  $("#dkNoShares").hidden = rows.length > 0;
  $("#dkShareCount").textContent = rows.length ? (mounted.length === rows.length ? String(rows.length) : dkFill("Mounted: {1}", mounted.length)) : "";

  const memory = info.memory || {};
  const stats = [];
  if (memory.total_kb > 0) stats.push(dkStat("RAM", memory.total_kb - memory.available_kb, memory.total_kb));
  if (memory.swap_total_kb > 0) stats.push(dkStat("Swap", memory.swap_total_kb - memory.swap_free_kb, memory.swap_total_kb));
  const parts = info.disks.flatMap((disk) => disk.parts).filter((part) => part.mount && part.size_kb > 0 && !part.system && !part.swap);
  const storeTotal = parts.reduce((sum, part) => sum + part.size_kb, 0);
  if (storeTotal > 0) stats.push(dkStat("Mounted drives", parts.reduce((sum, part) => sum + part.used_kb, 0), storeTotal, dkFill("{1} free of {2}", dkSize(parts.reduce((sum, part) => sum + part.avail_kb, 0)), dkSize(storeTotal))));
  $("#dkMemory").replaceChildren(...stats);
  $("#dkMemory").hidden = stats.length === 0;
  $("#dkDriveCount").textContent = info.disks.length ? String(info.disks.length) : "";

  dkStatus(dk.busy ? $("#dkSpinText").textContent : dkFormatText());
  dkFormatOutcome(info.format);
}

function dkFormatOutcome(format) {
  if (!format || (format.state !== "done" && format.state !== "lost")) return;
  const key = `${format.state}|${format.device}|${format.ok}|${format.output}`;
  if (dk.formatAck === key) return;
  dk.formatAck = key;
  const name = format.device || "";
  const text = format.state === "lost"
    ? dkFill("The format of {1} was interrupted.", name)
    : format.ok ? dkFill("{1} was formatted.", name) : dkFill("Formatting {1} failed.", name);
  const bad = format.state === "lost" || !format.ok;
  dkResult([text, format.output], bad);
  toast(text, bad ? "error" : "info");
}

async function dkLoad({ quiet = false } = {}) {
  const device = currentDevice();
  if (!device) return null;
  if (quiet && dk.inflight) return null;
  const generation = ++dk.gen;
  dk.inflight = true;
  if (!quiet) {
    dkError("");
    dkSetBusy(true, dk.info ? "Refreshing…" : "Reading…");
  }
  try {
    const info = await api(profileURL(device.id, "disks/info"));
    if (generation === dk.gen) {
      if (!quiet || JSON.stringify(info) !== JSON.stringify(dk.info)) dkRender(info);
      return info;
    }
  } catch (error) {
    if (generation === dk.gen && !quiet) dkError(error.message);
  } finally {
    dk.inflight = false;
    if (generation === dk.gen) {
      dk.last = Date.now();
      if (!quiet) dkSetBusy(false);
    }
  }
  return null;
}

function dkWatch() {
  clearInterval(dk.timer);
  dk.timer = setInterval(() => {
    if (view.current !== "disks" || document.hidden || dk.busy) return;
    const wait = dkFormatting() ? DK_FORMAT_POLL_MS : DK_POLL_MS;
    if (Date.now() - dk.last >= wait) dkLoad({ quiet: true });
  }, DK_FORMAT_POLL_MS);
}
function dkUnwatch() { clearInterval(dk.timer); dk.timer = 0; }
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && view.current === "disks" && !dk.busy) dkLoad({ quiet: true });
});

async function dkRun(request, busyText) {
  const device = currentDevice();
  if (!device || dk.busy) return null;
  if (!opBegin("disks", device.id, "Another operation")) return null;
  dkError("");
  dkResult("");
  dkSetBusy(true, busyText);
  try {
    return await post(profileURL(device.id, "disks/action"), request);
  } catch (error) {
    dkError(error.message);
    return null;
  } finally {
    dkSetBusy(false);
    opEnd("disks");
  }
}

function dkReport(result, messages) {
  const text = messages[result.status] || (result.ok ? "Done." : "The receiver did not finish the action.");
  dkResult([text, result.output], !result.ok);
  toast(text, result.ok ? "info" : "error");
}

async function dkMount(part) {
  const name = part.label ? `${part.device} (${part.label})` : part.device;
  const mount = await ask({
    title: "Mount drive", message: dkFill("Choose the folder on the receiver where {1} appears.", name),
    label: "Mount folder", value: `/media/${dkSlug(part.label) || part.name}`, ok: "Mount", icon: "plug",
    validate: (value) => (dkMountOK(value) ? "" : "Use a folder under /media or /mnt, for example /media/hdd."),
  });
  if (!mount) return;
  const result = await dkRun({ action: "mount", device: part.device, mount }, dkFill("Mounting {1}…", part.device));
  if (!result) return;
  dkReport(result, {
    mounted: dkFill("{1} is mounted on {2}.", part.device, mount),
    already: dkFill("{1} is already mounted.", part.device),
    busy: dkFill("The folder {1} is already used by another mount.", mount),
    failed: dkFill("{1} could not be mounted.", part.device),
  });
  await dkLoad();
}

async function dkMountSaved(mount) {
  const result = await dkRun({ action: "mount-fstab", mount }, dkFill("Mounting {1}…", mount));
  if (!result) return;
  dkReport(result, {
    mounted: dkFill("{1} is mounted.", mount),
    already: dkFill("{1} is already mounted.", mount),
    noentry: dkFill("{1} is not in the receiver's startup list.", mount),
    failed: dkFill("{1} could not be mounted.", mount),
  });
  await dkLoad();
}

async function dkUnmount(mount) {
  const accepted = await ask({
    title: "Unmount", message: dkFill("Unmount {1}? Playback, recordings and transfers that use it will stop.", mount),
    input: false, ok: "Unmount", icon: "plug-off",
  });
  if (!accepted) return;
  let force = false;
  for (;;) {
    const result = await dkRun({ action: "umount", mount, force }, dkFill("Unmounting {1}…", mount));
    if (!result) return;
    if (result.status === "still" && !force) {
      const again = await ask({
        title: "Still in use", message: dkFill("{1} is busy. Detach it anyway? Programs using it may fail.", mount),
        input: false, ok: "Detach", danger: true, icon: "warn",
      });
      if (again) { force = true; continue; }
    }
    dkReport(result, {
      unmounted: dkFill("{1} was unmounted.", mount),
      notmounted: dkFill("{1} was not mounted.", mount),
      still: dkFill("{1} is busy and was not unmounted.", mount),
    });
    break;
  }
  await dkLoad();
}

async function dkForget(mount) {
  const accepted = await ask({
    title: "Remove from startup", message: dkFill("Stop mounting {1} at every start? The share stays mounted until it is unmounted.", mount),
    input: false, ok: "Remove", danger: true, icon: "trash",
  });
  if (!accepted) return;
  const result = await dkRun({ action: "fstab-remove", mount }, dkFill("Updating {1}…", mount));
  if (!result) return;
  dkReport(result, {
    removed: dkFill("{1} will no longer be mounted at start.", mount),
    noentry: dkFill("{1} is not in the receiver's startup list.", mount),
    fstabfailed: "The receiver's /etc/fstab could not be changed.",
  });
  await dkLoad();
}

function dkFormat(part) {
  const formats = dk.info?.formats || [];
  if (!formats.length) {
    dkError("This receiver has no format tool (mkfs). Install e2fsprogs or dosfstools from Packages.");
    return;
  }
  dk.target = part;
  $("#dkFormatDev").textContent = part.device;
  $("#dkFormatInfo").textContent = [part.label, part.fs, dkSize(part.size_kb)].filter(Boolean).join(" · ");
  const select = $("#dkFormatFS");
  select.replaceChildren(...formats.map((fs) => h("option", { value: fs, text: { ext4: "ext4 (Linux, recommended)", ext3: "ext3 (Linux)", vfat: "FAT32 (works everywhere)", exfat: "exFAT", ntfs: "NTFS (Windows)" }[fs] || fs })));
  $("#dkFormatLabel").value = part.label || "";
  $("#dkFormatSure").checked = false;
  $("#dkFormatError").hidden = true;
  openDialog($("#dkFormatDialog"));
}

async function dkFormatSubmit(event) {
  event.preventDefault();
  const part = dk.target;
  const error = $("#dkFormatError");
  const label = $("#dkFormatLabel").value.trim();
  const problem = !$("#dkFormatSure").checked ? "Tick the box to confirm."
    : !/^[A-Za-z0-9_-]{0,16}$/.test(label) ? "The name may use letters, digits, - and _ (16 characters at most)." : "";
  if (problem) { error.textContent = problem; error.hidden = false; return; }
  const fs = $("#dkFormatFS").value;
  $("#dkFormatDialog").close();
  const short = part.device.split("/").pop();
  const typed = await ask({
    title: dkFill("Erase {1}", part.device),
    message: dkFill("Everything on {1} will be erased and cannot be recovered. Type {2} to confirm.", part.device, short),
    label: "Device name", ok: "Erase and format", danger: true, icon: "trash",
    validate: (value) => (value === short ? "" : "The name does not match."),
  });
  if (!typed) return;
  const result = await dkRun({ action: "format", device: part.device, fs, label, confirm: typed }, dkFill("Starting the format of {1}…", part.device));
  if (!result) return;
  dkReport(result, {
    started: dkFill("Formatting {1} started. This can take several minutes.", part.device),
    ismounted: "Unmount the partition before formatting it.",
    running: "Another format is already running on the receiver.",
    nodevice: dkFill("{1} no longer exists on the receiver.", part.device),
  });
  await dkLoad();
}

const dkShareState = { kind: "cifs" };

function dkShareKind(kind) {
  dkShareState.kind = kind;
  for (const button of $$("#dkShareKind button")) button.setAttribute("aria-checked", String(button.dataset.kind === kind));
  const cifs = kind === "cifs";
  $("#dkCifsPane").hidden = !cifs;
  $("#dkDomainField").hidden = !cifs;
  $("#dkShareLabel").textContent = cifs ? "Share name" : "Exported folder";
  $("#dkShare").placeholder = cifs ? "Movies" : "/export/movies";
  $("#dkVersionLabel").textContent = DK_SHARE_PANES[kind];
  $("#dkVersion").replaceChildren(...(cifs
    ? [["auto", "Automatic"], ["3.0", "3.0"], ["2.1", "2.1"], ["2.0", "2.0"], ["1.0", "1.0 (old devices)"]]
    : [["auto", "Automatic"], ["3", "3"], ["4", "4"]]).map(([value, text]) => h("option", { value, text })));
  dkShareSuggest();
}

function dkShareSuggest() {
  if (dk.mountEdited) return;
  const share = $("#dkShare").value.trim().split("/").filter(Boolean).pop() || $("#dkHost").value.trim();
  $("#dkMount").value = dkSlug(share) ? `/media/net/${dkSlug(share)}` : "";
}

function dkShareOpen() {
  dk.mountEdited = false;
  for (const id of ["dkHost", "dkShare", "dkMount", "dkUser", "dkPass", "dkDomain", "dkOptions"]) $(`#${id}`).value = "";
  $("#dkPersist").checked = true;
  $("#dkShareError").hidden = true;
  dkShareKind("cifs");
  openDialog($("#dkShareDialog"));
  $("#dkHost").focus();
}

async function dkShareSubmit(event) {
  event.preventDefault();
  const kind = dkShareState.kind;
  const body = {
    action: "share-mount", kind, host: $("#dkHost").value.trim(), share: $("#dkShare").value.trim(),
    mount: $("#dkMount").value.trim(), username: $("#dkUser").value.trim(), password: $("#dkPass").value,
    domain: $("#dkDomain").value.trim(), version: $("#dkVersion").value, options: $("#dkOptions").value.trim(),
    persist: $("#dkPersist").checked,
  };
  const error = $("#dkShareError");
  const problem = !/^[A-Za-z0-9._-]{1,100}$/.test(body.host) ? "Enter the server name or IP address."
    : !body.share ? (kind === "cifs" ? "Enter the share name." : "Enter the exported folder, for example /export/movies.")
    : kind === "nfs" && !body.share.startsWith("/") ? "The exported folder starts with /, for example /export/movies."
    : !dkMountOK(body.mount) ? "Use a folder under /media or /mnt, for example /media/net/movies." : "";
  if (problem) { error.textContent = problem; error.hidden = false; return; }
  if (kind === "nfs") Object.assign(body, { username: "", password: "", domain: "" });
  $("#dkShareDialog").close();
  const result = await dkRun(body, dkFill("Mounting {1}…", body.mount));
  $("#dkPass").value = "";
  if (!result) return;
  dkReport(result, {
    mounted: dkFill("{1} is mounted.", body.mount),
    saved: dkFill("{1} is mounted and will be mounted again at start.", body.mount),
    fstabfailed: "It is mounted, but the receiver's /etc/fstab could not be changed.",
    fstabconflict: dkFill("{1} is already used by a drive in /etc/fstab. Choose another folder.", body.mount),
    busy: dkFill("The folder {1} is already used by another mount.", body.mount),
    failed: dkFill("The share could not be mounted at {1}.", body.mount),
  });
  await dkLoad();
}

function disksEnter() {
  const device = currentDevice();
  if (!device) {
    dk.profileID = "";
    dk.gen++;
    dk.info = null;
    dkUnwatch();
    dkSetBusy(false);
    dkError("Select a receiver first.");
    dkResult("");
    for (const id of ["#dkDrives", "#dkShares", "#dkMemory"]) $(id).replaceChildren();
    return;
  }
  if (device.id !== dk.profileID) {
    dk.profileID = device.id;
    dk.info = null;
    dk.formatAck = "";
    for (const id of ["#dkDrives", "#dkShares", "#dkMemory"]) $(id).replaceChildren();
    dkResult("");
  }
  dkWatch();
  dkLoad();
}

$("#dkReload").addEventListener("click", () => dkLoad());
$("#dkAddShare").addEventListener("click", dkShareOpen);
$("#dkShareForm").addEventListener("submit", dkShareSubmit);
$("#dkShareCancel").addEventListener("click", () => $("#dkShareDialog").close());
$("#dkFormatForm").addEventListener("submit", dkFormatSubmit);
$("#dkFormatCancel").addEventListener("click", () => $("#dkFormatDialog").close());
for (const button of $$("#dkShareKind button")) button.addEventListener("click", () => dkShareKind(button.dataset.kind));
for (const id of ["dkHost", "dkShare"]) $(`#${id}`).addEventListener("input", dkShareSuggest);
$("#dkMount").addEventListener("input", () => { dk.mountEdited = $("#dkMount").value.trim() !== ""; });

window.DCC_I18N?.add?.("ar", {
  "Disks": "الأقراص", "Add network share": "إضافة مشاركة شبكة", "Mount a folder from a computer or NAS on the network": "تركيب مجلد من كمبيوتر أو NAS على الشبكة",
  "Drives": "المحركات", "No hard disk or USB drive was found on this receiver.": "لم يُعثر على قرص صلب أو فلاشة USB في هذا الجهاز.",
  "Network shares": "مشاركات الشبكة", "No network share is mounted.": "لا توجد مشاركة شبكة مركّبة.", "Memory": "الذاكرة",
  "Windows / Samba (CIFS)": "ويندوز / Samba (CIFS)", "Server": "الخادم", "Share name": "اسم المشاركة", "Exported folder": "المجلد المصدَّر",
  "Mount folder on the receiver": "مجلد التركيب على الجهاز", "User name": "اسم المستخدم", "Empty for guest access": "اتركه فارغاً للدخول كضيف",
  "SMB version": "إصدار SMB", "NFS version": "إصدار NFS", "Domain": "النطاق", "Optional": "اختياري", "Extra mount options": "خيارات تركيب إضافية",
  "Optional, comma separated": "اختياري، مفصولة بفواصل", "Mount again at every start": "التركيب مجدداً عند كل تشغيل", "Mount": "تركيب",
  "Format partition": "تنسيق قسم", "File system": "نظام الملفات", "Formatting erases everything on this partition. It cannot be undone.": "التنسيق يمسح كل ما على هذا القسم ولا يمكن التراجع عنه.",
  "I understand that all files on it will be lost": "أدرك أن كل الملفات عليه ستُفقد", "Continue": "متابعة",
  "Formatting {1}… this can take several minutes.": "جارٍ تنسيق {1}… قد يستغرق عدة دقائق.", "read-only": "للقراءة فقط", "system": "النظام", "Formatting…": "جارٍ التنسيق…",
  "Mount {1}": "تركيب {1}", "Unmount {1}": "فك تركيب {1}", "Unmount": "فك التركيب", "Format {1}": "تنسيق {1}", "Format": "تنسيق",
  "{1} used · {2} free of {3}": "{1} مستخدم · {2} فارغ من {3}", "{1} used · {2} total": "{1} مستخدم · {2} إجمالي", "USB / removable": "USB / قابل للإزالة", "system disk": "قرص النظام",
  "No partition found. Create one in the Terminal (fdisk) before it can be formatted.": "لا يوجد قسم. أنشئ قسماً من الطرفية (fdisk) قبل التنسيق.",
  "Do not mount {1} at start": "عدم تركيب {1} عند التشغيل", "at start": "عند التشغيل", "not mounted": "غير مركّب", "No swap space is in use.": "لا توجد مساحة swap مستخدمة.", "Mounted: {1}": "المركّب: {1}",
  "The format of {1} was interrupted.": "توقف تنسيق {1} قبل اكتماله.", "{1} was formatted.": "تم تنسيق {1}.", "Formatting {1} failed.": "فشل تنسيق {1}.",
  "Done.": "تم.", "The receiver did not finish the action.": "لم ينهِ الجهاز العملية.", "Mount drive": "تركيب محرك",
  "Choose the folder on the receiver where {1} appears.": "اختر مجلد الجهاز الذي سيظهر فيه {1}.", "Mount folder": "مجلد التركيب",
  "Use a folder under /media or /mnt, for example /media/hdd.": "استخدم مجلداً داخل /media أو /mnt، مثل /media/hdd.", "Mounting {1}…": "جارٍ تركيب {1}…",
  "{1} is mounted on {2}.": "تم تركيب {1} على {2}.", "{1} is already mounted.": "{1} مركّب بالفعل.", "The folder {1} is already used by another mount.": "المجلد {1} مستخدم في تركيب آخر.",
  "{1} could not be mounted.": "تعذّر تركيب {1}.", "{1} is mounted.": "تم تركيب {1}.", "{1} is not in the receiver's startup list.": "{1} ليس في قائمة التشغيل في الجهاز.",
  "Unmount {1}? Playback, recordings and transfers that use it will stop.": "فك تركيب {1}؟ سيتوقف التشغيل والتسجيل والنقل الذي يستخدمه.", "Unmounting {1}…": "جارٍ فك تركيب {1}…",
  "Still in use": "ما زال قيد الاستخدام", "{1} is busy. Detach it anyway? Programs using it may fail.": "{1} مشغول. فصله رغم ذلك؟ قد تفشل البرامج التي تستخدمه.", "Detach": "فصل",
  "{1} was unmounted.": "تم فك تركيب {1}.", "{1} was not mounted.": "{1} لم يكن مركّباً.", "{1} is busy and was not unmounted.": "{1} مشغول ولم يُفك تركيبه.",
  "Remove from startup": "إزالة من التشغيل", "Stop mounting {1} at every start? The share stays mounted until it is unmounted.": "إيقاف تركيب {1} عند كل تشغيل؟ تبقى المشاركة مركّبة حتى يُفك تركيبها.", "Remove": "إزالة",
  "Updating {1}…": "جارٍ تحديث {1}…", "{1} will no longer be mounted at start.": "لن يُركَّب {1} عند التشغيل بعد الآن.", "The receiver's /etc/fstab could not be changed.": "تعذّر تعديل /etc/fstab في الجهاز.",
  "This receiver has no format tool (mkfs). Install e2fsprogs or dosfstools from Packages.": "لا توجد أداة تنسيق (mkfs) في هذا الجهاز. ثبّت e2fsprogs أو dosfstools من صفحة الحزم.",
  "ext4 (Linux, recommended)": "ext4 (لينكس، موصى به)", "ext3 (Linux)": "ext3 (لينكس)", "FAT32 (works everywhere)": "FAT32 (يعمل في كل مكان)", "NTFS (Windows)": "NTFS (ويندوز)",
  "Tick the box to confirm.": "فعّل المربع للتأكيد.", "The name may use letters, digits, - and _ (16 characters at most).": "يمكن أن يتكوّن الاسم من حروف وأرقام و - و _ (16 حرفاً كحد أقصى).",
  "Erase {1}": "مسح {1}", "Everything on {1} will be erased and cannot be recovered. Type {2} to confirm.": "سيُمسح كل ما على {1} ولا يمكن استرجاعه. اكتب {2} للتأكيد.",
  "Device name": "اسم الجهاز", "Erase and format": "مسح وتنسيق", "The name does not match.": "الاسم غير مطابق.", "Starting the format of {1}…": "جارٍ بدء تنسيق {1}…",
  "Formatting {1} started. This can take several minutes.": "بدأ تنسيق {1}. قد يستغرق عدة دقائق.", "Unmount the partition before formatting it.": "فك تركيب القسم قبل تنسيقه.",
  "Another format is already running on the receiver.": "هناك عملية تنسيق أخرى تعمل على الجهاز.", "{1} no longer exists on the receiver.": "{1} لم يعد موجوداً في الجهاز.",
  "Automatic": "تلقائي", "1.0 (old devices)": "1.0 (للأجهزة القديمة)", "Enter the server name or IP address.": "أدخل اسم الخادم أو عنوان IP.", "Enter the share name.": "أدخل اسم المشاركة.",
  "Enter the exported folder, for example /export/movies.": "أدخل المجلد المصدَّر، مثل /export/movies.", "The exported folder starts with /, for example /export/movies.": "يبدأ المجلد المصدَّر بـ /، مثل /export/movies.",
  "Use a folder under /media or /mnt, for example /media/net/movies.": "استخدم مجلداً داخل /media أو /mnt، مثل /media/net/movies.",
  "{1} is mounted and will be mounted again at start.": "تم تركيب {1} وسيُركَّب مجدداً عند التشغيل.", "It is mounted, but the receiver's /etc/fstab could not be changed.": "تم التركيب، لكن تعذّر تعديل /etc/fstab في الجهاز.",
  "{1} is already used by a drive in /etc/fstab. Choose another folder.": "{1} مستخدم لمحرك في /etc/fstab. اختر مجلداً آخر.", "The share could not be mounted at {1}.": "تعذّر تركيب المشاركة في {1}.",
  "Unsupported device.": "جهاز غير مدعوم.", "Choose a folder under /media or /mnt, for example /media/hdd.": "اختر مجلداً داخل /media أو /mnt، مثل /media/hdd.", "Unsupported file system.": "نظام ملفات غير مدعوم.",
  "The extra options contain characters that are not allowed.": "الخيارات الإضافية تحتوي حروفاً غير مسموحة.", "Unsupported NFS version.": "إصدار NFS غير مدعوم.", "Enter the share name, for example Movies.": "أدخل اسم المشاركة، مثل Movies.",
  "The user name contains characters that are not allowed.": "اسم المستخدم يحتوي حروفاً غير مسموحة.", "The password is too long or contains control characters.": "كلمة المرور طويلة جداً أو تحتوي أحرف تحكم.",
  "Unsupported SMB version.": "إصدار SMB غير مدعوم.", "Choose NFS or CIFS.": "اختر NFS أو CIFS.", "Type the device name to confirm.": "اكتب اسم الجهاز للتأكيد.",
  "This partition was not found on the receiver.": "لم يُعثر على هذا القسم في الجهاز.", "This partition is on the disk the receiver runs from. It is not formatted from here.": "هذا القسم على القرص الذي يعمل منه الجهاز، ولا يُنسَّق من هنا.",
  "This receiver has no tool to format as {1}.": "لا توجد في هذا الجهاز أداة للتنسيق بنظام {1}.",
});
