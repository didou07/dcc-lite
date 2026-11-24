const HEX_BYTES = Array.from({ length: 256 }, (_, i) => i.toString(16).padStart(2, "0"));
const HEX_UNDO_LIMIT = 5000;

const hexEscape = (ch) => (ch === "&" ? "&amp;" : ch === "<" ? "&lt;" : ch === ">" ? "&gt;" : ch);

class HexView {
  constructor(host) {
    this.host = host;
    this.bytes = new Uint8Array(0);
    this.original = this.bytes;
    this.editable = false;
    this.onChange = null;
    this.searchInput = null;
    this.cursor = 0; this.nibble = 0; this.column = "hex";
    this.selectionAnchor = -1; this.selectionFocus = -1; this.searchMatch = null; this.searchSelection = false;
    this.dragging = false;
    this.ops = []; this.redoOps = [];
    this.changed = new Set(); this.structural = 0; this.structuralLost = 0;
    this.rows = 0; this.lineHeight = 20; this.first = -1; this.last = -1; this.frame = 0;
    this.spacer = document.createElement("div");
    this.pre = document.createElement("pre");
    this.pre.className = "hex-rows";
    this.host.replaceChildren(this.spacer, this.pre);
    this.host.addEventListener("scroll", () => this.schedule());
    this.host.addEventListener("mousedown", (event) => this.onMouse(event));
    this.host.addEventListener("keydown", (event) => this.onKey(event));
    this.host.addEventListener("focus", () => this.redraw());
    this.host.addEventListener("blur", () => this.redraw());
    document.addEventListener?.("mousemove", (event) => this.onMouseMove(event));
    document.addEventListener?.("mouseup", () => { this.dragging = false; });
  }

  static parseSearchPattern(value, mode = "hex") {
    const raw = String(value ?? "");
    if (mode === "ascii") {
      if (!raw.length) return { bytes: null, error: "Enter a value to search" };
      const bytes = [];
      for (let i = 0; i < raw.length; i++) {
        const code = raw.charCodeAt(i);
        if (code > 0x7f) return { bytes: null, error: "ASCII search accepts standard ASCII characters only" };
        bytes.push(code);
      }
      return { bytes: Uint8Array.from(bytes), error: "" };
    }
    const text = raw.trim();
    if (!text) return { bytes: null, error: "Enter a value to search" };
    const hex = text.replace(/0x/gi, "").replace(/[\s,:;_-]+/g, "");
    if (!hex || !/^[0-9a-f]+$/i.test(hex)) return { bytes: null, error: "Use hexadecimal bytes, for example DE AD BE EF" };
    if (hex.length % 2) return { bytes: null, error: "Enter complete pairs of hexadecimal digits" };
    const bytes = new Uint8Array(hex.length / 2);
    for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    return { bytes, error: "" };
  }

  set(bytes, { editable = false } = {}) {
    this.bytes = bytes.slice();
    this.original = bytes;
    this.editable = editable;
    this.cursor = 0; this.nibble = 0; this.column = "hex";
    this.selectionAnchor = this.selectionFocus = -1;
    this.searchMatch = null; this.searchSelection = false; this.dragging = false;
    this.ops = []; this.redoOps = []; this.changed = new Set(); this.structural = 0; this.structuralLost = 0;
    this.host.classList.toggle("hex-readonly", !editable);
    this.layout();
    this.host.scrollTop = 0;
    this.redraw();
    this.notify();
  }
  rebase() {
    this.original = this.bytes.slice();
    this.ops = []; this.redoOps = []; this.changed = new Set(); this.structural = 0; this.structuralLost = 0;
    this.redraw();
    this.notify();
  }
  revert() { this.set(this.original, { editable: this.editable }); }
  layout() {
    this.measure();
    this.rows = this.editable ? Math.floor(this.bytes.length / 16) + 1 : Math.ceil(this.bytes.length / 16);
    this.offsetWidth = this.bytes.length > 0xffffffff ? 12 : 8;
    this.spacer.style.height = `${Math.max(1, this.rows) * this.lineHeight}px`;
  }
  measure() {
    const probe = document.createElement("pre");
    probe.className = "hex-rows";
    probe.style.visibility = "hidden";
    probe.textContent = "0\n0";
    this.host.appendChild(probe);
    const height = probe.getBoundingClientRect().height / 2;
    probe.remove();
    this.lineHeight = height > 4 ? height : 20;
  }

