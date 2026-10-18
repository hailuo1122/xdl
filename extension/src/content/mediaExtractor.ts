/**
 * Page-side media discovery.
 *
 * Sniffing `webRequest` alone misses the two cases users actually care about:
 *
 *  1. **MSE / blob playback.** The page requests segments, MediaSource stitches
 *     them in memory, and `<video>.src` is a meaningless `blob:` URL. The real
 *     manifest was seen on the network earlier, so we keep a rolling log of
 *     recently observed manifest/segment URLs and correlate by page.
 *  2. **hls.js / dash.js driven playback.** The player fetches the manifest
 *     through XHR we can observe, then rewrites every URL itself. Again the
 *     manifest is the only useful artefact.
 *
 * This module therefore keeps a per-frame rolling record of media-looking
 * requests and reports the *manifest* (not the hundreds of segments) as the
 * download candidate.
 */

import { isHTTPUrl, mediaKindFor } from "../lib/context";
import type { MediaKind } from "../lib/types";

/** A media request observed on this page. */
export interface SeenMedia {
  url: string;
  kind: MediaKind;
  mime?: string;
  size?: number;
  at: number;
  /** Segments are noise once we know the manifest; kept for fallback only. */
  isSegment: boolean;
}

/** How long a seen URL stays interesting. */
const RETENTION_MS = 90 * 1000;
/** Hard cap per frame so a long session cannot grow without bound. */
const MAX_SEEN = 240;
/** Manifest candidates are promoted after this many corroborating segments. */
const SEGMENT_CORROBORATION = 3;

const seenByFrame = new Map<string, SeenMedia[]>();

function frameKey(): string {
  try {
    // top frame records globally; each iframe keeps its own list so a page with
    // several players does not cross-contaminate them.
    if (window.top === window) return "top";
    return location.href.slice(0, 200);
  } catch {
    return "unknown";
  }
}

function list(): SeenMedia[] {
  const k = frameKey();
  let l = seenByFrame.get(k);
  if (!l) {
    l = [];
    seenByFrame.set(k, l);
  }
  return l;
}

