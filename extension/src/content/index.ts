import { DEFAULT_SETTINGS, type AppState, type ContentMessage, type ContentState, type ExtensionSettings, type MediaCandidate } from "../lib/types";
import { isExcluded } from "../lib/settings";
import { BINARY_EXTS, baseName, extOf, formatBytes, isHTTPUrl, isMediaURL, mediaCandidate, mediaKindFor } from "../lib/context";

let settings: ExtensionSettings = { ...DEFAULT_SETTINGS };
let appState: AppState = { hostPresent: null, appOnline: false, checkedAt: 0 };
let media: MediaCandidate[] = [];
let reported = new Set<string>();
let panelOpen = false;
let uiHost: HTMLDivElement | null = null;
let shadow: ShadowRoot | null = null;
let toggleEl: HTMLButtonElement | null = null;
let panelEl: HTMLElement | null = null;
let listEl: HTMLElement | null = null;
let countEl: HTMLElement | null = null;
let pillCountEl: HTMLElement | null = null;
let toastEl: HTMLElement | null = null;
let askEl: HTMLElement | null = null;
let askTimer = 0;
let toastTimer = 0;

const KIND_LABEL: Record<string, string> = {
  hls: "HLS",
  dash: "DASH",
  video: "视频",
  audio: "音频",
  other: "分片",
};

function sendBg<T = unknown>(msg: unknown): Promise<T | null> {
  return new Promise((resolve) => {
    try {
      chrome.runtime.sendMessage(msg, (resp) => {
        void chrome.runtime.lastError;
        resolve((resp as T) ?? null);
      });
    } catch {
      resolve(null);
    }
  });
}

/* ------------------------------------------------------------------ */
/* styles + panel                                                      */
/* ------------------------------------------------------------------ */