  dirty() { return this.structural > 0 || this.changed.size > 0; }
  get changedCount() { return this.changed.size; }
  get sizeChange() { return this.bytes.length - this.original.length; }
  notify() { this.onChange?.(this); }
  getSelectionRange() {
    if (this.selectionAnchor < 0 || this.selectionFocus < 0 || !this.bytes.length) return null;
    const start = Math.min(this.selectionAnchor, this.selectionFocus);
    const end = Math.max(this.selectionAnchor, this.selectionFocus);
    return { start, end, length: end - start + 1 };
  }
  selectedText(mode = "hex") {
    const range = this.getSelectionRange();
    if (!range) return "";
    const bytes = this.bytes.subarray(range.start, range.end + 1);
    if (mode === "ascii") return Array.from(bytes, (b) => b >= 32 && b <= 126 ? String.fromCharCode(b) : ".").join("");
    return Array.from(bytes, (b) => HEX_BYTES[b].toUpperCase()).join(" ");
  }
  async copySelected(mode = "hex") {
    const text = this.selectedText(mode);
    if (!text) return false;
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch { /* Use the local copy fallback. */ }
    let area;
    try {
      area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      return Boolean(document.execCommand("copy"));
    } catch { return false; }
    finally { area?.remove(); }
  }
  clearSelection({ notify = true } = {}) {
    if (this.selectionAnchor < 0 && this.selectionFocus < 0 && !this.searchMatch) return;
    this.selectionAnchor = this.selectionFocus = -1;
    this.searchMatch = null;
    this.searchSelection = false;
    if (notify) this.redraw();
    if (notify) this.notify();
  }
  clearSearch() {
    this.searchMatch = null;
    if (this.searchSelection) this.selectionAnchor = this.selectionFocus = -1;
    this.searchSelection = false;
    this.redraw();
    this.notify();
  }
  selectAll() {
    if (!this.bytes.length) return;
    this.selectionAnchor = 0;
    this.selectionFocus = this.bytes.length - 1;
    this.searchMatch = null;
    this.searchSelection = false;
    this.cursor = 0;
    this.host.scrollTop = 0;
    this.redraw();
    this.notify();
  }

