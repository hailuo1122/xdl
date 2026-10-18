/**
 * Durable state for the MV3 service worker.
 *
 * Chrome terminates an idle MV3 service worker after ~30s. Everything the
 * takeover pipeline keeps in a plain Map (considered download ids, recently
 * submitted urls, captured request headers) silently vanishes, which is how a
 * paused download ends up stuck forever. This module mirrors those maps into
 * `chrome.storage.session` — which lives in memory for the browser session, so
 * it is cheap, and is restored automatically when the worker wakes back up.
 *
 * A heartbeat alarm keeps the worker alive only while there is real work in
 * flight; see setupBackground() for the lifecycle.
 */

const NS = "xdl.sw";

const K_CONSIDERED = `${NS}.considered`;
const K_SUBMITTED = `${NS}.submitted`;
const K_CAPTURE = `${NS}.captures`;
const K_CAPTURED_AT = `${NS}.capturedAt`;
const K_PAUSED = `${NS}.pausedByUs`;

/** Retention for the "already handled this download" ledger. */
const CONSIDERED_TTL_MS = 30 * 60 * 1000;
/** Retention for the url-level submission guard. */
const SUBMITTED_TTL_MS = 60 * 1000;
/** Retention for captured request headers (referer/origin/authorization). */
const CAPTURE_TTL_MS = 5 * 60 * 1000;

interface StoredEntry {
  id: number;
  at: number;
}

interface StoredSubmitted {
  url: string;
  at: number;
}

/**
 * Writes are debounced. A download can be handed over in milliseconds; hitting
 * storage once per event would add avoidable latency to the pause→submit path.
 */
class DebouncedWriter<T> {
  private timer: number | undefined;
  private pending: T | null = null;

  constructor(
    private readonly key: string,
    private readonly waitMs = 250,
  ) {}

  schedule(value: T) {
    this.pending = value;
    if (this.timer !== undefined) return;
    this.timer = self.setTimeout(() => {
      this.timer = undefined;
      const v = this.pending;
      this.pending = null;
      if (v === null) return;
      void chrome.storage.session.set({ [this.key]: v }).catch(() => {
        /* storage unavailable — in-memory copy still authoritative this tick */
      });
    }, this.waitMs);
  }

  /** Flush immediately; used before operations that must survive a wake-up. */
  async flush(value: T) {
    if (this.timer !== undefined) {
      self.clearTimeout(this.timer);
      this.timer = undefined;
    }
    this.pending = null;
    try {
      await chrome.storage.session.set({ [this.key]: value });
    } catch {
      /* ignore */
    }
  }
}

/* ------------------------------------------------------------------ */
/* considered download ledger                                          */
/* ------------------------------------------------------------------ */

const considered = new Map<number, number>();
let consideredLoaded = false;
const consideredWriter = new DebouncedWriter<StoredEntry[]>(K_CONSIDERED);

export async function restoreConsidered() {
  if (consideredLoaded) return;
  consideredLoaded = true;
  try {
    const data = await chrome.storage.session.get(K_CONSIDERED);
    const list = data[K_CONSIDERED] as StoredEntry[] | undefined;
    if (!Array.isArray(list)) return;
    const cutoff = Date.now() - CONSIDERED_TTL_MS;
    for (const e of list) {
      if (typeof e?.id === "number" && e.at >= cutoff) considered.set(e.id, e.at);
    }
  } catch {
    /* ignore */
  }
}

/**
 * Marks a download id as handled and returns true when it had not been seen
 * before. Callers use the return value to gate work, so a lost write only
 * causes a re-evaluation, never a duplicate handoff to XDL.
 */
export function markConsidered(id: number): boolean {
  if (considered.has(id)) return false;
  considered.set(id, Date.now());
  if (considered.size > 500) pruneConsidered();
  consideredWriter.schedule(
    [...considered.entries()].map(([i, at]) => ({ id: i, at })),
  );
  return true;
}

/**
 * Releases a claim without recording success, so a retry can re-evaluate the
 * download after a transient native failure.
 */
export function unmarkConsidered(id: number) {
  if (!considered.delete(id)) return;
  consideredWriter.schedule(
    [...considered.entries()].map(([i, at]) => ({ id: i, at })),
  );
}

function pruneConsidered() {  const cutoff = Date.now() - CONSIDERED_TTL_MS;
  for (const [id, at] of considered) {
    if (at < cutoff) considered.delete(id);
  }
  if (considered.size > 400) {
    // Hard cap: keep the newest half.
    const sorted = [...considered.entries()].sort((a, b) => b[1] - a[1]);
    considered.clear();
    for (const [id, at] of sorted.slice(0, 200)) considered.set(id, at);
  }
}

/* ------------------------------------------------------------------ */
/* recently submitted urls                                             */
/* ------------------------------------------------------------------ */

const submitted = new Map<string, number>();
let submittedLoaded = false;
const submittedWriter = new DebouncedWriter<StoredSubmitted[]>(K_SUBMITTED);

export async function restoreSubmitted() {
  if (submittedLoaded) return;
  submittedLoaded = true;
  try {
    const data = await chrome.storage.session.get(K_SUBMITTED);
    const list = data[K_SUBMITTED] as StoredSubmitted[] | undefined;
    if (!Array.isArray(list)) return;
    const cutoff = Date.now() - SUBMITTED_TTL_MS;
    for (const e of list) {
      if (typeof e?.url === "string" && e.at >= cutoff) submitted.set(e.url, e.at);
    }
  } catch {
    /* ignore */
  }
}