const PANEL_CSS = `
:host { all: initial; }
* { box-sizing: border-box; }
.wrap {
  position: fixed; right: 18px; bottom: 18px; z-index: 2147483646;
  font-family: "IBM Plex Sans", "Segoe UI Variable Text", "Microsoft YaHei UI", "Noto Sans SC", sans-serif;
  color: #141820; font-size: 12px; line-height: 1.5;
}
.pill {
  display: flex; align-items: center; gap: 8px; height: 34px; padding: 0 13px 0 10px;
  border: 1px solid #d2d8e4; border-radius: 8px;
  background: #ffffff; box-shadow: 0 8px 24px rgba(20,24,32,.12);
  color: #141820; cursor: pointer; font: inherit;
}
.pill:hover { border-color: #0b5fff; }
.pill .mark {
  width: 20px; height: 20px; border-radius: 5px; display: grid; place-items: center;
  background: #e8effe; color: #0b5fff; font-weight: 700; font-size: 10px;
}
.pill .count { color: #5a6478; font-size: 11px; }
.panel {
  position: absolute; right: 0; bottom: 44px; width: 340px; max-height: 58vh;
  display: flex; flex-direction: column; overflow: hidden;
  border: 1px solid #d2d8e4; border-radius: 10px; background: #ffffff;
  box-shadow: 0 18px 48px rgba(20,24,32,.16);
}
.panel[hidden], .toast[hidden], .ask[hidden], .pill[hidden] { display: none !important; }
.phead {
  display: flex; align-items: center; gap: 8px; padding: 11px 12px;
  border-bottom: 1px solid #e7ebf3; color: #141820; font-weight: 600;
}
.phead .sub { color: #8b94a7; font-weight: 400; font-size: 10px; margin-left: auto; }
.phead .x {
  width: 22px; height: 22px; border: 0; border-radius: 5px; background: transparent;
  color: #8b94a7; cursor: pointer; font-size: 15px; line-height: 1;
}
.phead .x:hover { background: #eef1f6; color: #141820; }
.plist { overflow: auto; padding: 6px; }
.item { padding: 9px 8px; border-radius: 7px; }
.item:hover { background: #f5f7fb; }
.item + .item { border-top: 1px solid #eef1f6; }
.row1 { display: flex; align-items: center; gap: 7px; }
.kind {
  flex: 0 0 auto; font-size: 9px; letter-spacing: .02em; padding: 2px 5px; border-radius: 3px;
  border: 1px solid #d2d8e4; color: #5a6478; background: #f5f7fb;
}
.kind.hls, .kind.dash { color: #0b5fff; border-color: #b6c9fc; background: #e8effe; }
.kind.video, .kind.audio { color: #0f7a52; border-color: #b7dfcb; background: #e3f4ec; }
.name { flex: 1; min-width: 0; font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.size { color: #8b94a7; font-size: 9.5px; font-family: "IBM Plex Mono", Consolas, monospace; }
.url { margin-top: 3px; color: #8b94a7; font-family: "IBM Plex Mono", Consolas, monospace; font-size: 9px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.actions { display: flex; align-items: center; gap: 5px; margin-top: 6px; }
.act {
  height: 24px; padding: 0 8px; border: 1px solid #d2d8e4; border-radius: 5px;
  background: #ffffff; color: #5a6478; font: inherit; font-size: 10px; cursor: pointer;
}
.act:hover { border-color: #b8c0d0; color: #141820; }
.act.primary { background: #0b5fff; border-color: #0b5fff; color: #ffffff; font-weight: 600; }
.act.primary:hover { background: #3d7eff; border-color: #3d7eff; color: #ffffff; }
.act:disabled { opacity: .45; cursor: default; }
.tag { margin-left: auto; color: #8b94a7; font-size: 9px; }
.empty { padding: 22px 12px; text-align: center; color: #8b94a7; font-size: 11px; }
.pfoot { padding: 8px 12px; border-top: 1px solid #e7ebf3; color: #8b94a7; font-size: 9.5px; }

.toast {
  position: fixed; top: 18px; right: 18px; z-index: 2147483647;
  max-width: 360px; padding: 9px 12px; border-radius: 8px;
  background: #ffffff; border: 1px solid #d2d8e4;
  box-shadow: 0 10px 28px rgba(20,24,32,.14); color: #141820; font-size: 11px;
  display: flex; align-items: flex-start; gap: 8px;
}
.toast .dot { width: 6px; height: 6px; border-radius: 50%; margin-top: 5px; background: #0b5fff; flex: 0 0 auto; }
.toast.success .dot { background: #0f7a52; }
.toast.error .dot { background: #c0392b; }

.ask {
  position: fixed; top: 18px; left: 50%; transform: translateX(-50%); z-index: 2147483647;
  width: 420px; max-width: calc(100vw - 36px); padding: 14px 15px 13px;
  border: 1px solid #d2d8e4; border-radius: 10px; background: #ffffff;
  box-shadow: 0 18px 48px rgba(20,24,32,.18);
}
.ask .t { font-weight: 600; font-size: 13px; letter-spacing: -.01em; color: #141820; }
.ask .n {
  margin-top: 5px; color: #141820; font-size: 12px; font-weight: 500;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.ask .meta { margin-top: 2px; color: #8b94a7; font-size: 10px; }
.ask .loc {
  margin-top: 11px; padding: 8px 9px; border-radius: 7px; border: 1px solid #d2d8e4;
  background: #f5f7fb;
}
.ask .loc-label { color: #8b94a7; font-size: 9.5px; margin-bottom: 4px; }
.ask .loc-row { display: flex; align-items: center; gap: 8px; }
.ask .loc-path {
  flex: 1; min-width: 0; color: #5a6478; font-size: 10.5px;
  font-family: "IBM Plex Mono", Consolas, "Microsoft YaHei UI", monospace;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.ask .loc-row .act { flex: 0 0 auto; height: 26px; }
.ask .hint { margin-top: 8px; color: #8b94a7; font-size: 9.5px; }
.ask .row { display: flex; gap: 7px; margin-top: 11px; }
.ask .row .act { flex: 1; height: 30px; }
`;

