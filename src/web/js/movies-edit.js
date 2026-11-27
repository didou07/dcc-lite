"use strict";

const mv = { movie: null, busy: false, locations: [] };

const mvDirTrim = (dir) => String(dir || "").replace(/\/+$/, "");
const mvDeepEnough = (dir) => /^\/[^/]+\/[^/]+\/[^/]+/.test(mvDirTrim(dir));
const mvSafeName = (text) => String(text || "").replace(/[\\/]+/g, "-").trim();

function mvErr(id, message) {
  const node = $(id);
  node.hidden = !message;
  node.textContent = message || "";
}

function mvKnownTags() {
  const counts = new Map();
  for (const m of guide.movies || []) for (const tag of gdTags(m)) counts.set(tag, (counts.get(tag) || 0) + 1);
  return [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([tag]) => tag).slice(0, 24);
}

function mvTagList() {
  const seen = new Set();
  return $("#mvTags").value.split(/\s+/).filter((tag) => tag && !seen.has(tag.toLowerCase()) && seen.add(tag.toLowerCase()));
}

function mvPaintChips() {
  const active = new Set(mvTagList().map((tag) => tag.toLowerCase()));
  $("#mvTagChips").replaceChildren(...mvKnownTags().map((tag) => h("button", {
    type: "button", class: `mv-chip${active.has(tag.toLowerCase()) ? " on" : ""}`, "aria-pressed": String(active.has(tag.toLowerCase())), text: tag,
    onClick: () => {
      const list = mvTagList();
      const at = list.findIndex((x) => x.toLowerCase() === tag.toLowerCase());
      if (at >= 0) list.splice(at, 1); else list.push(tag);
      $("#mvTags").value = list.join(" ");
      mvPaintChips();
    },
  })));
}

function mvSetInfoBusy(busy) {
  mv.busy = busy;
  for (const id of ["#mvTitle", "#mvDesc", "#mvTags", "#mvRename", "#mvInfoOk"]) $(id).disabled = busy;
}

async function mvEditInfo(m) {
  mv.movie = m;
  mvErr("#mvInfoError", "");
  $("#mvInfoFile").textContent = m.file;
  $("#mvTitle").value = m.title || "";
  $("#mvDesc").value = m.info || "";
  $("#mvTags").value = m.tags || "";
  $("#mvRename").checked = false;
  mvPaintChips();
  mvSetInfoBusy(true);
  openDialog($("#mvInfoDialog"));
  try {
    const meta = await api(guideURL(`movies/meta?file=${encodeURIComponent(m.file)}`));
    if (mv.movie !== m) return;
    if (meta.exists) {
      $("#mvTitle").value = meta.title || m.title || "";
      $("#mvDesc").value = meta.description || "";
      $("#mvTags").value = meta.tags || "";
    }
    mvPaintChips();
    mvSetInfoBusy(false);
    $("#mvTitle").focus();
    $("#mvTitle").select();
  } catch (error) {
    if (mv.movie !== m) return;
    mvErr("#mvInfoError", error.message);
    $("#mvInfoCancel").focus();
  }
}

async function mvInfoSubmit(event) {
  event.preventDefault();
  const m = mv.movie;
  if (!m || mv.busy) return;
  const title = $("#mvTitle").value.trim();
  if (!title) { mvErr("#mvInfoError", "Type a title."); return; }
  mvErr("#mvInfoError", "");
  mvSetInfoBusy(true);
  try {
    await post(guideURL("movies/meta"), { file: m.file, ref: m.ref, time: m.time || 0, title, description: $("#mvDesc").value.trim(), tags: mvTagList().join(" ") });
    let renamed = true;
    if ($("#mvRename").checked && mvSafeName(title) !== m.title) {
      try { await post(guideURL("movies/rename"), { ref: m.ref, name: mvSafeName(title) }); } catch (error) { renamed = false; toast(error.message, "error"); }
    }
    $("#mvInfoDialog").close();
    toast(renamed ? "Recording updated" : "Saved, but the file was not renamed", renamed ? "info" : "error");
    await gdLoadMovies(guide.dir);
  } catch (error) {
    mvErr("#mvInfoError", error.message);
    mvSetInfoBusy(false);
  }
}