export function isRecentlySubmitted(url: string): boolean {
  const at = submitted.get(url);
  if (at === undefined) return false;
  if (at < Date.now()) {
    submitted.delete(url);
    return false;
  }
  return true;
}

export function markSubmitted(url: string) {
  submitted.set(url, Date.now());
  if (submitted.size > 300) {
    const cutoff = Date.now() - SUBMITTED_TTL_MS;
    for (const [u, at] of submitted) {
      if (at < cutoff) submitted.delete(u);
    }
  }
  submittedWriter.schedule(
    [...submitted.entries()].map(([u, at]) => ({ url: u, at })),
  );
}

/* ------------------------------------------------------------------ */
/* request header capture (moved out of module-level memory)           */
/* ------------------------------------------------------------------ */

export interface CapturedHeaders {
  referer?: string;
  origin?: string;
  authorization?: string;
  at: number;
}

/**
 * Persisted variant of the header capture map. Keys are `tabId|url`,
 * `tabId|origin+path`, or a bare `url` for tab-less contexts.
 */
const captures = new Map<string, CapturedHeaders>();
let capturesLoaded = false;
const captureWriter = new DebouncedWriter<[string, CapturedHeaders][]>(K_CAPTURE);

export async function restoreCaptures() {
  if (capturesLoaded) return;
  capturesLoaded = true;
  try {
    const data = await chrome.storage.session.get(K_CAPTURE);
    const list = data[K_CAPTURE] as [string, CapturedHeaders][] | undefined;
    if (!Array.isArray(list)) return;
    const cutoff = Date.now() - CAPTURE_TTL_MS;
    for (const [k, v] of list) {
      if (typeof k === "string" && v && typeof v === "object" && v.at >= cutoff) {
        captures.set(k, v);
      }
    }
  } catch {
    /* ignore */
  }
}

export function captureKeys(tabId: number | undefined, url: string): string[] {
  const keys: string[] = [];
  if (tabId != null && tabId >= 0) keys.push(`${tabId}|${url}`);
  else keys.push(`-|${url}`);
  try {
    const u = new URL(url);
    const base = `${u.origin}${u.pathname}`;
    keys.push(tabId != null && tabId >= 0 ? `${tabId}|${base}` : `-|${base}`);
  } catch {
    /* ignore */
  }
  return keys;
}

export function putCapture(
  tabId: number | undefined,
  url: string,
  headers: { referer?: string; origin?: string; authorization?: string },
) {
  if (!headers.referer && !headers.origin && !headers.authorization) return;
  const entry: CapturedHeaders = { ...headers, at: Date.now() };
  for (const k of captureKeys(tabId, url)) captures.set(k, entry);
  pruneCaptures();
  captureWriter.schedule([...captures.entries()]);
}

export function getCapture(
  tabId: number | undefined,
  url: string,
): CapturedHeaders | undefined {
  const cutoff = Date.now() - CAPTURE_TTL_MS;
  for (const k of captureKeys(tabId, url)) {
    const hit = captures.get(k);
    if (hit && hit.at >= cutoff) return hit;
  }
  // Tab-less fallback: a url captured under any tab still helps when the
  // download arrives without tab context.
  for (const k of captureKeys(undefined, url)) {
    const hit = captures.get(k);
    if (hit && hit.at >= cutoff) return hit;
  }
  return undefined;
}

function pruneCaptures() {
  if (captures.size <= 260) return;
  const cutoff = Date.now() - CAPTURE_TTL_MS;
  for (const [k, v] of captures) {
    if (v.at < cutoff) captures.delete(k);
  }
  if (captures.size > 320) {
    const sorted = [...captures.entries()].sort((a, b) => b[1].at - a[1].at);
    captures.clear();
    for (const [k, v] of sorted.slice(0, 160)) captures.set(k, v);
  }
}

/* ------------------------------------------------------------------ */
/* downloads this worker paused (recovery ledger)                      */
/* ------------------------------------------------------------------ */

interface PausedRecord {
  id: number;
  at: number;
}

/**
 * A download paused by us must never be left stranded. We write the id before
 * pausing and clear it only once we know the outcome (XDL took it, or the
 * browser was resumed). If the worker dies mid-flight, the next startup finds
 * these ids and resumes them.
 */
const pausedByUs = new Map<number, number>();
let pausedLoaded = false;
const pausedWriter = new DebouncedWriter<PausedRecord[]>(K_PAUSED, 0);

export async function restorePaused(): Promise<number[]> {
  if (!pausedLoaded) {
    pausedLoaded = true;
    try {
      const data = await chrome.storage.session.get(K_PAUSED);
      const list = data[K_PAUSED] as PausedRecord[] | undefined;
      if (Array.isArray(list)) {
        for (const e of list) {
          if (typeof e?.id === "number") pausedByUs.set(e.id, e.at);
        }
      }
    } catch {
      /* ignore */
    }
  }
  const ids = [...pausedByUs.keys()];
  // Everything recovered is resumed by the caller; the ledger is now empty.
  pausedByUs.clear();
  await pausedWriter.flush([]);
  return ids;
}

export async function markPausedByUs(id: number) {
  pausedByUs.set(id, Date.now());
  await pausedWriter.flush([...pausedByUs.entries()].map(([i, at]) => ({ id: i, at })));
}

export async function clearPausedByUs(id: number) {
  if (!pausedByUs.delete(id)) return;
  await pausedWriter.flush([...pausedByUs.entries()].map(([i, at]) => ({ id: i, at })));
}

/** Restore every persisted namespace. Call once during worker startup. */
export async function restoreAll() {
  await Promise.all([restoreConsidered(), restoreSubmitted(), restoreCaptures()]);
}
