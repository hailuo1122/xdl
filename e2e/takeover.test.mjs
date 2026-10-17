import { chromium } from "playwright-core";
import { spawn, execFileSync } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import assert from "node:assert/strict";
import os from "node:os";
import path from "node:path";

const PIPE = String.fromCharCode(92, 92, 46, 92, 112, 105, 112, 101, 92, 120, 100, 108, 46, 118, 49);
const EXT_ID = "jkmmncllhpjaklglnieoaacgchndkgkd";
const TEST_URL = "http://127.0.0.1:18080/m2/page";
const CHIPS_URL = "http://localhost:18080/m2/chips-page";
const BROWSER = (process.env.XDL_E2E_BROWSER || "msedge").toLowerCase();
const extensionPath = process.env.XDL_EXTENSION_PATH || path.resolve("../extension/.output/chrome-mv3");
const evidenceDir = process.env.XDL_E2E_EVIDENCE || path.join(process.env.LOCALAPPDATA || os.tmpdir(), "XDL-dev", "evidence");
const profile = process.env.XDL_E2E_PROFILE || path.join(os.tmpdir(), `xdl-e2e-${BROWSER}-profile`);
const downloadsPath = process.env.XDL_E2E_DOWNLOADS || path.join(os.tmpdir(), `xdl-e2e-${BROWSER}-downloads`);
const fallbackMode = process.env.XDL_E2E_FALLBACK === "1";
const dataDir = process.env.XDL_DATA_DIR || "";
const appExe = process.env.XDL_APP_EXE || path.join(process.env.LOCALAPPDATA || "", "XDL-dev", "xdl.exe");
const headful = process.env.XDL_E2E_HEADFUL === "1";

const DEFAULTS = {
  mode: "smart",
  minSize: 1024 * 1024,
  exclude: [],
  httpsOnly: false,
  mediaSniff: true,
  notifyFallback: true,
};

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function findChromeForTesting() {
  if (process.env.XDL_CHROME_BIN) return process.env.XDL_CHROME_BIN;
  const base = path.join(process.env.LOCALAPPDATA || "", "XDL-dev", "browsers", "chrome");
  try {
    const versions = fs
      .readdirSync(base)
      .filter((name) => name.startsWith("win64-"))
      .sort()
      .reverse();
    for (const v of versions) {
      const exe = path.join(base, v, "chrome-win64", "chrome.exe");
      if (fs.existsSync(exe)) return exe;
    }
  } catch {
    /* not installed */
  }
  return "";
}

function launchOptions() {
  const opts = {
    headless: !headful,
    viewport: { width: 1280, height: 800 },
    downloadsPath,
    acceptDownloads: true,
    args: [
      `--disable-extensions-except=${extensionPath}`,
      `--load-extension=${extensionPath}`,
      "--no-first-run",
      "--no-default-browser-check",
    ],
  };
  if (BROWSER === "chrome" || BROWSER === "chromium") {
    const exe = findChromeForTesting();
    if (!exe) {
      throw new Error(
        "Chrome for Testing not found. Run: npx --yes @puppeteer/browsers install chrome@stable --path %LOCALAPPDATA%\\XDL-dev\\browsers",
      );
    }
    opts.executablePath = exe;
  } else {
    opts.channel = BROWSER === "edge" ? "msedge" : "msedge";
  }
  return opts;
}

function rpc(method, params = {}, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const socket = net.connect(PIPE);
    let buf = Buffer.alloc(0);
    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error(`pipe timeout: ${method}`));
    }, timeoutMs);
    socket.on("connect", () => {
      const payload = Buffer.from(JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }));
      const len = Buffer.alloc(4);
      len.writeUInt32LE(payload.length);
      socket.write(Buffer.concat([len, payload]));
    });
    socket.on("data", (chunk) => {
      buf = Buffer.concat([buf, chunk]);
      if (buf.length < 4) return;
      const n = buf.readUInt32LE(0);
      if (buf.length < 4 + n) return;
      const msg = JSON.parse(buf.subarray(4, 4 + n));
      clearTimeout(timer);
      socket.destroy();
      if (msg.error) reject(new Error(msg.error.message));
      else resolve(msg.result);
    });
    socket.on("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });
}

