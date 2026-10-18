/**
 * Bulk selection overlay.
 *
 * Mirrors what a download manager does in the browser: hovering a downloadable
 * link shows a checkbox, and selecting several raises a floating panel that
 * hands the whole set to XDL in one batch. Kept in its own module because it
 * owns a second shadow root and a fairly large amount of DOM bookkeeping that
 * would otherwise drown the media panel.
 *
 * Everything is opt-in and unobtrusive: nothing renders until the user
 * actually hovers a link we classify as a file.
 */

import { classifyLink, baseName, formatBytes } from "../lib/context";
import { ruleLabelFor } from "../lib/siteRules";

/** A link the user may select. */
interface PickItem {
  a: HTMLAnchorElement;
  url: string;
  name: string;
  badge: HTMLSpanElement;
  host: string;
  ruleLabel?: string;
}

const MAX_PICK = 300;
/** Pointer must settle before badges appear, so ordinary browsing is clean. */
const HOVER_DELAY_MS = 260;

let host: HTMLDivElement | null = null;
let shadow: ShadowRoot | null = null;
let barEl: HTMLDivElement | null = null;
let badgeHost: HTMLDivElement | null = null;

const items = new Map<HTMLAnchorElement, PickItem>();
let selected: PickItem[] = [];
let hoverTimer = 0;
let enabled = false;

/* ------------------------------------------------------------------ */
/* styles                                                              */
/* ------------------------------------------------------------------ */

const CSS = `
:host { all: initial; }
* { box-sizing: border-box; }
.bar {
  position: fixed; left: 50%; bottom: 26px; transform: translateX(-50%);
  z-index: 2147483645; display: flex; align-items: center; gap: 10px;
  padding: 9px 12px; border-radius: 12px; background: #ffffff;
  border: 1px solid #d2d8e4; box-shadow: 0 14px 40px rgba(20,24,32,.18);
  font-family: "IBM Plex Sans", "Segoe UI Variable Text", "Microsoft YaHei UI", "Noto Sans SC", sans-serif;
  font-size: 12px; color: #141820;
}
.bar[hidden] { display: none !important; }
.bar .count {
  display: flex; align-items: center; gap: 7px; padding-right: 4px;
  font-weight: 500; white-space: nowrap;
}
.bar .box {
  width: 15px; height: 15px; border-radius: 4px; display: grid; place-items: center;
  background: #0b5fff; color: #fff; font-size: 9px; font-weight: 700;
}
.bar .meta { color: #5a6478; font-weight: 400; }
.bar .sep { width: 1px; height: 18px; background: #e7ebf3; }
.btn {
  height: 28px; padding: 0 11px; border-radius: 7px; border: 1px solid #d2d8e4;
  background: #fff; color: #5a6478; font: inherit; font-size: 11.5px; cursor: pointer;
  white-space: nowrap;
}
.btn:hover { border-color: #b8c0d0; color: #141820; }
.btn.primary { background: #0b5fff; border-color: #0b5fff; color: #fff; font-weight: 500; }
.btn.primary:hover { background: #3d7eff; border-color: #3d7eff; color: #fff; }
.btn.primary:disabled { opacity: .5; cursor: default; background: #0b5fff; border-color: #0b5fff; }
.btn.icon { padding: 0 9px; }

.badge {
  position: absolute; z-index: 2147483644; display: flex; align-items: center; gap: 6px;
  padding: 5px 9px 5px 6px; border-radius: 8px; background: #0b5fff; color: #fff;
  font-family: "IBM Plex Sans", "Segoe UI Variable Text", "Microsoft YaHei UI", sans-serif;
  font-size: 11px; line-height: 1.3; white-space: nowrap; cursor: pointer;
  box-shadow: 0 8px 22px rgba(11,95,255,.32); user-select: none;
}
.badge[hidden] { display: none !important; }
.badge .tick {
  width: 13px; height: 13px; border-radius: 4px; display: grid; place-items: center;
  background: rgba(255,255,255,.24); font-size: 8px; flex: 0 0 auto;
}
.badge.on { background: #0f7a52; box-shadow: 0 8px 22px rgba(15,122,82,.32); }
.badge.on .tick { background: #ffffff; color: #0f7a52; }
.badge .nm { max-width: 190px; overflow: hidden; text-overflow: ellipsis; }
.badge .sz { opacity: .8; font-family: "IBM Plex Mono", Consolas, monospace; font-size: 10px; }
`;

/* ------------------------------------------------------------------ */
/* lifecycle                                                           */
/* ------------------------------------------------------------------ */

export function setBulkEnabled(on: boolean) {
  enabled = on;
  if (!on) clearAll();
}