/** True for HLS/DASH media segments, which we track but do not surface. */
function looksLikeSegment(url: string): boolean {
  const lower = url.toLowerCase();
  if (/[?&](?:seq|segment|chunk|part|index)=\d+/.test(lower)) return true;
  if (/\.(?:ts|m4s|cmfv|cmfa|cmft|aac|vtt)(?:$|[?#])/.test(lower)) return true;
  if (/\/(?:seg|segment|chunk|part)[^/]*\d/.test(lower)) return true;
  return false;
}

/** A media URL worth offering as a download candidate. */
export interface Downloadable {
  url: string;
  kind: MediaKind;
  mime?: string;
  size?: number;
}

export function recordMedia(
  url: string,
  mime: string | undefined,
  size: number | undefined,
): SeenMedia | null {
  if (!isHTTPUrl(url)) return null;
  const kind = mediaKindFor(url, mime);
  if (!kind) return null;
  const isSegment = kind === "other" || looksLikeSegment(url);
  const entry: SeenMedia = { url, kind, mime, size, at: Date.now(), isSegment };

  const l = list();
  const existing = l.find((x) => x.url === url);
  if (existing) {
    existing.at = Date.now();
    existing.mime = mime || existing.mime;
    existing.size = size || existing.size;
    return existing;
  }
  l.unshift(entry);
  prune(l);
  return entry;
}

function prune(l: SeenMedia[]) {
  if (l.length > MAX_SEEN) l.length = MAX_SEEN;
  const cutoff = Date.now() - RETENTION_MS;
  while (l.length && l[l.length - 1]!.at < cutoff) l.pop();
}

/**
 * Chooses what to offer the user from everything observed on this page.
 *
 * Manifests win outright. Otherwise a direct media file is used, but only once
 * enough sibling segments corroborate it — otherwise a page that merely loads
 * one preview image would show up as a bogus "video".
 */
export function pickDownloadable(): { url: string; kind: MediaKind; mime?: string; size?: number; label?: string }[] {
  const l = list();
  if (!l.length) return [];

  /*
   * Selection policy, in priority order:
   *
   *  - A manifest (m3u8 / mpd) is always the answer. It is the single URL that
   *    describes the entire stream, so it is what the user should download.
   *  - A direct media file is offered when the page is actually streaming it:
   *    we require corroborating segment siblings, because a page that merely
   *    loads one preview image must not appear as a bogus "video".
   *
   * `list()` is prepend-ordered, so the result is naturally newest-first.
   */
  const result: Downloadable[] = [];
  const emitted = new Set<string>();

  const directFiles = l.filter((x) => !x.isSegment && x.kind !== "other");
  for (const d of directFiles) {
    if (d.kind === "hls" || d.kind === "dash") {
      // Manifest: always offer.
      if (!emitted.has(d.url)) {
        emitted.add(d.url);
        result.push({ url: d.url, kind: d.kind, mime: d.mime, size: d.size });
      }
      continue;
    }
    // Direct media file: require corroboration before offering.
    let prefix = "";
    try {
      prefix = new URL(d.url).pathname.replace(/\/[^/]*$/, "/");
    } catch {
      continue;
    }
    let siblings = 0;
    for (const s of l) {
      if (s.isSegment) siblings++;
      else if (s.url !== d.url && s.url.startsWith(prefix)) siblings++;
    }
    if (siblings >= SEGMENT_CORROBORATION && !emitted.has(d.url)) {
      emitted.add(d.url);
      result.push({ url: d.url, kind: d.kind, mime: d.mime, size: d.size });
    }
  }

  // If we saw only segments and no manifest survived, fall back to the newest
  // direct file so the panel is not empty on an unusual player.
  if (!result.length) {
    for (const s of l) {
      if (s.isSegment || emitted.has(s.url)) continue;
      emitted.add(s.url);
      result.push({ url: s.url, kind: s.kind, mime: s.mime, size: s.size });
      break;
    }
  }

  return result.slice(0, 12);
}

/** Drops everything recorded for this frame (navigation, SPA route change). */
export function reset(): void {
  seenByFrame.delete(frameKey());
}

/* ------------------------------------------------------------------ */
/* MSE blob tracking                                                   */
/* ------------------------------------------------------------------ */

/**
 * `<video>.src` is frequently a `blob:` URL that means nothing outside the
 * page. Map it back to the manifest that produced it so the panel can offer a
 * real, downloadable URL.
 */
const blobOrigins = new Map<string, SeenMedia>();

/**
 * Records which page-level media a blob URL most likely came from. Called when
 * we observe a <video> element whose src is a blob, using the most recently
 * seen manifest on the page.
 */
export function noteBlob(blobUrl: string): void {
  const l = list();
  const recent = l.find((x) => !x.isSegment && x.kind !== "other");
  if (recent) blobOrigins.set(blobUrl, recent);
}

/** Resolves a blob: URL back to a real stream URL, when we know the origin. */
export function resolveBlob(blobUrl: string): SeenMedia | undefined {
  return blobOrigins.get(blobUrl);
}

/**
 * Installs a lightweight MediaSource hook so appendBuffer traffic is visible.
 * Modern players route segments through MediaSource; observing which object a
 * blob belongs to lets the panel show "this video" rather than a bare URL.
 *
 * Best-effort and fail-safe: any error simply means we learn nothing.
 */
export function installMseObserver(): void {
  try {
    const w = window as unknown as { MediaSource?: { prototype: Record<string, unknown> } };
    if (!w.MediaSource?.prototype) return;
    const proto = w.MediaSource.prototype;
    if ((proto as Record<string, unknown>).__xdlHooked) return;
    (proto as Record<string, unknown>).__xdlHooked = true;
    // We only use this as a signal that playback started; the actual URLs come
    // from the network layer, which already observed them.
    const original = proto.addSourceBuffer as unknown;
    if (typeof original === "function") {
      proto.addSourceBuffer = function patched(this: unknown, ...args: unknown[]) {
        document.dispatchEvent(new CustomEvent("xdl-mse-activity"));
        return (original as (...a: unknown[]) => unknown).apply(this, args);
      };
    }
  } catch {
    /* never break the page */
  }
}
