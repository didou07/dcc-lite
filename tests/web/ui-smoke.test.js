// Opens the real interface in Chromium (Playwright) with a stub /api/ and fails on any
// JavaScript error, failed request, broken tab, or horizontal overflow at phone width.
// Needs: npm install playwright && npx playwright install chromium
const http = require("http"), fs = require("fs"), path = require("path");
const { chromium } = require("playwright");

const ROOT = path.join(__dirname, "../../src/web");
const MIME = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".webp": "image/webp", ".png": "image/png", ".woff2": "font/woff2", ".ico": "image/x-icon" };
const LANGS = ["en", "ar", "fr", "es", "de", "tr", "ru", "zh"];
const VIEWS = ["devices", "files", "terminal", "screen", "logs"];

// Static files from src/web; every /api/ call answers with empty JSON.
const server = http.createServer((req, res) => {
  const url = decodeURIComponent(req.url.split("?")[0]);
  if (url.startsWith("/api/")) {
    const list = /profiles|transfers|faults|ranges|places|sessions/.test(url);
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end(JSON.stringify(list ? [] : url.endsWith("health") ? { ok: true, version: "test" } : {}));
  }
  const file = path.join(ROOT, url === "/" ? "index.html" : url);
  if (!file.startsWith(ROOT) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) { res.writeHead(404); return res.end("not found"); }
  res.writeHead(200, { "Content-Type": MIME[path.extname(file)] || "application/octet-stream" });
  fs.createReadStream(file).pipe(res);
});

(async () => {
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = "http://127.0.0.1:" + server.address().port;
  const browser = await chromium.launch();
  const problems = [];

  for (const lang of LANGS) for (const route of ["/", "/login.html"]) {
    const tag = `${lang}${route}`;
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    await ctx.addInitScript((l) => { try { localStorage.setItem("dcclite.lang", l); } catch { } }, lang);
    const page = await ctx.newPage();
    page.on("pageerror", (e) => problems.push(`[${tag}] page error: ${e.message}`));
    page.on("console", (m) => { if (m.type() === "error") problems.push(`[${tag}] console error: ${m.text()}`); });
    page.on("requestfailed", (r) => problems.push(`[${tag}] request failed: ${r.url()}`));
    page.on("response", (r) => { if (r.status() >= 400 && !r.url().includes("/api/")) problems.push(`[${tag}] HTTP ${r.status()} ${r.url()}`); });
    await page.goto(base + route, { waitUntil: "networkidle" });

    if (route === "/") {
      for (const v of VIEWS) {
        await page.click(`.tab[data-view="${v}"]`);
        await page.waitForTimeout(150);
        const current = await page.evaluate(() => document.body.dataset.view);
        if (current !== v) problems.push(`[${tag}] tab ${v} did not open (view=${current})`);
      }
      // The Tools tab opens whichever tool page was used last, so visit each tool page by id.
      const ids = await page.evaluate(() => TOOL_PAGES.map((t) => t.id));
      for (const id of ids) {
        await page.evaluate((i) => showTool(i), id);
        await page.waitForTimeout(150);
        const tab = await page.evaluate(() => document.querySelector('#mainTabs .tab[aria-selected="true"]')?.dataset.view);
        if (tab !== "tools") problems.push(`[${tag}] tool page ${id}: Tools tab not selected (${tab})`);
      }
      await page.click('.tab[data-view="devices"]');
      await page.locator("button:visible", { hasText: /./ }).first().waitFor();
      await page.evaluate(() => document.getElementById("deviceAdd")?.click());
      await page.waitForTimeout(250);
      const open = await page.evaluate(() => document.getElementById("deviceDialog")?.open);
      if (!open) problems.push(`[${tag}] add-device dialog did not open`);
    }

    await page.setViewportSize({ width: 390, height: 800 });
    await page.waitForTimeout(250);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (overflow > 1) problems.push(`[${tag}] horizontal overflow at 390px: ${overflow}px`);
    await ctx.close();
  }

  await browser.close();
  server.close();
  const unique = [...new Set(problems)];
  if (unique.length) { console.error(unique.join("\n")); process.exit(1); }
  console.log(`ui smoke: ${LANGS.length} languages x 2 pages, no problems`);
})().catch((e) => { console.error(e); process.exit(1); });