async function mvFolderTools() {
  const dir = mvDirTrim(guide.dir);
  if (!dir) { gdError("Open a folder first."); return; }
  mvErr("#mvFolderError", "");
  $("#mvFolderDir").textContent = `${dir}/`;
  $("#mvNewName").value = "";
  $("#mvRenameName").value = dir.split("/").pop();
  $("#mvThisSec").hidden = !mvDeepEnough(dir);
  mvPaintBookmark();
  openDialog($("#mvFolderDialog"));
  try {
    mv.locations = (await api(guideURL("movie-locations"))).locations || [];
    mvPaintBookmark();
  } catch { }
}

const mvIsBookmark = () => mv.locations.some((l) => mvDirTrim(l) === mvDirTrim(guide.dir));
function mvPaintBookmark() {
  $("#mvBookmark span").textContent = mvIsBookmark() ? "Remove from recording folders" : "Add to recording folders";
}

const MV_FOLDER_MESSAGES = {
  created: "Folder created", exists: "A folder with that name already exists.", removed: "Folder deleted",
  notempty: "The folder is not empty. Move or delete its recordings first.", ismount: "This folder is a mounted drive and is not changed from here.",
  missing: "The folder does not exist.", renamed: "Folder renamed", failed: "The receiver could not do that.",
};

async function mvFolderAction(request, after) {
  mvErr("#mvFolderError", "");
  for (const button of $$("#mvFolderDialog button")) button.disabled = true;
  try {
    const result = await post(guideURL("movies/folder"), request);
    if (!result.ok) {
      mvErr("#mvFolderError", result.output || MV_FOLDER_MESSAGES[result.status] || "The receiver could not do that.");
      return;
    }
    $("#mvFolderDialog").close();
    toast(MV_FOLDER_MESSAGES[result.status] || "Done");
    await after(result);
  } catch (error) {
    mvErr("#mvFolderError", error.message);
  } finally {
    for (const button of $$("#mvFolderDialog button")) button.disabled = false;
  }
}

function mvCreateFolder() {
  const name = $("#mvNewName").value.trim();
  if (!name || /[\\/]/.test(name) || name === "." || name === "..") { mvErr("#mvFolderError", "The folder name must not be empty or contain a slash."); return; }
  const dir = `${mvDirTrim(guide.dir)}/${name}`;
  return mvFolderAction({ action: "create", dir, bookmark: $("#mvNewBookmark").checked }, () => gdLoadMovies(`${dir}/`));
}

function mvRenameFolder() {
  const name = $("#mvRenameName").value.trim();
  const dir = mvDirTrim(guide.dir);
  if (!name || /[\\/]/.test(name) || name === "." || name === "..") { mvErr("#mvFolderError", "The folder name must not be empty or contain a slash."); return; }
  if (name === dir.split("/").pop()) { $("#mvFolderDialog").close(); return; }
  return mvFolderAction({ action: "rename", dir, name }, () => gdLoadMovies(`${gdParent(dir).replace(/\/+$/, "")}/${name}/`));
}

async function mvDeleteFolder() {
  const dir = mvDirTrim(guide.dir);
  const accepted = await ask({ title: "Delete this folder?", message: `${dir}/\nOnly an empty folder can be deleted.`, input: false, ok: "Delete", danger: true, icon: "trash" });
  if (!accepted) return;
  return mvFolderAction({ action: "remove", dir }, () => gdLoadMovies(gdParent(dir)));
}

function mvToggleBookmark() {
  const dir = mvDirTrim(guide.dir);
  const remove = mvIsBookmark();
  return mvFolderAction({ action: remove ? "bookmark-remove" : "bookmark-add", dir }, async () => {
    toast(remove ? "Removed from the recording folders" : "Added to the recording folders");
    try { mv.locations = (await api(guideURL("movie-locations"))).locations || []; } catch { }
  });
}