  schedule() {
    if (this.frame) return;
    this.frame = requestAnimationFrame(() => { this.frame = 0; this.draw(); });
  }
  redraw() { this.first = this.last = -1; this.draw(); }
  draw() {
    if (!this.rows || (!this.bytes.length && !this.editable)) { this.pre.textContent = "(empty file)"; this.pre.style.top = "0"; return; }
    const view = this.host.clientHeight || 400;
    const first = Math.max(0, Math.floor(this.host.scrollTop / this.lineHeight) - 12);
    const last = Math.min(this.rows, Math.ceil((this.host.scrollTop + view) / this.lineHeight) + 12);
    if (first === this.first && last === this.last) return;
    this.first = first; this.last = last;
    const lines = [];
    for (let row = first; row < last; row++) lines.push(this.rowHTML(row * 16));
    this.pre.style.top = `${first * this.lineHeight}px`;
    this.pre.innerHTML = lines.join("\n");
  }
  rowHTML(start) {
    const { bytes, cursor, editable } = this;
    const focused = document.activeElement === this.host;
    const track = this.structural === 0;
    const selection = this.getSelectionRange();
    const searchMatch = this.searchMatch;
    let hex = "", text = "";
    for (let i = 0; i < 16; i++) {
      const at = start + i;
      if (at < bytes.length) {
        const b = bytes[at];
        const mod = track && this.changed.has(at) ? " hx-m" : "";
        const selected = selection && at >= selection.start && at <= selection.end ? " hx-sel" : "";
        const found = searchMatch && at >= searchMatch.start && at <= searchMatch.end ? " hx-find" : "";
        const here = editable && at === cursor;
        const position = !editable && at === cursor && !selected && !found ? " hx-pos" : "";
        const hexClass = mod + selected + found + position + (here ? (this.column === "hex" ? " hx-cur" : " hx-cur2") : "");
        const textClass = mod + selected + found + position + (here ? (this.column === "text" ? " hx-cur" : " hx-cur2") : "");
        const ch = b >= 32 && b < 127 ? hexEscape(String.fromCharCode(b)) : ".";
        hex += `<i data-i="${at}" class="${hexClass.trim()}${here && !focused ? " hx-idle" : ""}">${HEX_BYTES[b]}</i> `;
        text += `<i data-i="${at}" data-c="t" class="${textClass.trim()}${here && !focused ? " hx-idle" : ""}">${ch}</i>`;
      } else if (editable && at === bytes.length) {
        const here = cursor === at;
        hex += `<i data-i="${at}" class="hx-end${here ? (this.column === "hex" ? " hx-cur" : " hx-cur2") : ""}">  </i> `;
        text += `<i data-i="${at}" data-c="t" class="hx-end${here ? (this.column === "text" ? " hx-cur" : " hx-cur2") : ""}"> </i>`;
      } else hex += "   ";
      if (i === 7) hex += " ";
    }
    return `<span class="hx-off">${start.toString(16).padStart(this.offsetWidth, "0")}</span>  ${hex} |${text}|`;
  }

  goTo(offset) {
    if (!Number.isFinite(offset) || offset < 0 || offset >= Math.max(1, this.bytes.length)) return false;
    this.selectionAnchor = this.selectionFocus = -1;
    this.searchMatch = null; this.searchSelection = false;
    this.setCursor(offset, { scroll: "top" });
    return true;
  }
  setCursor(index, { scroll = "visible" } = {}) {
    const max = this.editable ? this.bytes.length : Math.max(0, this.bytes.length - 1);
    this.cursor = Math.max(0, Math.min(max, index));
    this.nibble = 0;
    const top = Math.floor(this.cursor / 16) * this.lineHeight;
    const view = this.host.clientHeight || 400;
    if (scroll === "top") this.host.scrollTop = top;
    else if (top < this.host.scrollTop) this.host.scrollTop = top;
    else if (top + this.lineHeight > this.host.scrollTop + view) this.host.scrollTop = top + this.lineHeight - view;
    this.redraw();
    this.notify();
  }

  find(pattern, direction = 1) {
    if (!(pattern instanceof Uint8Array)) pattern = Uint8Array.from(pattern || []);
    if (!pattern.length || pattern.length > this.bytes.length) return null;
    const max = this.bytes.length - pattern.length;
    const range = this.getSelectionRange();
    const forward = direction >= 0;
    const from = forward ? (range ? range.end + 1 : this.cursor) : (range ? range.start - 1 : this.cursor - 1);
    const scan = (start, end, step) => {
      const size = pattern.length;
      if (step > 0) {
        const skip = new Uint32Array(256); skip.fill(size);
        for (let i = 0; i < size - 1; i++) skip[pattern[i]] = size - 1 - i;
        for (let at = start; at <= end;) {
          let j = size - 1;
          while (j >= 0 && this.bytes[at + j] === pattern[j]) j--;
          if (j < 0) return at;
          at += skip[this.bytes[at + size - 1]];
        }
      } else {
        const skip = new Uint32Array(256); skip.fill(size);
        for (let i = size - 1; i >= 1; i--) skip[pattern[i]] = i;
        for (let at = start; at >= end;) {
          let j = 0;
          while (j < size && this.bytes[at + j] === pattern[j]) j++;
          if (j === size) return at;
          at -= skip[this.bytes[at]];
        }
      }
      return -1;
    };
    let index = -1, wrapped = false;
    if (forward) {
      const start = Math.max(0, from);
      if (start <= max) index = scan(start, max, 1);
      if (index < 0 && start > 0) { index = scan(0, Math.min(max, start - 1), 1); wrapped = index >= 0; }
    } else {
      const start = Math.min(max, from);
      index = scan(start, 0, -1);
      if (index < 0 && start < max) { index = scan(max, start + 1, -1); wrapped = index >= 0; }
    }
    if (index < 0) {
      if (this.searchSelection) this.selectionAnchor = this.selectionFocus = -1;
      this.searchSelection = false; this.searchMatch = null;
      this.redraw(); this.notify();
      return null;
    }
    this.selectionAnchor = index;
    this.selectionFocus = index + pattern.length - 1;
    this.searchMatch = { start: index, end: index + pattern.length - 1 };
    this.searchSelection = true;
    this.column = "hex";
    this.setCursor(index, { scroll: "visible" });
    return { index, wrapped };
  }