function ensureShadow(): ShadowRoot | null {
  if (shadow) return shadow;
  if (!document.documentElement) return null;
  host = document.createElement("div");
  host.id = "xdl-bulk-host";
  host.style.all = "initial";
  document.documentElement.appendChild(host);
  shadow = host.attachShadow({ mode: "open" });

  const style = document.createElement("style");
  style.textContent = CSS;
  shadow.appendChild(style);

  // Badge layer: one absolutely-positioned element per hovered/selected link.
  badgeHost = document.createElement("div");
  badgeHost.style.cssText = "position:fixed;inset:0;pointer-events:none;z-index:2147483644";
  shadow.appendChild(badgeHost);

  barEl = document.createElement("div");
  barEl.className = "bar";
  barEl.hidden = true;
  barEl.innerHTML = `
    <span class="count"><span class="box">0</span>已选 <span class="meta"></span></span>
    <span class="sep"></span>
    <button class="btn icon" data-act="all" title="全选本页">全选</button>
    <button class="btn primary" data-act="send">用 XDL 下载</button>
    <button class="btn" data-act="clear">取消</button>`;
  shadow.appendChild(barEl);

  barEl.addEventListener("click", (e) => {
    const act = (e.target as HTMLElement)?.closest("[data-act]")?.getAttribute("data-act");
    if (act === "send") void sendSelected();
    else if (act === "clear") clearSelection();
    else if (act === "all") selectAll();
  });

  return shadow;
}

/* ------------------------------------------------------------------ */
/* badge rendering                                                     */
/* ------------------------------------------------------------------ */

function badgeFor(item: PickItem): HTMLSpanElement {
  const root = ensureShadow();
  if (!root || !badgeHost) return document.createElement("span");
  const badge = document.createElement("span");
  badge.className = "badge";
  badge.innerHTML = `
    <span class="tick">✓</span>
    <span class="nm"></span>
    <span class="sz"></span>`;
  (badge.querySelector(".nm") as HTMLElement).textContent = item.name;
  const sizeKnown = !!item.badge.dataset.size;
  (badge.querySelector(".sz") as HTMLElement).textContent = sizeKnown
    ? item.badge.dataset.size || ""
    : "";
  badge.addEventListener("click", (e) => {
    e.preventDefault();
    e.stopPropagation();
    toggle(item);
  });
  badgeHost.appendChild(badge);
  positionBadge(item, badge);
  return badge;
}

function positionBadge(item: PickItem, badge: HTMLSpanElement) {
  const r = item.a.getBoundingClientRect();
  if (!r.width && !r.height) {
    badge.hidden = true;
    return;
  }
  badge.hidden = false;
  badge.classList.toggle("on", selected.includes(item));
  const top = Math.max(6, r.top - 30);
  const left = Math.min(
    Math.max(6, r.left),
    window.innerWidth - badge.offsetWidth - 8,
  );
  badge.style.top = `${top}px`;
  badge.style.left = `${left}px`;
}

/* ------------------------------------------------------------------ */
/* classification + hover detection                                    */
/* ------------------------------------------------------------------ */

function pickable(a: HTMLAnchorElement): PickItem | null {
  if (!enabled || !a.href) return null;
  if (a.getAttribute("href")?.startsWith("#")) return null;
  const verdict = classifyLink(a.href, {
    hasDownloadAttr: a.hasAttribute("download"),
    currentUrl: location.href,
  });
  if (verdict.kind === "ignore") return null;
  const existing = items.get(a);
  if (existing) return existing;
  if (items.size >= MAX_PICK) return null;
  const url = a.href;
  const item: PickItem = {
    a,
    url,
    name: baseName(url) || "下载文件",
    host: (() => {
      try {
        return new URL(url).hostname;
      } catch {
        return "";
      }
    })(),
    ruleLabel: ruleLabelFor(url),
    badge: document.createElement("span"),
  };
  items.set(a, item);
  return item;
}

function onMove(e: MouseEvent) {
  if (!enabled) return;
  const target = e.target as Element | null;
  const a = (target?.closest?.("a[href]") as HTMLAnchorElement | null) ?? null;
  if (!a) {
    clearHover();
    return;
  }
  const item = pickable(a);
  if (!item) {
    clearHover();
    return;
  }
  if (hoverTimer) return;
  hoverTimer = window.setTimeout(() => {
    hoverTimer = 0;
    // Attach the badge lazily so scrolling never leaves it stranded.
    if (!item.badge.isConnected) {
      const made = badgeFor(item);
      item.badge = made;
      item.badge.dataset.size = "";
      // Size is learned from the network layer, not the DOM; leave blank unless
      // the page advertises it via a title attribute.
      const title = item.a.getAttribute("title") || "";
      const m = title.match(/(\d+(?:\.\d+)?)\s*(B|KB|MB|GB)/i);
      if (m?.[1] && m[2]) item.badge.dataset.size = `${m[1]} ${m[2].toUpperCase()}`;
      positionBadge(item, item.badge);
    }
    positionBadge(item, item.badge);
  }, HOVER_DELAY_MS);
}

function clearHover() {
  if (hoverTimer) {
    clearTimeout(hoverTimer);
    hoverTimer = 0;
  }
  // Hide badges that are neither selected nor currently hovered.
  for (const item of items.values()) {
    if (selected.includes(item)) continue;
    if (item.badge.isConnected && !item.badge.hidden) item.badge.hidden = true;
  }
}

/* ------------------------------------------------------------------ */
/* selection                                                           */
/* ------------------------------------------------------------------ */

