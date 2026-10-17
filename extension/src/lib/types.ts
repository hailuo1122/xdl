export type TakeoverMode = "smart" | "ask" | "off";

export type AskChoice = "xdl" | "browser";

export interface ExtensionSettings {
  mode: TakeoverMode;
  minSize: number;
  exclude: string[];
  httpsOnly: boolean;
  mediaSniff: boolean;
  notifyFallback: boolean;
}

export const DEFAULT_SETTINGS: ExtensionSettings = {
  mode: "ask",
  minSize: 1024 * 1024,
  exclude: [],
  httpsOnly: false,
  mediaSniff: true,
  notifyFallback: true,
};

export interface AppState {
  hostPresent: boolean | null;
  appOnline: boolean;
  version?: string;
  extensionId?: string;
  lastError?: string;
  errorCode?: string;
  checkedAt: number;
}

export type MediaKind = "hls" | "dash" | "video" | "audio" | "other";

export interface MediaCandidate {
  id: string;
  url: string;
  kind: MediaKind;
  mime?: string;
  size?: number;
  pageUrl?: string;
  ts: number;
}

export interface TaskError {
  code: string;
  message: string;
  hint?: string;
}

export interface TaskInfo {
  id: string;
  kind: string;
  url: string;
  name: string;
  state: string;
  size: number;
  done: number;
  speed: number;
  err?: TaskError;
}

export interface ContentState {
  settings: ExtensionSettings;
  app: AppState;
}

export interface TakeoverResult {
  ok: boolean;
  task?: TaskInfo;
  error?: string;
  code?: string;
}

export const HOST_NAME = "com.xdl.host";

export type BgMessage =
  | { type: "state.get" }
  | { type: "state.refresh"; launch?: boolean }
  | { type: "settings.set"; settings: ExtensionSettings }
  | { type: "link-takeover"; url: string; pageUrl?: string; suggestedName?: string; probeSize?: boolean; force?: boolean; dir?: string }
  | { type: "browser-download"; url: string; filename?: string }
  | { type: "media.scan"; items: MediaCandidate[] }
  | { type: "ask.answer"; notificationId: string; choice: AskChoice; dir?: string }
  | { type: "fs.chooseDir" }
  | { type: "fs.defaultDir" }
  | { type: "media.list"; tabId?: number }
  | { type: "media.send"; url: string; pageUrl?: string; suggestedName?: string }
  | { type: "media.snapshot" }
  | { type: "panel.open" }
  | { type: "tasks.list" }
  | { type: "task.action"; action: "pause" | "resume"; id: string }
  | { type: "repair-integration" }
  | { type: "app.start" }
  | { type: "toast"; kind: "info" | "success" | "error"; text: string };

export type ContentMessage =
  | { type: "state.changed"; state: ContentState }
  | { type: "media.update"; candidates: MediaCandidate[] }
  | { type: "panel.open" }
  | { type: "toast"; kind: "info" | "success" | "error"; text: string }
  | { type: "ask.download"; notificationId: string; url: string; name: string; sizeLabel?: string; defaultDir?: string };