$("#mvInfoForm").addEventListener("submit", mvInfoSubmit);
$("#mvInfoCancel").addEventListener("click", () => $("#mvInfoDialog").close());
$("#mvTags").addEventListener("input", mvPaintChips);
$("#guideFolderTools").addEventListener("click", mvFolderTools);
$("#mvFolderClose").addEventListener("click", () => $("#mvFolderDialog").close());
$("#mvCreate").addEventListener("click", mvCreateFolder);
$("#mvNewName").addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); mvCreateFolder(); } });
$("#mvRenameDir").addEventListener("click", mvRenameFolder);
$("#mvRenameName").addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); mvRenameFolder(); } });
$("#mvDeleteDir").addEventListener("click", mvDeleteFolder);
$("#mvBookmark").addEventListener("click", mvToggleBookmark);

window.DCC_I18N?.add?.("ar", {
  "Edit recording": "تعديل التسجيل", "Title": "العنوان", "Description": "الوصف", "Tags": "الوسوم", "Separate tags with a space": "افصل الوسوم بمسافة",
  "Tags used on this receiver": "الوسوم المستخدمة في هذا الجهاز", "Also rename the file to match the title": "إعادة تسمية الملف ليطابق العنوان أيضاً",
  "The receiver's own screen shows the change after it reloads its recordings.": "تظهر التغييرات على شاشة الجهاز بعد أن يُعيد قراءة التسجيلات.",
  "Edit title, description and tags": "تعديل العنوان والوصف والوسوم", "Not available while this is recording": "غير متاح أثناء التسجيل",
  "Type a title.": "اكتب عنواناً.", "Recording updated": "تم تحديث التسجيل", "Saved, but the file was not renamed": "تم الحفظ لكن لم تُعاد تسمية الملف",
  "Folders": "المجلدات", "Folders: create, rename, delete, recording folders": "المجلدات: إنشاء وإعادة تسمية وحذف ومجلدات التسجيل",
  "New folder here": "مجلد جديد هنا", "Folder name": "اسم المجلد", "New folder name": "اسم المجلد الجديد", "Create": "إنشاء",
  "Add to the receiver's recording folders": "إضافته إلى مجلدات التسجيل في الجهاز", "This folder": "هذا المجلد", "Rename": "إعادة تسمية",
  "Add to recording folders": "إضافة إلى مجلدات التسجيل", "Remove from recording folders": "إزالة من مجلدات التسجيل", "Delete empty folder": "حذف المجلد الفارغ",
  "Open a folder first.": "افتح مجلداً أولاً.", "Folder created": "تم إنشاء المجلد", "A folder with that name already exists.": "يوجد مجلد بهذا الاسم.",
  "Folder deleted": "تم حذف المجلد", "The folder is not empty. Move or delete its recordings first.": "المجلد ليس فارغاً. انقل تسجيلاته أو احذفها أولاً.",
  "This folder is a mounted drive and is not changed from here.": "هذا المجلد محرك مركّب ولا يُعدَّل من هنا.", "The folder does not exist.": "المجلد غير موجود.",
  "Folder renamed": "تمت إعادة تسمية المجلد", "The receiver could not do that.": "تعذّر على الجهاز تنفيذ ذلك.", "Done": "تم",
  "The folder name must not be empty or contain a slash.": "يجب ألا يكون اسم المجلد فارغاً أو يحتوي على شرطة مائلة.",
  "Delete this folder?": "حذف هذا المجلد؟", "Only an empty folder can be deleted.": "لا يُحذف إلا المجلد الفارغ.",
  "Added to the recording folders": "تمت الإضافة إلى مجلدات التسجيل", "Removed from the recording folders": "تمت الإزالة من مجلدات التسجيل",
  "This recording has no information file and cannot be described.": "لا يوجد ملف معلومات لهذا التسجيل فلا يمكن وصفه.",
  "The recording's information file is not in the expected format.": "ملف معلومات التسجيل ليس بالصيغة المتوقعة.", "The text is too long.": "النص طويل جداً.",
  "Choose a folder inside a drive, for example /media/hdd/movie/Series.": "اختر مجلداً داخل محرك، مثل /media/hdd/movie/Series.",
});