function toggle(item: PickItem) {
  const i = selected.indexOf(item);
  if (i >= 0) selected.splice(i, 1);
  else if (selected.length < MAX_PICK) selected.push(item);
  syncBar();
  if (selected.length) positionBadge(item, item.badge);
  else item.badge.hidden = true;
}

function selectAll() {
  // Everything currently known to be pickable, or a fresh page scan.
  const candidates = items.size
    ? [...items.values()]
    : scanPageLinks().map((a) => pickable(a)).filter((x): x is PickItem => !!x);
  selected = candidates.slice(0, MAX_PICK);
  for (const item of selected) {
    const badge = item.badge.isConnected ? item.badge : badgeFor(item);
    item.badge = badge;
    positionBadge(item, badge);
  }
  syncBar();
}

function scanPageLinks(): HTMLAnchorElement[] {
  const out: HTMLAnchorElement[] = [];
  let count = 0;
  for (const a of document.querySelectorAll<HTMLAnchorElement>("a[href]")) {
    if (++count > 1200) break;
    out.push(a);
  }
  return out;
}

function clearSelection() {
  selected = [];
  syncBar();
  clearHover();
}

function clearAll() {
  selected = [];
  for (const item of items.values()) {
    item.badge.remove();
  }
  items.clear();
  syncBar();
}

function syncBar() {
  if (!barEl) return;
  const n = selected.length;
  barEl.hidden = n === 0;
  if (!n) return;
  const box = barEl.querySelector(".box") as HTMLElement;
  const meta = barEl.querySelector(".meta") as HTMLElement;
  if (box) box.textContent = String(n);
  if (meta) {
    const hosts = new Set(selected.map((s) => s.host));
    const singleHost = hosts.size === 1 ? [...hosts][0] ?? "" : "";
    // Prefer a known site label (哔哩哔哩 …) over a raw hostname, which reads
    // much better when several files from one service are selected.
    const rule = selected.find((s) => s.ruleLabel)?.ruleLabel;
    meta.textContent = rule || singleHost || `${hosts.size} 个站点`;
  }
  const send = barEl.querySelector('[data-act="send"]') as HTMLButtonElement | null;
  if (send) {
    send.textContent = n === 1 ? "用 XDL 下载" : `用 XDL 下载 ${n} 个`;
  }
}

/* ------------------------------------------------------------------ */
/* sending                                                             */
/* ------------------------------------------------------------------ */

async function sendSelected() {
  const batch = selected.slice();
  if (!batch.length) return;
  const send = barEl?.querySelector('[data-act="send"]') as HTMLButtonElement | null;
  if (send) {
    send.disabled = true;
    send.textContent = "正在发送…";
  }
  const res = (await chrome.runtime.sendMessage({
    type: "bulk.takeover",
    urls: batch.map((b) => b.url),
    pageUrl: location.href,
  })) as { ok?: boolean; added?: number; skipped?: number; error?: string } | undefined;

  if (send) {
    send.disabled = false;
    syncBar();
  }
  if (res?.ok) {
    const added = res.added || batch.length;
    showHint(`已交给 XDL：${added} 个${res.skipped ? `（跳过重复 ${res.skipped}）` : ""}`);
    clearSelection();
  } else {
    showHint(res?.error || "XDL 未响应，已保持选中状态");
  }
}

let hintEl: HTMLDivElement | null = null;
let hintTimer = 0;

function showHint(text: string) {
  const root = ensureShadow();
  if (!root) return;
  if (!hintEl) {
    hintEl = document.createElement("div");
    hintEl.style.cssText =
      "position:fixed;left:50%;bottom:76px;transform:translateX(-50%);z-index:2147483647;" +
      "padding:9px 14px;border-radius:9px;background:#ffffff;border:1px solid #d2d8e4;" +
      "box-shadow:0 12px 32px rgba(20,24,32,.16);font:500 12px/1.4 'IBM Plex Sans','Segoe UI Variable Text','Microsoft YaHei UI',sans-serif;color:#141820";
    root.appendChild(hintEl);
  }
  hintEl.textContent = text;
  hintEl.hidden = false;
  if (hintTimer) clearTimeout(hintTimer);
  hintTimer = window.setTimeout(() => {
    if (hintEl) hintEl.hidden = true;
  }, 3600);
}

/* ------------------------------------------------------------------ */
/* install                                                             */
/* ------------------------------------------------------------------ */

export function installBulk(): void {
  if (!enabled) return;
  ensureShadow();
  window.addEventListener("mousemove", onMove, { passive: true, capture: true });
  window.addEventListener("scroll", clearHover, { passive: true, capture: true });
  window.addEventListener("blur", clearHover, { passive: true });
  window.addEventListener("resize", clearHover, { passive: true });
  // Keep selected badges pinned to their anchors while the page moves.
  const reposition = () => {
    for (const item of selected) {
      if (item.badge.isConnected) positionBadge(item, item.badge);
    }
  };
  window.addEventListener("scroll", reposition, { passive: true, capture: true });
}

/** Removes every badge and listener state (navigation, disable). */
export function teardownBulk(): void {
  window.removeEventListener("mousemove", onMove, { capture: true } as EventListenerOptions);
  clearAll();
}

export { formatBytes };