async function waitFor(fn, description, timeoutMs = 20000) {
  const start = Date.now();
  let last;
  while (Date.now() - start < timeoutMs) {
    try {
      const value = await fn();
      if (value) return value;
    } catch (err) {
      last = err;
    }
    await sleep(350);
  }
  throw new Error(`timeout waiting for ${description}${last ? ` (${last.message})` : ""}`);
}

async function waitForNewTask(baseline, matcher, description, timeoutMs = 30000) {
  const started = new Set(baseline.map((t) => t.id));
  return waitFor(async () => {
    const tasks = await rpc("download.list");
    return tasks.find((t) => !started.has(t.id) && matcher(t)) || null;
  }, description, timeoutMs);
}

async function waitForNewTaskLoose(baseline, matcher, description, timeoutMs = 40000) {
  const started = new Set(baseline.map((t) => t.id));
  return waitFor(async () => {
    try {
      const tasks = await rpc("download.list");
      return tasks.find((t) => !started.has(t.id) && matcher(t)) || null;
    } catch {
      return null;
    }
  }, description, timeoutMs);
}

async function assertNoNewTask(baseline, matcher, description, waitMs = 2500) {
  await sleep(waitMs);
  const tasks = await rpc("download.list");
  const started = new Set(baseline.map((t) => t.id));
  const extra = tasks.filter((t) => !started.has(t.id) && matcher(t));
  assert.equal(extra.length, 0, `${description}: unexpected tasks ${extra.map((t) => t.name).join(", ")}`);
  return tasks;
}

async function waitDone(task, timeoutMs = 30000) {
  return waitFor(async () => {
    const tasks = await rpc("download.list");
    const t = tasks.find((x) => x.id === task.id);
    if (!t) return null;
    if (t.state === "done") return t;
    if (t.state === "failed" || t.state === "canceled") {
      throw new Error(`task ${t.name} ended ${t.state}: ${t.err?.message || ""}`);
    }
    return null;
  }, `task ${task.name} to finish`, timeoutMs);
}

async function setSettings(sw, patch) {
  const current = await sw.evaluate(async () => {
    const data = await chrome.storage.local.get("xdl.settings");
    return data["xdl.settings"] || null;
  });
  const next = { ...DEFAULTS, ...(current || {}), ...patch };
  await sw.evaluate(async (settings) => chrome.storage.local.set({ "xdl.settings": settings }), next);
  await sleep(450);
  return next;
}

async function browserDownloads(sw, part) {
  return sw.evaluate(async (needle) => {
    const all = await chrome.downloads.search({});
    return all
      .filter((d) => (d.url || "").includes(needle) || (d.finalUrl || "").includes(needle))
      .map((d) => ({
        id: d.id,
        state: d.state,
        paused: d.paused,
        filename: d.filename,
        totalBytes: d.totalBytes,
        byExtensionId: d.byExtensionId,
        error: d.error,
      }));
  }, part);
}

async function waitBrowserDownload(sw, part, predicate, description, timeoutMs = 30000) {
  return waitFor(async () => {
    const list = await browserDownloads(sw, part);
    return list.find(predicate) || null;
  }, description, timeoutMs);
}

async function toastText(page) {
  return page.evaluate(() => {
    const shadow = document.querySelector("#xdl-media-host")?.shadowRoot;
    return shadow?.querySelector(".toast .txt")?.textContent || "";
  });
}

async function waitToast(page, needle, timeoutMs = 6000) {
  return waitFor(async () => {
    const text = await toastText(page);
    return text.includes(needle) ? text : null;
  }, `toast containing ${needle}`, timeoutMs);
}

async function askNotificationIds(sw) {
  // notifications.getAll() is a map of id -> true, not notification details.
  const all = await sw.evaluate(async () => await chrome.notifications.getAll());
  return Object.keys(all).filter((id) => id.startsWith("xdl-ask-"));
}

async function screenshot(page, name) {
  fs.mkdirSync(evidenceDir, { recursive: true });
  const file = path.join(evidenceDir, `${BROWSER}-${name}.png`);
  await page.screenshot({ path: file, fullPage: false }).catch(() => {});
  return file;
}