function ensureHost(): ShadowRoot | null {
  if (shadow) return shadow;
  if (!document.documentElement) return null;
  uiHost = document.createElement("div");
  uiHost.id = "xdl-media-host";
  uiHost.style.all = "initial";
  document.documentElement.appendChild(uiHost);
  shadow = uiHost.attachShadow({ mode: "open" });
  const style = document.createElement("style");
  style.textContent = PANEL_CSS;
  shadow.appendChild(style);
  const wrap = document.createElement("div");
  wrap.className = "wrap";
  shadow.appendChild(wrap);

  toggleEl = document.createElement("button");
  toggleEl.className = "pill";
  toggleEl.hidden = true;
  toggleEl.type = "button";
  toggleEl.innerHTML = `<span class="mark">↓</span><span class="label">媒体</span><span class="count"></span>`;
  toggleEl.addEventListener("click", () => togglePanel(!panelOpen));
  pillCountEl = toggleEl.querySelector(".count");
  wrap.appendChild(toggleEl);

  panelEl = document.createElement("section");
  panelEl.className = "panel";
  panelEl.hidden = true;
  panelEl.innerHTML = `
    <div class="phead"><span>本页媒体</span><span class="sub"></span><button class="x" title="关闭">×</button></div>
    <div class="plist"></div>
    <div class="pfoot">直链 / HLS / DASH 都可交给 XDL（DRM / 直播除外）</div>`;
  panelEl.querySelector(".x")?.addEventListener("click", () => togglePanel(false));
  listEl = panelEl.querySelector(".plist");
  countEl = panelEl.querySelector(".sub");
  wrap.appendChild(panelEl);

  toastEl = document.createElement("div");
  toastEl.className = "toast";
  toastEl.hidden = true;
  wrap.appendChild(toastEl);

  askEl = document.createElement("div");
  askEl.className = "ask";
  askEl.hidden = true;
  wrap.appendChild(askEl);

  document.addEventListener("fullscreenchange", () => {
    if (document.fullscreenElement && panelOpen) togglePanel(false);
    updatePill();
  });
  renderPanel();
  updatePill();
  return shadow;
}

function togglePanel(open: boolean) {
  panelOpen = open;
  ensureHost();
  if (panelEl) panelEl.hidden = !open;
  if (open) renderPanel();
  updatePill();
}

function updatePill() {
  if (!toggleEl) return;
  const fullscreen = !!document.fullscreenElement;
  const visible =
    !!settings.mediaSniff &&
    media.some((c) => c.kind !== "other") &&
    (panelOpen || !!document.querySelector("video,audio") || media.some((c) => c.kind === "hls" || c.kind === "dash"));
  const count = String(media.filter((c) => c.kind !== "other").length);
  toggleEl.hidden = !visible || fullscreen;
  if (countEl) countEl.textContent = count;
  if (pillCountEl) pillCountEl.textContent = count;
}

function renderPanel() {
  ensureHost();
  if (!listEl || !countEl) return;
  countEl.textContent = `${media.filter((c) => c.kind !== "other").length} 项`;
  listEl.textContent = "";
  const list = media.filter((c) => c.kind !== "other").slice(0, 20);
  if (!list.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "还没有发现媒体。播放一下视频，或让页面开始加载。";
    listEl.appendChild(empty);
    return;
  }
  for (const c of list) {
    const item = document.createElement("div");
    item.className = "item";

    const row1 = document.createElement("div");
    row1.className = "row1";
    const kind = document.createElement("span");
    kind.className = `kind ${c.kind}`;
    kind.textContent = KIND_LABEL[c.kind] || c.kind;
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = baseName(c.url) || c.kind.toUpperCase();
    name.title = c.url;
    const size = document.createElement("span");
    size.className = "size";
    size.textContent = formatBytes(c.size);
    row1.append(kind, name, size);
    item.appendChild(row1);

    const url = document.createElement("div");
    url.className = "url";
    url.textContent = c.url;
    url.title = c.url;
    item.appendChild(url);

    const actions = document.createElement("div");
    actions.className = "actions";
    if (c.kind === "video" || c.kind === "audio" || c.kind === "hls" || c.kind === "dash") {
      const send = document.createElement("button");
      send.className = "act primary";
      send.type = "button";
      send.textContent = "交给 XDL";
      send.addEventListener("click", async () => {
        send.disabled = true;
        send.textContent = "正在发送…";
        const res = (await sendBg<{ ok: boolean; error?: string }>({
          type: "media.send",
          url: c.url,
          pageUrl: c.pageUrl || location.href,
          suggestedName: baseName(c.url),
        })) || { ok: false };
        send.textContent = res.ok ? "已发送" : "重试";
        send.disabled = res.ok;
      });
      actions.appendChild(send);
    }
    const copy = document.createElement("button");
    copy.className = "act";
    copy.type = "button";
    copy.textContent = "复制链接";
    copy.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(c.url);
        showToast("info", "链接已复制");
      } catch {
        showToast("error", "复制失败");
      }
    });
    actions.appendChild(copy);
    item.appendChild(actions);
    listEl.appendChild(item);
  }
}