  record(op) {
    this.ops.push(op);
    if (this.ops.length > HEX_UNDO_LIMIT) { const gone = this.ops.shift(); if (gone.t !== "s") this.structuralLost++; }
    this.redoOps = [];
  }
  put(index, value, { remember = true } = {}) {
    const old = this.bytes[index];
    if (old === value) return;
    this.bytes[index] = value;
    if (this.structural === 0) {
      if (value === this.original[index]) this.changed.delete(index); else this.changed.add(index);
    }
    if (remember) this.record({ t: "s", i: index, a: old, b: value });
  }
  insert(index, value, { remember = true } = {}) {
    const next = new Uint8Array(this.bytes.length + 1);
    next.set(this.bytes.subarray(0, index), 0);
    next[index] = value;
    next.set(this.bytes.subarray(index), index + 1);
    this.bytes = next;
    this.structural++;
    if (remember) this.record({ t: "i", i: index, v: value });
    this.layout();
  }
  remove(index, { remember = true } = {}) {
    const value = this.bytes[index];
    const next = new Uint8Array(this.bytes.length - 1);
    next.set(this.bytes.subarray(0, index), 0);
    next.set(this.bytes.subarray(index + 1), index);
    this.bytes = next;
    this.structural++;
    if (remember) this.record({ t: "d", i: index, v: value });
    this.layout();
  }
  removeRange(start, end, { remember = true } = {}) {
    const removed = this.bytes.slice(start, end + 1);
    const next = new Uint8Array(this.bytes.length - removed.length);
    next.set(this.bytes.subarray(0, start), 0);
    next.set(this.bytes.subarray(end + 1), start);
    this.bytes = next;
    this.structural++;
    if (remember) this.record({ t: "r", i: start, v: removed });
    this.layout();
  }
  insertRange(index, values, { remember = true } = {}) {
    const insert = values instanceof Uint8Array ? values : Uint8Array.from(values);
    const next = new Uint8Array(this.bytes.length + insert.length);
    next.set(this.bytes.subarray(0, index), 0);
    next.set(insert, index);
    next.set(this.bytes.subarray(index), index + insert.length);
    this.bytes = next;
    this.structural++;
    if (remember) this.record({ t: "r", i: index, v: insert.slice() });
    this.layout();
  }
  deleteSelection() {
    const range = this.getSelectionRange();
    if (!this.editable || !range) return false;
    this.removeRange(range.start, range.end);
    this.cursor = Math.min(range.start, this.bytes.length);
    this.selectionAnchor = this.selectionFocus = -1;
    this.searchMatch = null; this.searchSelection = false; this.nibble = 0;
    this.redraw(); this.notify();
    return true;
  }
  resync() {
    if (this.structural !== 0) return;
    this.changed = new Set();
    for (let i = 0; i < this.bytes.length; i++) if (this.bytes[i] !== this.original[i]) this.changed.add(i);
  }
  apply(op, undo) {
    if (op.t === "s") this.put(op.i, undo ? op.a : op.b, { remember: false });
    else if (op.t === "i") { if (undo) this.remove(op.i, { remember: false }); else this.insert(op.i, op.v, { remember: false }); }
    else if (op.t === "d") { if (undo) this.insert(op.i, op.v, { remember: false }); else this.remove(op.i, { remember: false }); }
    else if (op.t === "r") { if (undo) this.insertRange(op.i, op.v, { remember: false }); else this.removeRange(op.i, op.i + op.v.length - 1, { remember: false }); }
  }
  countStructural() {
    this.structural = this.structuralLost + this.ops.reduce((n, op) => n + (op.t === "s" ? 0 : 1), 0);
  }
  undo() {
    const op = this.ops.pop();
    if (!op) return;
    this.apply(op, true);
    this.redoOps.push(op);
    this.countStructural();
    this.resync();
    this.selectionAnchor = this.selectionFocus = -1; this.searchMatch = null; this.searchSelection = false;
    this.setCursor(op.i);
  }
  redo() {
    const op = this.redoOps.pop();
    if (!op) return;
    this.apply(op, false);
    this.ops.push(op);
    this.countStructural();
    this.resync();
    this.selectionAnchor = this.selectionFocus = -1; this.searchMatch = null; this.searchSelection = false;
    this.setCursor(op.i + (op.t === "s" ? 1 : 0));
  }