/* ------------------------------------------------------------------ */
/* fallback lifecycle helpers (opt-in; app must be started by caller)  */
/* ------------------------------------------------------------------ */

function ps(script) {
  return execFileSync("powershell", ["-NoProfile", "-Command", script], { encoding: "utf8" }).trim();
}

async function stopApp() {
  ps(
    `Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'xdl.exe' -and $_.ExecutablePath -eq '${appExe.replace(/'/g, "''")}' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`,
  );
  await waitFor(async () => {
    try {
      await rpc("app.ping", {}, 1000);
      return null;
    } catch {
      return true;
    }
  }, "app to stop", 15000);
}

async function startApp() {
  const child = spawn(appExe, ["--minimized"], {
    detached: true,
    stdio: "ignore",
    env: { ...process.env, XDL_DATA_DIR: dataDir },
  });
  child.unref();
  await waitFor(async () => {
    try {
      await rpc("app.ping", {}, 1500);
      return true;
    } catch {
      return null;
    }
  }, "app to start", 20000);
}

/* ------------------------------------------------------------------ */

fs.rmSync(profile, { recursive: true, force: true });
fs.rmSync(downloadsPath, { recursive: true, force: true });
fs.mkdirSync(profile, { recursive: true });
fs.mkdirSync(downloadsPath, { recursive: true });
fs.mkdirSync(evidenceDir, { recursive: true });

const appWasOnline = await rpc("app.ping").then(
  () => true,
  () => false,
);
if (!appWasOnline) {
  throw new Error("XDL is not running (named pipe unavailable). Start the built app first.");
}

const baseline = await rpc("download.list");
let context;
let page;
let sw;
let extPage;
let extId = "";

const report = [];
function pass(name, detail = "") {
  report.push({ name, detail });
  console.log(`PASS ${name}${detail ? " · " + detail : ""}`);
}