function showToast(kind: "info" | "success" | "error", text: string) {
  const host = ensureHost();
  if (!host || !toastEl) return;
  toastEl.className = `toast ${kind}`;
  toastEl.hidden = false;
  toastEl.innerHTML = `<span class="dot"></span><span class="txt"></span>`;
  (toastEl.querySelector(".txt") as HTMLElement).textContent = text;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = self.setTimeout(() => {
    if (toastEl) toastEl.hidden = true;
  }, 3800);
}

/* ------------------------------------------------------------------ */
/* ask sheet                                                           */
/* ------------------------------------------------------------------ */

const ASK_TTL_MS = 45000;

function hideAsk() {
  ensureHost();
  if (askTimer) clearTimeout(askTimer);
  askTimer = 0;
  if (askEl) askEl.hidden = true;
}

function shortenPath(path: string, max = 42): string {
  const p = path.replace(/\//g, "\\").trim();
  if (p.length <= max) return p || "默认下载目录";
  const keep = Math.max(10, Math.floor((max - 1) / 2));
  return `${p.slice(0, keep)}…${p.slice(-keep)}`;
}

function showAsk(opts: {
  url: string;
  name: string;
  sizeLabel?: string;
  defaultDir?: string;
  onYes: (dir?: string) => void;
  onNo: () => void;
}) {
  ensureHost();
  if (!askEl) return;
  askEl.innerHTML = "";
  let selectedDir = (opts.defaultDir || "").trim();

  const armAskTimeout = () => {
    if (askTimer) clearTimeout(askTimer);
    askTimer = self.setTimeout(() => {
      hideAsk();
      opts.onNo();
    }, ASK_TTL_MS);
  };

  const t = document.createElement("div");
  t.className = "t";
  t.textContent = "用 XDL 下载这个文件？";
  const n = document.createElement("div");
  n.className = "n";
  n.textContent = opts.name || baseName(opts.url) || opts.url;
  n.title = opts.url;
  askEl.append(t, n);
  if (opts.sizeLabel) {
    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = opts.sizeLabel;
    askEl.appendChild(meta);
  }

  const loc = document.createElement("div");
  loc.className = "loc";
  const locLabel = document.createElement("div");
  locLabel.className = "loc-label";
  locLabel.textContent = "保存位置";
  const locRow = document.createElement("div");
  locRow.className = "loc-row";
  const locPath = document.createElement("div");
  locPath.className = "loc-path";
  const renderPath = () => {
    locPath.textContent = shortenPath(selectedDir || "默认下载目录");
    locPath.title = selectedDir || "使用 XDL 当前默认下载目录";
  };
  renderPath();
  const pick = document.createElement("button");
  pick.type = "button";
  pick.className = "act";
  pick.textContent = "更改";
  pick.addEventListener("click", async () => {
    pick.disabled = true;
    pick.textContent = "选择中…";
    armAskTimeout();
    const res = (await sendBg<{ ok: boolean; dir?: string; error?: string }>({ type: "fs.chooseDir" })) || {
      ok: false,
    };
    pick.disabled = false;
    pick.textContent = "更改";
    if (res.ok && res.dir) {
      selectedDir = res.dir;
      renderPath();
      return;
    }
    if (res.error) showToast("error", res.error);
  });
  locRow.append(locPath, pick);
  loc.append(locLabel, locRow);
  askEl.appendChild(loc);

  const hint = document.createElement("div");
  hint.className = "hint";
  hint.textContent = "确认后由 XDL 多连接下载；选「浏览器下载」则交回浏览器。";
  const row = document.createElement("div");
  row.className = "row";
  const yes = document.createElement("button");
  yes.type = "button";
  yes.className = "act primary";
  yes.textContent = "用 XDL 下载";
  yes.addEventListener("click", () => {
    hideAsk();
    opts.onYes(selectedDir || undefined);
  });
  const no = document.createElement("button");
  no.type = "button";
  no.className = "act";
  no.textContent = "浏览器下载";
  no.addEventListener("click", () => {
    hideAsk();
    opts.onNo();
  });
  row.append(yes, no);
  askEl.append(hint, row);
  askEl.hidden = false;
  armAskTimeout();

  if (!selectedDir) {
    void sendBg<{ ok: boolean; dir?: string }>({ type: "fs.defaultDir" }).then((res) => {
      if (!res?.dir || selectedDir) return;
      selectedDir = res.dir;
      renderPath();
    });
  }
}

/* ------------------------------------------------------------------ */
/* click interception                                                  */
/* ------------------------------------------------------------------ */

function findAnchor(e: MouseEvent): HTMLAnchorElement | null {
  const path = typeof e.composedPath === "function" ? e.composedPath() : [];
  for (const el of path) {
    if (el instanceof HTMLAnchorElement) return el;
  }
  const target = e.target as Element | null;
  return target?.closest?.("a[href]") as HTMLAnchorElement | null;
}

function eligibleLink(a: HTMLAnchorElement): { url: string; name: string } | null {
  if (!a.href) return null;
  if (a.getAttribute("href")?.startsWith("#")) return null;
  if (!isHTTPUrl(a.href)) return null;
  try {
    const u = new URL(a.href);
    if (u.href.split("#")[0] === location.href.split("#")[0]) return null;
  } catch {
    return null;
  }
  const ext = extOf(a.href);
  const hasDownload = a.hasAttribute("download");
  if (!hasDownload && !BINARY_EXTS.has(ext)) return null;
  const host = new URL(a.href).hostname;
  if (isExcluded(host, settings.exclude)) return null;
  if (settings.httpsOnly && !a.href.toLowerCase().startsWith("https://")) return null;
  const downloadAttr = a.getAttribute("download");
  const name = downloadAttr && downloadAttr.trim() ? downloadAttr.trim() : baseName(a.href);
  return { url: a.href, name };
}

let hostWarnAt = 0;

async function smartTakeOver(url: string, name: string, probeSize: boolean, force: boolean, dir?: string) {
  showToast("info", "正在交给 XDL…");
  const res = (await sendBg<{ ok: boolean; code?: string; error?: string; task?: { name: string } }>({
    type: "link-takeover",
    url,
    pageUrl: location.href,
    suggestedName: name,
    probeSize,
    force,
    dir,
  })) || { ok: false };
  if (res.ok) {
    showToast("success", `已发送到 XDL：${res.task?.name || name}`);
    return;
  }
  await sendBg({ type: "browser-download", url, filename: name });
  if (res.code === "BELOW_THRESHOLD") {
    showToast("info", res.error || "文件小于接管阈值，已交给浏览器下载");
    return;
  }
  showToast("error", `XDL 未接管：${res.error || "已回退浏览器"}`);
}

function onClick(e: MouseEvent) {
  if (!e.isTrusted || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
  if (e.defaultPrevented) return;
  if (settings.mode === "off") return;
  const a = findAnchor(e);
  if (!a) return;
  const hit = eligibleLink(a);
  if (!hit) return;
  if (appState.hostPresent === false) {
    // Let Chrome perform its normal download; explain in Chinese instead of
    // surfacing "Specified native messaging host not found".
    if (Date.now() - hostWarnAt > 15000) {
      hostWarnAt = Date.now();
      showToast("info", "浏览器组件未安装，本次由浏览器下载。可在 XDL 设置 → 浏览器接管里重新注册。");
    }
    return;
  }
  e.preventDefault();
  e.stopPropagation();
  e.stopImmediatePropagation();
  if (settings.mode === "ask") {
    showAsk({
      url: hit.url,
      name: hit.name,
      // The user explicitly confirmed, so exclusion/threshold do not apply.
      onYes: (dir) => void smartTakeOver(hit.url, hit.name, false, true, dir),
      onNo: () => void sendBg({ type: "browser-download", url: hit.url, filename: hit.name }),
    });
    return;
  }
  void smartTakeOver(hit.url, hit.name, true, false);
}

/* ------------------------------------------------------------------ */
/* local media scan                                                    */
/* ------------------------------------------------------------------ */

function collectLocalMedia(): MediaCandidate[] {
  const out = new Map<string, MediaCandidate>();
  const add = (url: string, mime?: string) => {
    if (!url || !isHTTPUrl(url) || url.startsWith("blob:")) return;
    const c = mediaCandidate(url, { mime, pageUrl: location.href });
    if (!c || c.kind === "other") return;
    if (!out.has(c.id)) out.set(c.id, c);
  };
  try {
    for (const el of document.querySelectorAll<HTMLMediaElement>("video,audio")) {
      add(el.currentSrc);
      add(el.src);
      for (const source of el.querySelectorAll<HTMLSourceElement>("source[src]")) add(source.src);
    }
    let count = 0;
    for (const a of document.querySelectorAll<HTMLAnchorElement>("a[href]")) {
      if (++count > 4000) break;
      if (!isMediaURL(a.href)) continue;
      add(a.href);
    }
    for (const entry of performance.getEntriesByType("resource")) {
      if (!isMediaURL(entry.name)) continue;
      add(entry.name);
    }
  } catch {
    /* DOM may be unavailable */
  }
  return [...out.values()];
}

function mergeMedia(incoming: MediaCandidate[]) {
  const map = new Map<string, MediaCandidate>();
  for (const c of [...media, ...incoming]) {
    if (!c || c.kind === "other" || !isHTTPUrl(c.url)) continue;
    const prev = map.get(c.id);
    if (!prev || prev.ts < c.ts) map.set(c.id, c);
  }
  media = [...map.values()].sort((a, b) => b.ts - a.ts).slice(0, 60);
  renderPanel();
  updatePill();
}

function scanPage() {
  const local = collectLocalMedia();
  const fresh = local.filter((c) => !reported.has(c.id));
  mergeMedia(local);
  if (fresh.length) {
    for (const c of fresh) reported.add(c.id);
    void sendBg({ type: "media.scan", items: fresh });
  }
}

/* ------------------------------------------------------------------ */
/* boot                                                                */
/* ------------------------------------------------------------------ */

function applyState(next: ContentState | null | undefined) {
  if (!next) return;
  if (next.settings) settings = next.settings;
  if (next.app) appState = next.app;
  renderPanel();
  updatePill();
}

export function setupContent() {
  window.addEventListener("click", onClick, true);

  chrome.runtime.onMessage.addListener((message: ContentMessage, _sender, sendResponse) => {
    switch (message?.type) {
      case "state.changed":
        applyState(message.state);
        sendResponse?.({ ok: true });
        return false;
      case "media.update":
        mergeMedia(message.candidates || []);
        sendResponse?.({ ok: true });
        return false;
      case "panel.open":
        togglePanel(true);
        renderPanel();
        sendResponse?.({ ok: true });
        return false;
      case "toast":
        showToast(message.kind, message.text);
        sendResponse?.({ ok: true });
        return false;
      case "ask.download":
        showAsk({
          url: message.url,
          name: message.name,
          sizeLabel: message.sizeLabel,
          defaultDir: message.defaultDir,
          onYes: (dir) => {
            void sendBg({ type: "ask.answer", notificationId: message.notificationId, choice: "xdl", dir });
          },
          onNo: () => {
            void sendBg({ type: "ask.answer", notificationId: message.notificationId, choice: "browser" });
          },
        });
        sendResponse?.({ ok: true });
        return false;
      default:
        return false;
    }
  });

  chrome.runtime.onMessage.addListener((message: { type?: string }, _sender, sendResponse) => {
    if (message?.type === "media.snapshot") {
      sendResponse({ candidates: collectLocalMedia() });
      return false;
    }
    return false;
  });

  void (async () => {
    const state = await sendBg<{ ok: boolean; state?: ContentState }>({ type: "state.get" });
    if (state?.state) applyState(state.state);
    const list = await sendBg<{ ok: boolean; candidates?: MediaCandidate[] }>({ type: "media.list" });
    if (list?.candidates) mergeMedia(list.candidates);
    scanPage();
    self.setInterval(scanPage, 4500);
  })();
}
