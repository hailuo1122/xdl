import { DEFAULT_SETTINGS, type AppState, type AskChoice, type BgMessage, type ContentState, type ExtensionSettings, type MediaCandidate, type TakeoverResult, type TaskInfo } from "../lib/types";
import { isExcluded, loadSettings, saveSettings, settingsFromStorageValue } from "../lib/settings";
import { XDLError, nativeCall, pingApp } from "../lib/native";
import {
  baseName,
  captureRequestHeaders,
  cookieHeaderFor,
  extOf,
  formatBytes,
  isHTMLMime,
  isHTTPUrl,
  isMediaURL,
  lookupCaptured,
  mediaCandidate,
  mediaKindFor,
} from "../lib/context";

const ICON_URL = () => chrome.runtime.getURL("icon/128.png");

let currentSettings: ExtensionSettings = { ...DEFAULT_SETTINGS };
let appState: AppState = { hostPresent: null, appOnline: false, checkedAt: 0 };
let ready: Promise<void> = Promise.resolve();

const consideredDownloads = new Set<number>();
const recentSubmitted = new Map<string, number>();
const mediaByTab = new Map<number, MediaCandidate[]>();
const mediaLoaded = new Set<number>();
const askPending = new Map<string, AskPending>();

const ASK_STORAGE_KEY = "xdl.ask.pending";
const ASK_TIMEOUT_MS = 45000;

interface AskPending {
  notificationId: string;
  downloadId: number;
  name: string;
  message: string;
  expiresAt: number;
  dir?: string;
}

function mediaKey(tabId: number) {
  return `xdl.media.${tabId}`;
}

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function errCode(err: unknown): string {
  return err instanceof XDLError ? err.code : "NATIVE_ERROR";
}

function errMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function friendlyError(code: string, raw: string): string {
  switch (code) {
    case "HOST_MISSING":
      return "浏览器组件未安装或已损坏，请在 XDL 设置里重新注册";
    case "HOST_FORBIDDEN":
      return "浏览器扩展未获授权，请重新注册浏览器组件";
    case "HOST_EXITED":
      return "浏览器组件异常退出";
    case "APP_OFFLINE":
      return "XDL 未运行或未响应";
    case "APP_STARTING":
      return "XDL 启动超时";
    case "NATIVE_TIMEOUT":
      return "XDL 响应超时";
    case "EXCLUDED":
      return "该站点已排除接管";
    case "HTTPS_ONLY":
      return "当前设置只接管 HTTPS 下载";
    case "UNSUPPORTED_URL":
      return "只支持 http(s) 下载链接";
    case "VIDEO_PENDING":
      return "视频任务提交失败";
    default:
      return raw || "未知错误";
  }
}

/* ------------------------------------------------------------------ */
/* native bridge                                                       */
/* ------------------------------------------------------------------ */

function applyNativeFailure(err: unknown) {
  const code = errCode(err);
  const hostPresent = !["HOST_MISSING", "HOST_FORBIDDEN", "HOST_INVALID"].includes(code);
  appState = {
    hostPresent,
    appOnline: false,
    errorCode: code,
    lastError: errMessage(err),
    checkedAt: Date.now(),
  };
  void broadcastState();
}

function applyNativeSuccess(version?: string, extensionId?: string) {
  const wasOnline = appState.hostPresent === true && appState.appOnline;
  appState = {
    hostPresent: true,
    appOnline: true,
    version: version || appState.version,
    extensionId: extensionId || appState.extensionId,
    checkedAt: Date.now(),
  };
  if (!wasOnline) void broadcastState();
}

let stateInflight: Promise<AppState> | null = null;

async function refreshAppState(launch = false): Promise<AppState> {
  if (stateInflight) return stateInflight;
  stateInflight = (async () => {
    try {
      const res = await pingApp(launch);
      appState = {
        hostPresent: true,
        appOnline: true,
        version: res.version,
        extensionId: res.integration?.extensionId || chrome.runtime.id,
        checkedAt: Date.now(),
      };
    } catch (err) {
      const code = errCode(err);
      const hostPresent = !["HOST_MISSING", "HOST_FORBIDDEN", "HOST_INVALID"].includes(code);
      appState = {
        hostPresent,
        appOnline: false,
        errorCode: code,
        lastError: errMessage(err),
        checkedAt: Date.now(),
      };
    }
    await broadcastState();
    return appState;
  })().finally(() => {
    stateInflight = null;
  });
  return stateInflight;
}

async function maybeRefreshAppState(maxAgeMs = 60000): Promise<AppState> {
  if (Date.now() - appState.checkedAt < maxAgeMs) return appState;
  return refreshAppState(false);
}