try {
  context = await chromium.launchPersistentContext(profile, launchOptions());
  sw = context.serviceWorkers()[0];
  if (!sw) sw = await context.waitForEvent("serviceworker", { timeout: 20000 });
  extId = await sw.evaluate(() => chrome.runtime.id);
  assert.equal(extId, EXT_ID, "fixed extension ID");
  const ua = await sw.evaluate(() => navigator.userAgent);
  console.log(`BROWSER ${BROWSER} · ${ua}`);
  pass("browser extension loaded", `${BROWSER} · ${extId}`);

  extPage = await context.newPage();
  await extPage.goto(`chrome-extension://${extId}/options.html`);
  await extPage.waitForTimeout(400);

  await context.addCookies([{ name: "xdl_test", value: "1", domain: "127.0.0.1", path: "/" }]);
  page = await context.newPage();
  await page.goto(TEST_URL);
  await page.waitForTimeout(1200);

  /* 1. click takeover with Cookie + Referer */
  await page.click("#guarded");
  let guarded = await waitForNewTask(baseline, (t) => t.url.includes("/m2/guarded/"), "guarded click task");
  guarded = await waitDone(guarded);
  assert.equal(guarded.size, 3145728);
  assert.equal(guarded.name, "m2-guarded.bin");
  assert.ok(fs.existsSync(guarded.finalPath));
  pass("click takeover (Cookie + Referer)", `${guarded.name} · ${guarded.size} bytes · ${guarded.state}`);
  await screenshot(page, "click-takeover");

  /* 2. browser-initiated download takeover */
  let before = await rpc("download.list");
  await page.click("#attach");
  let attached = await waitForNewTask(before, (t) => t.url.includes("/m2/attach/"), "downloads takeover task");
  attached = await waitDone(attached);
  assert.match(attached.name, /^test-2621440/, "server Content-Disposition filename wins");
  assert.ok(fs.existsSync(attached.finalPath));
  pass("browser download takeover", `${attached.name} · ${attached.size} bytes`);

  /* 3. Authorization header forwarding */
  await page.waitForTimeout(500);
  before = await rpc("download.list");
  await page.click("#auth");
  let authTask = await waitForNewTask(before, (t) => t.url.includes("/m2/auth/"), "auth task");
  authTask = await waitDone(authTask);
  assert.equal(authTask.name, "m2-auth.bin");
  pass("Authorization header forwarding", `${authTask.name} · ${authTask.state}`);

  /* 4. media panel direct MP4 */
  before = await rpc("download.list");
  await page.evaluate(() => {
    const shadow = document.querySelector("#xdl-media-host")?.shadowRoot;
    shadow?.querySelector(".pill")?.click();
  });
  await page.waitForTimeout(400);
  const clicked = await page.evaluate(() => {
    const shadow = document.querySelector("#xdl-media-host")?.shadowRoot;
    const rows = [...(shadow?.querySelectorAll(".item") || [])];
    const row = rows.find((r) => r.textContent?.includes("video.mp4"));
    const button = [...(row?.querySelectorAll(".act") || [])].find((b) => b.textContent?.includes("交给 XDL"));
    button?.click();
    return !!button;
  });
  assert.ok(clicked, "media panel send action");
  let video = await waitForNewTask(before, (t) => t.url.endsWith("/m2/video.mp4"), "media handoff task");
  video = await waitDone(video);
  pass("media panel MP4 handoff", `${video.name} · ${video.size} bytes`);
  await screenshot(page, "media-panel");

  /* 5. HLS rejection for explicit paths */
  const hls = await extPage.evaluate(
    async (url) => await chrome.runtime.sendMessage({ type: "media.send", url, pageUrl: "http://127.0.0.1:18080/m2/page" }),
    "http://127.0.0.1:18080/m2/hls.m3u8",
  );
  assert.equal(hls.ok, false);
  assert.equal(hls.code, "VIDEO_PENDING");
  pass("HLS rejected with next-version message", hls.error);

  /* 6. size threshold via credentialed HEAD */
  await setSettings(sw, { minSize: 3 * 1024 * 1024, mode: "smart" });
  before = await rpc("download.list");
  const smallBefore = new Set((await browserDownloads(sw, "/file/524288")).map((d) => d.id));
  await page.click("#small");
  const smallBrowser = await waitBrowserDownload(
    sw,
    "/file/524288",
    (d) => !smallBefore.has(d.id) && d.state === "complete",
    "small file downloaded by browser",
    20000,
  );
  assert.ok(fs.existsSync(smallBrowser.filename));
  await assertNoNewTask(before, (t) => t.url.includes("/file/524288"), "below-threshold stays in browser");
  const thresholdToast = await waitToast(page, "小于");
  pass("size threshold falls back to browser", `${smallBrowser.filename} · ${thresholdToast}`);
  await setSettings(sw, { minSize: 1024 * 1024 });

  /* 7. HEAD failure means unknown size -> takeover allowed */
  await setSettings(sw, { minSize: 3 * 1024 * 1024 });
  before = await rpc("download.list");
  await page.click("#headfail");
  let headfail = await waitForNewTask(before, (t) => t.url.includes("/m2/headfail/"), "head 405 task");
  headfail = await waitDone(headfail);
  pass("HEAD failure allows takeover", `${headfail.name} · ${headfail.size} bytes`);
  await setSettings(sw, { minSize: 1024 * 1024 });

  /* 8. exclusion: automatic path stays in browser, explicit path is forced */
  await setSettings(sw, { exclude: ["127.0.0.1"] });
  before = await rpc("download.list");
  const plainBefore = new Set((await browserDownloads(sw, "/file/5242880")).map((d) => d.id));
  await page.click("#plain");
  await waitBrowserDownload(
    sw,
    "/file/5242880",
    (d) => !plainBefore.has(d.id) && d.state === "complete",
    "excluded browser download",
    20000,
  );
  await assertNoNewTask(before, (t) => t.url.includes("/file/5242880"), "excluded site not auto-taken");
  const forced = await extPage.evaluate(
    async (url) =>
      await chrome.runtime.sendMessage({
        type: "link-takeover",
        url,
        pageUrl: "http://127.0.0.1:18080/m2/page",
        force: true,
      }),
    "http://127.0.0.1:18080/m2/guarded/1572864",
  );
  assert.equal(forced.ok, true, `forced takeover failed: ${forced.error || ""}`);
  let forcedTask = await waitDone(forced.task);
  pass("excluded site: explicit takeover forced", `${forcedTask.name} · ${forcedTask.state}`);
  await setSettings(sw, { exclude: [] });

  /* 9. HTTPS-only applies to automatic and explicit paths */
  await setSettings(sw, { httpsOnly: true });
  before = await rpc("download.list");
  const plainHttpsBefore = new Set((await browserDownloads(sw, "/file/5242880")).map((d) => d.id));
  await page.click("#plain");
  await waitBrowserDownload(
    sw,
    "/file/5242880",
    (d) => !plainHttpsBefore.has(d.id) && d.state === "complete",
    "https-only browser download",
    20000,
  );
  await assertNoNewTask(before, (t) => t.url.includes("/file/5242880"), "https-only blocks auto takeover");
  const httpsBlocked = await extPage.evaluate(
    async (url) =>
      await chrome.runtime.sendMessage({
        type: "link-takeover",
        url,
        pageUrl: "http://127.0.0.1:18080/m2/page",
        force: true,
      }),
    "http://127.0.0.1:18080/m2/guarded/1835008",
  );
  assert.equal(httpsBlocked.ok, false);
  assert.equal(httpsBlocked.code, "HTTPS_ONLY");
  pass("HTTPS-only applies to explicit takeover", httpsBlocked.error);
  await setSettings(sw, { httpsOnly: false });

  /* 10. ask mode for chrome.downloads */
  await setSettings(sw, { mode: "ask" });

  async function triggerAsk(part, selector) {
    const existing = new Set((await browserDownloads(sw, part)).map((d) => d.id));
    await page.click(selector);
    let item;
    try {
      item = await waitFor(async () => {
        const list = await browserDownloads(sw, part);
        return list.find((d) => !existing.has(d.id) && d.paused) || null;
      }, "asked download to pause", 20000);
    } catch (err) {
      const st = await extPage.evaluate(async () => await chrome.runtime.sendMessage({ type: "state.get" }));
      const list = await browserDownloads(sw, part);
      console.log("ASK DEBUG settings", JSON.stringify(st?.state?.settings), "downloads", JSON.stringify(list));
      throw err;
    }
    const ids = await waitFor(async () => {
      const list = await askNotificationIds(sw);
      return list.length ? list : null;
    }, "ask notification", 8000);
    return { item, notificationId: ids[0] };
  }

  // 10a. choose XDL
  before = await rpc("download.list");
  const ask1 = await triggerAsk("/m2/slowattach/2621440", "#slowattach");
  await extPage.evaluate(
    async ({ id }) => await chrome.runtime.sendMessage({ type: "ask.answer", notificationId: id, choice: "xdl" }),
    { id: ask1.notificationId },
  );
  let askTask = await waitForNewTask(before, (t) => t.url.includes("/m2/slowattach/2621440"), "ask xdl task", 30000);
  askTask = await waitDone(askTask);
  await waitFor(async () => {
    const list = await browserDownloads(sw, "/m2/slowattach/2621440");
    return list.some((d) => d.id === ask1.item.id) ? null : true;
  }, "asked browser item erased", 10000);
  pass("ask mode: use XDL", `${askTask.name} · browser item erased`);

  // 10b. keep browser download
  before = await rpc("download.list");
  const ask2 = await triggerAsk("/m2/slowattach/2621441", "#slowattach2");
  await extPage.evaluate(
    async ({ id }) => await chrome.runtime.sendMessage({ type: "ask.answer", notificationId: id, choice: "browser" }),
    { id: ask2.notificationId },
  );
  const kept = await waitBrowserDownload(
    sw,
    "/m2/slowattach/2621441",
    (d) => d.id === ask2.item.id && d.state === "complete",
    "asked download resumed in browser",
    25000,
  );
  assert.ok(fs.existsSync(kept.filename));
  await assertNoNewTask(before, (t) => t.url.includes("/m2/slowattach/2621441"), "keep-browser creates no task");
  pass("ask mode: keep browser", kept.filename);

  // 10c. 30s no answer -> resume
  before = await rpc("download.list");
  const ask3 = await triggerAsk("/m2/slowattach/2621442", "#slowattach3");
  const resumed = await waitBrowserDownload(
    sw,
    "/m2/slowattach/2621442",
    (d) => d.id === ask3.item.id && d.state === "complete",
    "ask timeout resumes and completes",
    45000,
  );
  assert.ok(fs.existsSync(resumed.filename));
  await assertNoNewTask(before, (t) => t.url.includes("/m2/slowattach/2621442"), "ask timeout creates no task");
  const leftoverNotes = await askNotificationIds(sw);
  assert.equal(leftoverNotes.length, 0, "ask notification cleared after timeout");
  pass("ask mode: 30s timeout auto-resume", resumed.filename);
  await setSettings(sw, { mode: "smart" });

  /* 11. CHIPS partitioned cookie */
  const chipsPage = await context.newPage();
  await chipsPage.goto(CHIPS_URL);
  await chipsPage.waitForTimeout(1500);
  const partitioned = await sw.evaluate(async () => {
    try {
      const list = await chrome.cookies.getAll({
        url: "http://127.0.0.1:18080/m2/chips/2097152",
        partitionKey: { topLevelSite: "http://localhost:18080" },
      });
      return list.map((c) => ({ name: c.name, value: c.value, partitionKey: c.partitionKey }));
    } catch (err) {
      return [{ error: String(err) }];
    }
  });
  console.log("CHIPS", JSON.stringify(partitioned));
  if (partitioned.some((c) => c.name === "xdl_chips")) {
    before = await rpc("download.list");
    await chipsPage.click("#chips");
    let chips = await waitForNewTask(before, (t) => t.url.includes("/m2/chips/"), "chips task");
    chips = await waitDone(chips);
    pass("CHIPS partitioned cookie forwarded", `${chips.name} · ${chips.state}`);
  } else {
    console.log("SKIP CHIPS partitioned cookie not visible to the extension in this browser");
  }

  /* fallback scenarios (mutating; opt-in) */
  if (fallbackMode) {
    if (!dataDir) throw new Error("XDL_E2E_FALLBACK requires XDL_DATA_DIR");
    const nativeDir = path.join(dataDir, "native");
    const hostExe = path.join(nativeDir, "xdl-native-host.exe");
    const hostBak = hostExe + ".bak";
    const appPathFile = path.join(nativeDir, "app-path.txt");
    const originalAppPath = fs.existsSync(appPathFile) ? fs.readFileSync(appPathFile, "utf8") : "";
    let hostRenamed = false;
    let appPathChanged = false;
    try {
      await page.goto(TEST_URL);
      await page.waitForTimeout(800);

      /* C. cold start from a browser click */
      const baselineC = await rpc("download.list");
      await stopApp();
      await extPage.evaluate(async () => await chrome.runtime.sendMessage({ type: "state.refresh" }));
      await sleep(600);
      await page.click("#plain");
      let cold = await waitForNewTaskLoose(
        baselineC,
        (t) => t.url.includes("/file/5242880"),
        "cold-start click task",
        45000,
      );
      cold = await waitDone(cold, 45000);
      pass("fallback C: app cold-starts from click", `${cold.name} · ${cold.state}`);
      const afterC = await rpc("download.list");

      /* A. host missing */
      await stopApp();
      try {
        fs.rmSync(hostBak, { force: true });
        fs.renameSync(hostExe, hostBak);
      } catch (err) {
        throw new Error(`could not hide native host: ${err.message}`);
      }
      hostRenamed = true;
      await extPage.evaluate(async () => await chrome.runtime.sendMessage({ type: "state.refresh" }));
      await sleep(600);
      const hostMissingBefore = new Set((await browserDownloads(sw, "/file/5242880")).map((d) => d.id));
      await page.click("#plain");
      const fallbackA = await waitBrowserDownload(
        sw,
        "/file/5242880",
        (d) => !hostMissingBefore.has(d.id) && d.state === "complete",
        "host-missing browser fallback download",
        30000,
      );
      const toastA = await waitToast(page, "浏览器组件", 8000).catch(() => toastText(page));
      assert.ok(
        toastA.includes("浏览器组件") || toastA.includes("未安装") || toastA.includes("XDL 未接管"),
        `expected Chinese fallback hint, got: ${toastA}`,
      );
      assert.ok(fs.existsSync(fallbackA.filename));
      pass("fallback A: host missing -> browser", `${fallbackA.filename} · "${toastA}"`);
      fs.renameSync(hostBak, hostExe);
      hostRenamed = false;

      /* B. host exists, app launch fails */
      fs.writeFileSync(appPathFile, "C:\\Windows\\System32\\whoami.exe\r\n", "utf8");
      appPathChanged = true;
      await extPage.evaluate(async () => await chrome.runtime.sendMessage({ type: "state.refresh" }));
      await sleep(600);
      const beforeB = await browserDownloads(sw, "/file/5242880");
      await page.click("#plain");
      const fallbackB = await waitBrowserDownload(
        sw,
        "/file/5242880",
        (d) => !beforeB.some((x) => x.id === d.id) && d.state === "complete",
        "app-start-failure browser fallback",
        40000,
      );
      assert.ok(fs.existsSync(fallbackB.filename));
      const toastB = await toastText(page);
      assert.ok(
        toastB.includes("XDL 未接管") || toastB.includes("启动") || toastB.includes("未响应"),
        `expected Chinese failure hint, got: ${toastB}`,
      );
      pass("fallback B: app launch failure -> browser", `${fallbackB.filename} · "${toastB}"`);

      const attachBefore = new Set((await browserDownloads(sw, "/m2/slowattach/2621441")).map((d) => d.id));
      await page.click("#slowattach2");
      const pausedB = await waitFor(async () => {
        const list = await browserDownloads(sw, "/m2/slowattach/2621441");
        return list.find((d) => !attachBefore.has(d.id) && d.paused) || null;
      }, "fallback B download paused", 20000);
      const resumedB = await waitBrowserDownload(
        sw,
        "/m2/slowattach/2621441",
        (d) => d.id === pausedB.id && d.state === "complete",
        "fallback B browser download resumes",
        45000,
      );
      assert.ok(fs.existsSync(resumedB.filename));
      const attachAfter = await browserDownloads(sw, "/m2/slowattach/2621441");
      assert.equal(attachAfter.filter((d) => d.paused).length, 0, "no paused browser downloads left");
      pass("fallback B: download resumes without zombie", resumedB.filename);

      fs.writeFileSync(appPathFile, originalAppPath, "utf8");
      appPathChanged = false;
      await startApp();
      const afterFallbacks = await rpc("download.list");
      const newIDs = new Set(afterFallbacks.map((t) => t.id));
      for (const t of afterC) assert.ok(newIDs.has(t.id), "task list must survive restart");
      const unexpected = afterFallbacks.filter(
        (t) => !afterC.some((x) => x.id === t.id) && (t.url.includes("/file/5242880") || t.url.includes("/m2/attach/") || t.url.includes("/m2/slowattach/")),
      );
      assert.equal(unexpected.length, 0, "fallback paths must not create XDL tasks");
      pass("fallback: no duplicate/zombie XDL tasks after restart", `${afterFallbacks.length} tasks`);
    } finally {
      if (hostRenamed) {
        try {
          fs.renameSync(hostBak, hostExe);
        } catch {
          /* best effort */
        }
      }
      if (appPathChanged) {
        try {
          fs.writeFileSync(appPathFile, originalAppPath, "utf8");
        } catch {
          /* best effort */
        }
      }
    }
  }

  const evidence = {
    browser: BROWSER,
    userAgent: sw ? await sw.evaluate(() => navigator.userAgent) : "",
    extensionId: extId,
    finishedAt: new Date().toISOString(),
    checks: report,
  };
  fs.writeFileSync(path.join(evidenceDir, `${BROWSER}-takeover.json`), JSON.stringify(evidence, null, 2));
  console.log(`All takeover checks passed (${report.length}). Evidence: ${evidenceDir}`);
} finally {
  await context?.close().catch(() => {});
}
