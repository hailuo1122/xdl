import { DEFAULT_SETTINGS, type ExtensionSettings, type SitePref, type TakeoverMode } from "./types";

const KEY = "xdl.settings";

function clampSettings(raw: Partial<ExtensionSettings> | undefined): ExtensionSettings {
  const s: ExtensionSettings = { ...DEFAULT_SETTINGS, ...(raw || {}) };
  if (s.mode !== "smart" && s.mode !== "ask" && s.mode !== "off") s.mode = "smart";
  if (!Number.isFinite(s.minSize) || s.minSize < 0) s.minSize = DEFAULT_SETTINGS.minSize;
  s.minSize = Math.min(s.minSize, 1024 * 1024 * 1024);
  if (!Array.isArray(s.exclude)) s.exclude = [];
  s.exclude = s.exclude
    .map((x) => String(x).trim().toLowerCase())
    .filter(Boolean)
    .slice(0, 200);
  s.httpsOnly = !!s.httpsOnly;
  s.mediaSniff = s.mediaSniff !== false;
  s.notifyFallback = s.notifyFallback !== false;
  s.siteRules = s.siteRules !== false;
  s.askTimeoutToXdl = s.askTimeoutToXdl !== false;
  s.bulkSelect = s.bulkSelect !== false;
  const prefs: Record<string, SitePref> = {};
  const rawPrefs = (raw?.sitePrefs || s.sitePrefs || {}) as Record<string, unknown>;
  for (const [k, v] of Object.entries(rawPrefs)) {
    const host = String(k).trim().toLowerCase().replace(/^\.+/, "");
    if (!host || host.length > 253) continue;
    if (v === "xdl" || v === "browser") prefs[host] = v;
    if (Object.keys(prefs).length >= 200) break;
  }
  s.sitePrefs = prefs;
  return s;
}

export function sitePrefFor(host: string, prefs: Record<string, SitePref> | undefined): SitePref | undefined {
  const h = host.toLowerCase();
  if (!h || !prefs) return undefined;
  if (prefs[h]) return prefs[h];
  for (const [k, v] of Object.entries(prefs)) {
    if (h === k || h.endsWith("." + k)) return v;
  }
  return undefined;
}

export async function loadSettings(): Promise<ExtensionSettings> {
  try {
    const data = await chrome.storage.local.get(KEY);
    return clampSettings(data[KEY] as Partial<ExtensionSettings>);
  } catch {
    return { ...DEFAULT_SETTINGS };
  }
}

export async function saveSettings(s: ExtensionSettings): Promise<ExtensionSettings> {
  const next = clampSettings(s);
  await chrome.storage.local.set({ [KEY]: next });
  return next;
}

export function settingsFromStorageValue(v: unknown): ExtensionSettings {
  return clampSettings(v as Partial<ExtensionSettings>);
}

export function isExcluded(host: string, patterns: string[]): boolean {
  const h = host.toLowerCase();
  for (const raw of patterns) {
    let p = raw.trim().toLowerCase();
    if (!p) continue;
    try {
      if (p.includes("://")) p = new URL(p).hostname;
    } catch {
      /* keep raw */
    }
    p = p.replace(/^\.+/, "").replace(/\/.*$/, "");
    if (!p) continue;
    if (h === p || h.endsWith("." + p)) return true;
  }
  return false;
}

export function modeLabel(mode: TakeoverMode): string {
  switch (mode) {
    case "smart":
      return "智能接管";
    case "ask":
      return "每次询问";
    default:
      return "已关闭";
  }
}