/**
 * Best-effort HEAD used only to honor the size threshold before automatic
 * click takeover. Cookies are attached by the browser (host permission);
 * Authorization is forwarded from the observed request. Any failure means
 * "size unknown", which allows the takeover to proceed.
 */
async function probeContentSize(url: string, authorization?: string): Promise<number> {
  const controller = new AbortController();
  const timer = self.setTimeout(() => controller.abort(), 2500);
  try {
    const res = await fetch(url, {
      method: "HEAD",
      credentials: "include",
      redirect: "follow",
      signal: controller.signal,
      headers: authorization ? { Authorization: authorization } : undefined,
    });
    if (!res.ok) return 0;
    const n = Number(res.headers.get("content-length") || 0);
    return Number.isFinite(n) && n > 0 ? n : 0;
  } catch {
    return 0;
  } finally {
    self.clearTimeout(timer);
  }
}

async function submitDownload(input: {
  url: string;
  pageUrl?: string;
  suggestedName?: string;
  tabId?: number;
  probeSize?: boolean;
  force?: boolean;
  dir?: string;
}): Promise<TakeoverResult> {
  await ready;
  const url = input.url.split("#")[0] ?? "";
  if (!isHTTPUrl(url)) {
    return { ok: false, code: "UNSUPPORTED_URL", error: friendlyError("UNSUPPORTED_URL", "") };
  }
  const kind = mediaKindFor(url);
  let host = "";
  try {
    host = new URL(url).hostname;
  } catch {
    return { ok: false, code: "UNSUPPORTED_URL", error: "链接格式不正确" };
  }
  // Explicit actions (context menu, media panel, confirmed ask) may target an
  // excluded site. Automatic takeover never reaches this point for those.
  if (!input.force && isExcluded(host, currentSettings.exclude)) {
    return { ok: false, code: "EXCLUDED", error: friendlyError("EXCLUDED", "") };
  }
  if (currentSettings.httpsOnly && !url.toLowerCase().startsWith("https://")) {
    return { ok: false, code: "HTTPS_ONLY", error: friendlyError("HTTPS_ONLY", "") };
  }

  const pageUrl = input.pageUrl || "";
  const cookie = await cookieHeaderFor(url, pageUrl);
  const captured = lookupCaptured(input.tabId, url);
  if (input.probeSize && currentSettings.minSize > 0) {
    const size = await probeContentSize(url, captured?.authorization);
    if (size > 0 && size < currentSettings.minSize) {
      return {
        ok: false,
        code: "BELOW_THRESHOLD",
        error: `文件小于 ${formatBytes(currentSettings.minSize)} 接管阈值，已交给浏览器下载`,
      };
    }
  }
  const headers: Record<string, string> = {};
  const ua = self.navigator?.userAgent;
  if (ua) headers["User-Agent"] = ua;
  if (cookie) headers.Cookie = cookie;
  if (pageUrl) headers.Referer = pageUrl;
  else if (captured?.referer) headers.Referer = captured.referer;
  if (captured?.origin) headers.Origin = captured.origin;
  if (captured?.authorization) headers.Authorization = captured.authorization;
  if (self.navigator?.language) headers["Accept-Language"] = self.navigator.language;

  const name = baseName(input.suggestedName);
  const dir = (input.dir || "").trim();
  const isMedia = kind === "hls" || kind === "dash";
  const method = isMedia ? "media.start" : "download.add";
  const params = {
    url,
    ...(name ? { name } : {}),
    ...(dir ? { dir } : {}),
    ...(isMedia && kind ? { kind } : {}),
    headers,
  };
  const clientRequestId =
    typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(36).slice(2)}`;

  try {
    let task: TaskInfo;
    try {
      task = await nativeCall<TaskInfo>(method, params, {
        clientRequestId,
        timeoutMs: isMedia ? 20000 : 14000,
      });
    } catch (err) {
      const code = errCode(err);
      if (["APP_OFFLINE", "APP_STARTING", "NATIVE_TIMEOUT"].includes(code)) {
        await sleep(1200);
        task = await nativeCall<TaskInfo>(method, params, {
          clientRequestId,
          timeoutMs: isMedia ? 28000 : 22000,
        });
      } else {
        throw err;
      }
    }
    applyNativeSuccess();
    if (!task?.id) {
      throw new XDLError("APP_OFFLINE", "XDL 没有接住这个任务");
    }
    recentSubmitted.set(url, Date.now() + 12000);
    return { ok: true, task };
  } catch (err) {
    applyNativeFailure(err);
    const code = errCode(err);
    return { ok: false, code, error: friendlyError(code, errMessage(err)) };
  }
}

/* ------------------------------------------------------------------ */
/* state broadcast                                                     */
/* ------------------------------------------------------------------ */

function contentState(): ContentState {
  return { settings: currentSettings, app: appState };
}

async function broadcastState() {
  const message = { type: "state.changed", state: contentState() };
  try {
    const tabs = await chrome.tabs.query({});
    await Promise.allSettled(
      tabs
        .filter((t) => typeof t.id === "number")
        .map((t) => chrome.tabs.sendMessage(t.id!, message)),
    );
  } catch {
    /* no tabs */
  }
}

async function toastTab(tabId: number | undefined, kind: "info" | "success" | "error", text: string) {
  if (tabId == null) return;
  try {
    await chrome.tabs.sendMessage(tabId, { type: "toast", kind, text });
  } catch {
    /* no content script */
  }
}

async function notify(title: string, message: string, idPrefix = "xdl-note") {
  if (!currentSettings.notifyFallback) return;
  try {
    await chrome.notifications.create(`${idPrefix}-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`, {
      type: "basic",
      iconUrl: ICON_URL(),
      title,
      message,
      priority: 0,
    });
  } catch {
    /* notifications unavailable */
  }
}

/* ------------------------------------------------------------------ */
/* downloads takeover                                                  */
/* ------------------------------------------------------------------ */

function pauseDownload(id: number): Promise<void> {
  return new Promise((resolve, reject) => {
    chrome.downloads.pause(id, () => {
      const e = chrome.runtime.lastError;
      if (e) reject(new Error(e.message));
      else resolve();
    });
  });
}

function resumeDownload(id: number): Promise<void> {
  return new Promise((resolve, reject) => {
    chrome.downloads.resume(id, () => {
      const e = chrome.runtime.lastError;
      if (e) reject(new Error(e.message));
      else resolve();
    });
  });
}

function cancelDownload(id: number): Promise<void> {
  return new Promise((resolve) => {
    chrome.downloads.cancel(id, () => {
      void chrome.runtime.lastError;
      resolve();
    });
  });
}

function eraseDownload(id: number): Promise<void> {
  return new Promise((resolve) => {
    chrome.downloads.erase({ id }, () => {
      void chrome.runtime.lastError;
      resolve();
    });
  });
}

function searchDownload(id: number): Promise<chrome.downloads.DownloadItem | undefined> {
  return new Promise((resolve) => {
    chrome.downloads.search({ id }, (items) => {
      void chrome.runtime.lastError;
      resolve(items && items[0]);
    });
  });
}

async function pauseWithRetry(id: number): Promise<boolean> {
  for (let attempt = 0; attempt < 3; attempt++) {
    if (attempt > 0) await sleep(180 * attempt);
    try {
      await pauseDownload(id);
      return true;
    } catch {
      const item = await searchDownload(id);
      if (!item || item.state === "complete" || item.state === "interrupted") return false;
    }
  }
  return false;
}

function downloadName(item: chrome.downloads.DownloadItem): string {
  const byFile = baseName(item.filename);
  if (byFile && extOf(byFile)) return byFile;
  return baseName(item.finalUrl || item.url) || byFile || "下载任务";
}

function urlHost(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return "";
  }
}

async function scheduleDownload(item: chrome.downloads.DownloadItem) {
  await ready;
  if (consideredDownloads.has(item.id)) return;
  consideredDownloads.add(item.id);
  await sleep(80);
  await processDownload(item.id);
}

async function processDownload(id: number) {
  if (currentSettings.mode === "off") return;
  let item = await searchDownload(id);
  if (!item) return;
  if (item.byExtensionId) return;
  const url = item.finalUrl || item.url;
  if (!isHTTPUrl(url)) return;
  if (isHTMLMime(item.mime)) return;
  if (extOf(url) === "html" || extOf(url) === "htm") return;
  if (isExcluded(urlHost(url), currentSettings.exclude)) return;
  if (currentSettings.httpsOnly && !url.toLowerCase().startsWith("https://")) return;
  const recent = recentSubmitted.get(url);
  if (recent && recent > Date.now()) return;
  if (item.state !== "in_progress") return;
  if (item.totalBytes > 0 && item.totalBytes < currentSettings.minSize) return;

  if (appState.checkedAt === 0) await refreshAppState(false);
  if (appState.hostPresent === false) return;

  const paused = await pauseWithRetry(id);
  if (!paused) return;
  item = (await searchDownload(id)) || item;
  if (item.state === "complete" || item.state === "interrupted") {
    await resumeDownload(id).catch(() => {});
    return;
  }
  if (item.totalBytes > 0 && item.totalBytes < currentSettings.minSize) {
    await resumeDownload(id).catch(() => {});
    return;
  }

  if (currentSettings.mode === "ask") {
    await askToTakeOver(item);
    return;
  }
  await takeOverItem(item);
}

async function takeOverItem(item: chrome.downloads.DownloadItem, dir?: string): Promise<boolean> {
  const url = item.finalUrl || item.url;
  // Do not forward the browser's provisional filename: at onCreated time
  // Chrome may still report an opaque GUID. Content-Disposition from the
  // server (or the URL leaf) is authoritative for downloads takeover.
  const result = await submitDownload({
    url,
    pageUrl: item.referrer,
    dir,
  });
  if (result.ok) {
    await cancelDownload(item.id);
    await eraseDownload(item.id);
    return true;
  }
  await resumeDownload(item.id).catch(() => {});
  const name = downloadName(item);
  await notify(
    "XDL 未能接管下载",
    `${name}\n${result.error || "已回退到浏览器继续下载"}`,
    "xdl-fail",
  );
  return false;
}

async function persistAsks() {
  try {
    await chrome.storage.local.set({ [ASK_STORAGE_KEY]: [...askPending.values()] });
  } catch {
    /* storage unavailable */
  }
}

async function loadPersistedAsks() {
  try {
    const data = await chrome.storage.local.get(ASK_STORAGE_KEY);
    const list = data[ASK_STORAGE_KEY] as AskPending[] | undefined;
    if (!Array.isArray(list)) return;
    for (const entry of list) {
      if (!entry?.notificationId || typeof entry.downloadId !== "number") continue;
      askPending.set(entry.notificationId, entry);
    }
  } catch {
    /* storage unavailable */
  }
}

async function forgetAsk(notificationId: string) {
  askPending.delete(notificationId);
  try {
    await chrome.alarms.clear(notificationId);
  } catch {
    /* alarms unavailable */
  }
  try {
    await chrome.notifications.clear(notificationId);
  } catch {
    /* ignore */
  }
  await persistAsks();
}

async function resumeIfPaused(downloadId: number) {
  const item = await searchDownload(downloadId);
  // Chrome keeps state="in_progress" while paused; the paused flag is separate.
  if (item?.paused && item.state === "in_progress") {
    await resumeDownload(item.id).catch(() => {});
  }
}

async function finishAsk(notificationId: string, choice: AskChoice | "timeout", dir?: string) {
  const pending = askPending.get(notificationId);
  if (!pending) return;
  askPending.delete(notificationId);
  try {
    await chrome.alarms.clear(notificationId);
  } catch {
    /* ignore */
  }
  try {
    await chrome.notifications.clear(notificationId);
  } catch {
    /* ignore */
  }
  await persistAsks();
  if (choice === "xdl") {
    const item = await searchDownload(pending.downloadId);
    if (item && item.state !== "complete" && item.state !== "interrupted") {
      await takeOverItem(item, dir || pending.dir);
    }
    return;
  }
  await resumeIfPaused(pending.downloadId);
}

async function defaultDownloadDir(): Promise<string> {
  try {
    const res = await pingApp(false);
    applyNativeSuccess(res.version, res.integration?.extensionId);
    return (res.downloadDir || "").trim();
  } catch (err) {
    applyNativeFailure(err);
    return "";
  }
}

async function findAskTab(item: chrome.downloads.DownloadItem): Promise<number | undefined> {
  try {
    const tabs = await chrome.tabs.query({});
    const ref = (item.referrer || "").trim();
    if (ref) {
      const hit = tabs.find((t) => {
        const u = t.url || "";
        return !!u && (u === ref || ref.startsWith(u) || u.startsWith(ref));
      });
      if (hit?.id != null) return hit.id;
    }
    const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
    return active?.id;
  } catch {
    return undefined;
  }
}

async function restoreAsks() {
  await loadPersistedAsks();
  for (const [id, pending] of [...askPending]) {
    const item = await searchDownload(pending.downloadId);
    if (!item || item.state === "complete" || item.state === "interrupted") {
      await forgetAsk(id);
      continue;
    }
    if (Date.now() >= pending.expiresAt) {
      await finishAsk(id, "timeout");
      continue;
    }
    try {
      await chrome.alarms.create(id, { when: pending.expiresAt });
    } catch {
      await finishAsk(id, "browser");
    }
  }
}

async function askToTakeOver(item: chrome.downloads.DownloadItem) {
  const name = downloadName(item);
  const size = item.totalBytes > 0 ? formatBytes(item.totalBytes) : "";
  const notificationId = `xdl-ask-${item.id}-${Date.now()}`;
  const pending: AskPending = {
    notificationId,
    downloadId: item.id,
    name,
    message: size ? `${name} · ${size}` : name,
    expiresAt: Date.now() + ASK_TIMEOUT_MS,
  };
  askPending.set(notificationId, pending);
  await persistAsks();
  try {
    await chrome.alarms.create(notificationId, { when: pending.expiresAt });
  } catch {
    await finishAsk(notificationId, "browser");
    return;
  }

  const defaultDir = await defaultDownloadDir();
  const tabId = await findAskTab(item);
  if (tabId != null) {
    try {
      await chrome.tabs.sendMessage(tabId, {
        type: "ask.download",
        notificationId,
        url: item.finalUrl || item.url,
        name,
        sizeLabel: size || undefined,
        defaultDir: defaultDir || undefined,
      });
      return;
    } catch {
      /* fall through to notification */
    }
  }

  try {
    await chrome.notifications.create(notificationId, {
      type: "basic",
      iconUrl: ICON_URL(),
      title: "用 XDL 下载？",
      message: size ? `${name}\n${size}` : name,
      buttons: [{ title: "用 XDL 下载" }, { title: "浏览器下载" }],
      priority: 1,
    });
  } catch {
    await finishAsk(notificationId, "browser");
  }
}

function setupNotifications() {
  chrome.notifications.onButtonClicked.addListener((notificationId, buttonIndex) => {
    void ready.then(() => finishAsk(notificationId, buttonIndex === 1 ? "browser" : "xdl"));
  });
  chrome.notifications.onClosed.addListener((notificationId, byUser) => {
    if (!byUser) return;
    void ready.then(() => finishAsk(notificationId, "browser"));
  });
  chrome.alarms.onAlarm.addListener((alarm) => {
    if (!alarm.name.startsWith("xdl-ask-")) return;
    void ready.then(() => finishAsk(alarm.name, "timeout"));
  });
}

/* ------------------------------------------------------------------ */
/* media sniffing                                                      */
/* ------------------------------------------------------------------ */

async function ensureMediaLoaded(tabId: number) {
  if (mediaLoaded.has(tabId)) return;
  mediaLoaded.add(tabId);
  try {
    const key = mediaKey(tabId);
    const data = await chrome.storage.session.get(key);
    const list = data[key] as MediaCandidate[] | undefined;
    if (Array.isArray(list)) mediaByTab.set(tabId, list);
  } catch {
    /* session storage unavailable */
  }
}

async function addMedia(tabId: number, candidate: MediaCandidate) {
  await ready;
  if (!currentSettings.mediaSniff) return;
  if (tabId < 0) return;
  await ensureMediaLoaded(tabId);
  const list = mediaByTab.get(tabId) || [];
  const idx = list.findIndex((x) => x.id === candidate.id);
  if (idx >= 0) {
    list[idx] = { ...list[idx], ...candidate, ts: Date.now() };
  } else {
    list.unshift(candidate);
  }
  list.sort((a, b) => b.ts - a.ts);
  const trimmed = list.slice(0, 60);
  mediaByTab.set(tabId, trimmed);
  try {
    await chrome.storage.session.set({ [mediaKey(tabId)]: trimmed });
  } catch {
    /* ignore quota */
  }
  try {
    await chrome.tabs.sendMessage(tabId, { type: "media.update", candidates: trimmed });
  } catch {
    /* no content script yet */
  }
}

async function clearTabMedia(tabId: number) {
  mediaByTab.delete(tabId);
  mediaLoaded.delete(tabId);
  try {
    await chrome.storage.session.remove(mediaKey(tabId));
  } catch {
    /* ignore */
  }
}

function mergeCandidates(base: MediaCandidate[], extra: MediaCandidate[]): MediaCandidate[] {
  const map = new Map<string, MediaCandidate>();
  for (const c of [...base, ...extra]) {
    if (!c || !c.id || !isHTTPUrl(c.url)) continue;
    if (c.kind === "other") continue;
    const existing = map.get(c.id);
    if (!existing || existing.ts < c.ts) map.set(c.id, c);
  }
  return [...map.values()].sort((a, b) => b.ts - a.ts).slice(0, 60);
}

async function listMedia(tabId?: number): Promise<MediaCandidate[]> {
  if (tabId == null) return [];
  await ensureMediaLoaded(tabId);
  let list = mediaByTab.get(tabId) || [];
  try {
    const snapshot = (await chrome.tabs.sendMessage(tabId, { type: "media.snapshot" })) as {
      candidates?: MediaCandidate[];
    };
    if (snapshot?.candidates) list = mergeCandidates(list, snapshot.candidates);
  } catch {
    /* content unavailable */
  }
  return list.filter((c) => c.kind !== "other");
}

function pageUrlOf(details: { initiator?: string }): string | undefined {
  const d = details as { documentUrl?: string; initiator?: string };
  return d.documentUrl || d.initiator || undefined;
}

function setupWebRequest() {
  chrome.webRequest.onBeforeRequest.addListener(
    (details) => {
      if (details.tabId < 0 || !isMediaURL(details.url)) return undefined;
      const c = mediaCandidate(details.url, { pageUrl: pageUrlOf(details) });
      if (c) void addMedia(details.tabId, c);
      return undefined;
    },
    { urls: ["<all_urls>"], types: ["media", "xmlhttprequest", "object", "other", "sub_frame"] },
  );

  chrome.webRequest.onHeadersReceived.addListener(
    (details) => {
      if (details.tabId < 0) return undefined;
      let mime = "";
      let size = 0;
      for (const h of details.responseHeaders || []) {
        const name = (h.name || "").toLowerCase();
        if (name === "content-type") mime = ((h.value || "").split(";")[0] || "").trim();
        if (name === "content-length") size = Number(h.value || 0);
      }
      if (!mime) return undefined;
      const kind = mediaKindFor(details.url, mime);
      if (kind !== "hls" && kind !== "dash" && kind !== "video" && kind !== "audio") return undefined;
      const c = mediaCandidate(details.url, {
        mime,
        size,
        pageUrl: pageUrlOf(details),
        kind,
      });
      if (c) void addMedia(details.tabId, c);
      return undefined;
    },
    { urls: ["<all_urls>"], types: ["media", "xmlhttprequest", "object", "other", "sub_frame"] },
    ["responseHeaders", "extraHeaders"],
  );

  chrome.webRequest.onBeforeSendHeaders.addListener(
    (details) => {
      if (details.tabId < 0) return undefined;
      let referer = "";
      let origin = "";
      let authorization = "";
      for (const h of details.requestHeaders || []) {
        const name = (h.name || "").toLowerCase();
        if (name === "referer") referer = h.value || "";
        if (name === "origin") origin = h.value || "";
        if (name === "authorization") authorization = h.value || "";
      }
      if (referer || origin || authorization) {
        captureRequestHeaders(details.tabId, details.url, { referer, origin, authorization });
      }
      return undefined;
    },
    {
      urls: ["<all_urls>"],
      types: ["main_frame", "sub_frame", "xmlhttprequest", "media", "object", "other"],
    },
    ["requestHeaders", "extraHeaders"],
  );
}

/* ------------------------------------------------------------------ */
/* context menus                                                       */
/* ------------------------------------------------------------------ */

async function ensureMenus() {
  try {
    await chrome.contextMenus.removeAll();
  } catch {
    /* ignore */
  }
  const create = (props: chrome.contextMenus.CreateProperties) => {
    chrome.contextMenus.create(props, () => void chrome.runtime.lastError);
  };
  create({ id: "xdl-link", title: "用 XDL 下载链接", contexts: ["link"] });
  create({ id: "xdl-media", title: "用 XDL 下载媒体", contexts: ["image", "video", "audio"] });
  create({ id: "xdl-scan", title: "扫描本页媒体", contexts: ["page"] });
  create({ id: "xdl-scan-links", title: "扫描本页可下载链接", contexts: ["page"] });
}

async function handleMenuClick(info: chrome.contextMenus.OnClickData, tab?: chrome.tabs.Tab) {
  await ready;
  if (info.menuItemId === "xdl-scan") {
    if (tab?.id == null) return;
    const list = await listMedia(tab.id);
    try {
      await chrome.tabs.sendMessage(tab.id, { type: "media.update", candidates: list });
      await chrome.tabs.sendMessage(tab.id, { type: "panel.open" });
    } catch {
      await notify("无法扫描此页面", "当前页面不允许扩展脚本运行（可能是浏览器内置页或 PDF）。请刷新普通网页后重试。", "xdl-scan");
    }
    return;
  }
  if (info.menuItemId === "xdl-scan-links") {
    if (tab?.id == null) return;
    try {
      const injected = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: () => {
          const exts = new Set([
            "zip", "rar", "7z", "tar", "gz", "exe", "msi", "pdf", "epub",
            "mp4", "mkv", "webm", "mp3", "flac", "jpg", "jpeg", "png", "gif",
            "webp", "m3u8", "mpd", "torrent", "iso", "apk",
          ]);
          const urls: string[] = [];
          const seen = new Set<string>();
          const push = (raw: string | null | undefined) => {
            if (!raw) return;
            try {
              const u = new URL(raw, location.href);
              if (u.protocol !== "http:" && u.protocol !== "https:") return;
              u.hash = "";
              const path = u.pathname.toLowerCase();
              const i = path.lastIndexOf(".");
              const ext = i >= 0 ? path.slice(i + 1) : "";
              if (!exts.has(ext)) return;
              const key = u.toString();
              if (seen.has(key)) return;
              seen.add(key);
              urls.push(key);
            } catch {
              /* ignore */
            }
          };
          document.querySelectorAll("a[href]").forEach((a) => push((a as HTMLAnchorElement).href));
          document.querySelectorAll("img[src], video[src], audio[src], source[src]").forEach((el) =>
            push((el as HTMLImageElement).src || (el as HTMLSourceElement).src),
          );
          return urls.slice(0, 200);
        },
      });
      const urls = (injected[0]?.result as string[] | undefined) || [];
      if (!urls.length) {
        await notify("未发现可下载链接", "当前页没有常见扩展名的文件链接。也可在 XDL 里用「网站抓取」。", "xdl-scan-links");
        return;
      }
      let ok = 0;
      for (const url of urls) {
        const r = await submitDownload({ url, pageUrl: tab.url, force: true });
        if (r.ok) ok++;
      }
      await notify("已批量发送", `发现 ${urls.length} 个链接，成功 ${ok} 个交给 XDL。`, "xdl-scan-links");
    } catch {
      await notify("无法扫描此页面", "当前页面不允许扩展脚本运行。请刷新普通网页后重试。", "xdl-scan-links");
    }
    return;
  }
  let url = "";
  if (info.menuItemId === "xdl-link") url = info.linkUrl || "";
  if (info.menuItemId === "xdl-media") url = info.srcUrl || "";
  if (!url) return;
  if (!isHTTPUrl(url)) {
    await notify("无法交给 XDL", "这个链接不是普通的 http(s) 地址（可能是 blob 或页面生成的内容）。", "xdl-badurl");
    return;
  }
  const result = await submitDownload({
    url,
    pageUrl: info.pageUrl || tab?.url,
    suggestedName: info.linkUrl ? undefined : baseName(url),
    // The context menu is an explicit action: it overrides site exclusion,
    // while HTTPS-only remains enforced inside submitDownload.
    force: true,
  });
  if (result.ok) {
    await toastTab(tab?.id, "success", `已发送到 XDL：${result.task?.name || baseName(url)}`);
  } else {
    await toastTab(tab?.id, "error", `XDL 未接管：${result.error}，已回退浏览器`);
    await notify("XDL 未能接管", result.error || "已回退到浏览器下载", "xdl-fail");
    try {
      await chrome.downloads.download({ url, conflictAction: "uniquify" });
    } catch {
      /* ignore */
    }
  }
}

/* ------------------------------------------------------------------ */
/* message handling                                                    */
/* ------------------------------------------------------------------ */

async function activeTabId(): Promise<number | undefined> {
  try {
    const tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
    return tabs[0]?.id;
  } catch {
    return undefined;
  }
}

async function handleMessage(
  msg: BgMessage,
  sender: chrome.runtime.MessageSender,
): Promise<unknown> {
  await ready;
  switch (msg.type) {
    case "state.get":
      return { ok: true, state: contentState() };
    case "state.refresh": {
      const st = await refreshAppState(!!msg.launch);
      return { ok: true, state: { settings: currentSettings, app: st } };
    }
    case "settings.set": {
      currentSettings = await saveSettings(msg.settings);
      await broadcastState();
      return { ok: true, settings: currentSettings };
    }
    case "link-takeover":
    case "media.send": {
      const pageUrl = msg.pageUrl || sender.tab?.url;
      const result = await submitDownload({
        url: msg.url,
        pageUrl,
        suggestedName: msg.suggestedName,
        tabId: sender.tab?.id,
        probeSize: msg.type === "link-takeover" ? msg.probeSize : undefined,
        force: msg.type === "link-takeover" ? msg.force : true,
        dir: msg.type === "link-takeover" ? msg.dir : undefined,
      });
      await toastTab(
        sender.tab?.id,
        result.ok ? "success" : "error",
        result.ok
          ? `已发送到 XDL：${result.task?.name || baseName(msg.url)}`
          : `XDL 未接管：${result.error}`,
      );
      return result;
    }
    case "browser-download": {
      try {
        const filename = baseName(msg.filename);
        const id = await chrome.downloads.download({
          url: msg.url,
          ...(filename ? { filename } : {}),
          conflictAction: "uniquify",
        });
        return { ok: true, id };
      } catch (err) {
        return { ok: false, error: errMessage(err) };
      }
    }
    case "fs.chooseDir": {
      try {
        const res = await nativeCall<{ dir?: string }>("fs.chooseDir", {}, { timeoutMs: 120000 });
        const dir = (res?.dir || "").trim();
        if (!dir) return { ok: false, error: "未选择目录" };
        applyNativeSuccess();
        return { ok: true, dir };
      } catch (err) {
        applyNativeFailure(err);
        return { ok: false, error: friendlyError(errCode(err), errMessage(err)) };
      }
    }
    case "fs.defaultDir": {
      const dir = await defaultDownloadDir();
      return { ok: true, dir };
    }
    case "ask.answer": {
      // Pending IDs are minted only by this service worker; answering one is enough auth.
      if (!askPending.has(msg.notificationId)) return { ok: false, error: "询问已失效" };
      await finishAsk(msg.notificationId, msg.choice, msg.dir);
      return { ok: true };
    }
    case "media.scan": {
      const tabId = sender.tab?.id;
      if (tabId == null) return { ok: false };
      for (const c of msg.items || []) {
        if (c?.kind !== "other") await addMedia(tabId, { ...c, ts: Date.now() });
      }
      return { ok: true };
    }
    case "media.list": {
      const tabId = msg.tabId ?? sender.tab?.id ?? (await activeTabId());
      return { ok: true, candidates: await listMedia(tabId), state: contentState() };
    }
    case "panel.open": {
      const tabId = sender.tab?.id ?? (await activeTabId());
      if (tabId == null) return { ok: false };
      const list = await listMedia(tabId);
      try {
        await chrome.tabs.sendMessage(tabId, { type: "media.update", candidates: list });
        await chrome.tabs.sendMessage(tabId, { type: "panel.open" });
      } catch {
        return { ok: false };
      }
      return { ok: true };
    }
    case "tasks.list": {
      try {
        const tasks = await nativeCall<TaskInfo[]>("download.list", {}, { timeoutMs: 5000 });
        applyNativeSuccess();
        return { ok: true, tasks, app: appState };
      } catch (err) {
        applyNativeFailure(err);
        return { ok: false, tasks: [], app: appState, error: errMessage(err), code: errCode(err) };
      }
    }
    case "task.action": {
      try {
        const method = msg.action === "pause" ? "download.pause" : "download.resume";
        await nativeCall(method, { id: msg.id }, { timeoutMs: 5000 });
        return { ok: true };
      } catch (err) {
        applyNativeFailure(err);
        return { ok: false, error: errMessage(err) };
      }
    }
    case "repair-integration": {
      try {
        const result = await nativeCall("integration.repair", {}, { timeoutMs: 10000 });
        await refreshAppState(false);
        return { ok: true, integration: result };
      } catch (err) {
        applyNativeFailure(err);
        return { ok: false, error: errMessage(err), code: errCode(err) };
      }
    }
    case "app.start": {
      const st = await refreshAppState(true);
      return { ok: st.appOnline, app: st };
    }
    case "toast":
      await toastTab(sender.tab?.id, msg.kind, msg.text);
      return { ok: true };
    default:
      return { ok: false, error: "unknown message" };
  }
}

/* ------------------------------------------------------------------ */
/* lifecycle                                                           */
/* ------------------------------------------------------------------ */

async function init() {
  currentSettings = await loadSettings();
  await restoreAsks();
  await ensureMenus();
  await broadcastState();
  void refreshAppState(false);
}

export function setupBackground() {
  ready = init();

  chrome.runtime.onMessage.addListener((message: BgMessage, sender, sendResponse) => {
    void handleMessage(message, sender)
      .then(sendResponse)
      .catch((err) => sendResponse({ ok: false, error: errMessage(err) }));
    return true;
  });

  chrome.runtime.onStartup.addListener(() => {
    void refreshAppState(false);
  });
  chrome.runtime.onInstalled.addListener(() => {
    void ensureMenus();
    void refreshAppState(false);
  });

  chrome.storage.onChanged.addListener((changes, area) => {
    if (area !== "local" || !changes["xdl.settings"]) return;
    currentSettings = settingsFromStorageValue(changes["xdl.settings"].newValue);
    void broadcastState();
  });

  chrome.downloads.onCreated.addListener((item) => {
    void scheduleDownload(item);
  });
  setupNotifications();

  chrome.contextMenus.onClicked.addListener((info, tab) => {
    void handleMenuClick(info, tab);
  });

  setupWebRequest();

  chrome.tabs.onActivated.addListener(() => {
    void maybeRefreshAppState(60000);
  });
  chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
    if (changeInfo.status === "loading") {
      void clearTabMedia(tabId);
    }
    if (changeInfo.status === "complete" && tab.active) {
      void maybeRefreshAppState(60000);
    }
  });
  chrome.tabs.onRemoved.addListener((tabId) => {
    void clearTabMedia(tabId);
  });
}