  onMouse(event) {
    if (event.button !== 0) return;
    const cell = event.target.closest?.("i[data-i]");
    if (!cell) return;
    event.preventDefault();
    this.host.focus({ preventScroll: true });
    this.column = cell.dataset.c === "t" ? "text" : "hex";
    const rawAt = Number(cell.dataset.i);
    if (!this.bytes.length || rawAt >= this.bytes.length) {
      this.selectionAnchor = this.selectionFocus = -1;
      this.searchMatch = null; this.searchSelection = false;
      this.dragging = false;
      this.setCursor(rawAt);
      return;
    }
    const at = Math.max(0, rawAt);
    if (event.shiftKey) {
      if (this.selectionAnchor < 0) this.selectionAnchor = this.cursor;
      this.selectionFocus = at;
      this.searchMatch = null; this.searchSelection = false;
      this.dragging = false;
    } else {
      this.selectionAnchor = this.selectionFocus = at;
      this.searchMatch = null; this.searchSelection = false;
      this.dragging = true;
    }
    this.setCursor(at);
  }
  onMouseMove(event) {
    if (!this.dragging) return;
    const rect = this.host.getBoundingClientRect?.();
    if (rect && event.clientY >= rect.top && event.clientY <= rect.bottom) {
      const edge = Math.max(12, this.lineHeight);
      const maxScroll = Math.max(0, this.host.scrollHeight - this.host.clientHeight);
      if (event.clientY < rect.top + edge) this.host.scrollTop = Math.max(0, this.host.scrollTop - this.lineHeight);
      else if (event.clientY > rect.bottom - edge) this.host.scrollTop = Math.min(maxScroll, this.host.scrollTop + this.lineHeight);
    }
    const cell = document.elementFromPoint?.(event.clientX, event.clientY)?.closest?.("i[data-i]");
    if (!cell || !this.host.contains?.(cell)) return;
    const at = Math.min(Number(cell.dataset.i), Math.max(0, this.bytes.length - 1));
    if (at === this.selectionFocus) return;
    this.selectionFocus = at;
    this.setCursor(at);
  }
  onKey(event) {
    const { key } = event;
    const mod = event.ctrlKey || event.metaKey;
    const page = Math.max(1, Math.floor((this.host.clientHeight || 400) / this.lineHeight) - 1) * 16;
    const move = (to) => {
      event.preventDefault();
      const max = this.editable ? this.bytes.length : Math.max(0, this.bytes.length - 1);
      const next = Math.max(0, Math.min(max, to));
      if (event.shiftKey) {
        if (this.selectionAnchor < 0) this.selectionAnchor = this.cursor;
        this.selectionFocus = next;
        this.searchSelection = false;
      } else {
        this.selectionAnchor = this.selectionFocus = -1;
        this.searchMatch = null; this.searchSelection = false;
      }
      this.setCursor(next);
    };
    if (mod && key.toLowerCase() === "z") { event.preventDefault(); if (this.editable) { event.shiftKey ? this.redo() : this.undo(); } return; }
    if (mod && key.toLowerCase() === "y") { event.preventDefault(); if (this.editable) this.redo(); return; }
    if (mod && key.toLowerCase() === "f") { event.preventDefault(); this.searchInput?.focus(); this.searchInput?.select?.(); return; }
    if (mod && key.toLowerCase() === "a") { event.preventDefault(); this.selectAll(); return; }
    if (mod && key.toLowerCase() === "c" && this.getSelectionRange()) { event.preventDefault(); void this.copySelected(event.shiftKey ? "ascii" : "hex"); return; }
    if (mod && key === "Home") return move(0);
    if (mod && key === "End") return move(this.bytes.length);
    if (mod || event.altKey) return;
    switch (key) {
      case "ArrowLeft": return move(this.cursor - 1);
      case "ArrowRight": return move(this.cursor + 1);
      case "ArrowUp": return move(this.cursor - 16);
      case "ArrowDown": return move(this.cursor + 16);
      case "PageUp": return move(this.cursor - page);
      case "PageDown": return move(this.cursor + page);
      case "Home": return move(this.cursor - (this.cursor % 16));
      case "End": return move(this.cursor - (this.cursor % 16) + 15);
      case "Escape": this.dragging = false; this.searchMatch = null; this.clearSelection(); this.redraw(); return;
      case "Tab":
        if (this.editable && !event.shiftKey && this.column === "hex") { event.preventDefault(); this.column = "text"; this.nibble = 0; this.redraw(); }
        else if (this.editable && event.shiftKey && this.column === "text") { event.preventDefault(); this.column = "hex"; this.nibble = 0; this.redraw(); }
        return;
    }
    if (!this.editable) return;
    if (key === "Insert") { event.preventDefault(); this.selectionAnchor = this.selectionFocus = -1; this.searchMatch = null; this.searchSelection = false; this.insert(this.cursor, 0); this.redraw(); this.notify(); return; }
    if (key === "Delete") {
      event.preventDefault();
      if (this.deleteSelection()) return;
      if (this.cursor < this.bytes.length) { this.searchMatch = null; this.searchSelection = false; this.remove(this.cursor); this.cursor = Math.min(this.cursor, this.bytes.length); this.redraw(); this.notify(); }
      return;
    }
    if (key === "Backspace") {
      event.preventDefault();
      if (this.deleteSelection()) return;
      if (this.cursor > 0) { this.searchMatch = null; this.searchSelection = false; this.remove(this.cursor - 1); this.cursor--; this.nibble = 0; this.redraw(); this.notify(); }
      return;
    }
    if (key.length !== 1) return;
    if (this.column === "hex") {
      if (!/^[0-9a-fA-F]$/.test(key)) return;
      event.preventDefault();
      this.selectionAnchor = this.selectionFocus = -1; this.searchMatch = null; this.searchSelection = false;
      const digit = parseInt(key, 16);
      if (this.cursor === this.bytes.length) this.insert(this.cursor, 0);
      const old = this.bytes[this.cursor];
      this.put(this.cursor, this.nibble === 0 ? (digit << 4) | (old & 0x0f) : (old & 0xf0) | digit);
      if (this.nibble === 0) { this.nibble = 1; this.redraw(); this.notify(); }
      else this.setCursor(this.cursor + 1);
      return;
    }
    const code = key.charCodeAt(0);
    if (code < 32 || code > 126) return;
    event.preventDefault();
    this.selectionAnchor = this.selectionFocus = -1; this.searchMatch = null; this.searchSelection = false;
    if (this.cursor === this.bytes.length) this.insert(this.cursor, code); else this.put(this.cursor, code);
    this.setCursor(this.cursor + 1);
  }
}
