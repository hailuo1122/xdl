export type TaskState =
  | "queued"
  | "resolving"
  | "running"
  | "paused"
  | "waiting_user"
  | "verifying"
  | "done"
  | "failed"
  | "canceled";

export interface TaskError {
  code: string;
  message: string;
  hint?: string;
}

export interface TaskView {
  id: string;
  kind: string;
  url: string;
  finalUrl?: string;
  name: string;
  state: TaskState;
  dir?: string;
  finalPath?: string;
  size: number;
  done: number;
  speed: number;
  conns: number;
  category?: string;
  validator: {
    etag?: string;
    lastModified?: string;
    size?: number;
    acceptRanges: boolean;
  };
  err?: TaskError;
  createdAt: string;
  updatedAt: string;
  peers?: number;
  seeds?: number;
  uploaded?: number;
  ratio?: number;
}

export type ChunkState = "pending" | "running" | "done" | "failed" | "stolen";

export interface Chunk {
  index: number;
  start: number;
  end: number;
  done: number;
  state: ChunkState;
  owner: number;
  attempts: number;
}

export interface TaskDetail {
  task: TaskView;
  chunks: Chunk[];
  connections: number;
}

export interface CategoryRule {
  name: string;
  extensions?: string[];
  domains?: string[];
  subdir?: string;
}

export interface Settings {
  downloadDir: string;
  maxConcurrent: number;
  defaultConns: number;
  turbo: boolean;
  globalLimitBps: number;
  ffmpegPath: string;
  launchAtLogin: boolean;
  startMinimized: boolean;
  closeToTray: boolean;
  notifications: boolean;
  firstRunDone: boolean;
  theme: "dark" | "light" | "system";
  language: string;
  btLegalAccepted: boolean;
  btSeedAfterDone: boolean;
  btUploadLimitBps: number;
  btMaxPeers: number;
  autoCategory: boolean;
  categories?: CategoryRule[];
  scheduleEnabled: boolean;
  schedulePauseStart: number;
  schedulePauseEnd: number;
  updateManifestUrl?: string;
  aria2Enabled: boolean;
  aria2Port: number;
  aria2Secret: string;
}

export interface BrowserIntegration {
  browser: string;
  manifestPath?: string;
  installed: boolean;
}

export interface NativeHostStatus {
  hostExists: boolean;
  hostPath?: string;
  manifestExists: boolean;
  manifestPath?: string;
  extensionId: string;
  origins: string[];
  browsers: BrowserIntegration[];
  installed: boolean;
}

export interface ExtensionSeen {
  lastSeen: string;
  version?: string;
  browser?: string;
  id?: string;
}

export interface IntegrationStatus {
  host: NativeHostStatus;
  extensionId: string;
  extensionSeen: ExtensionSeen;
  extensionLive: boolean;
}

export interface AppStatus {
  version: string;
  ready: boolean;
  tasks: number;
  running: number;
  speed: number;
  limit: number;
  downloadDir: string;
  settings: Settings;
  integration: IntegrationStatus;
  connected: boolean;
}

export interface AddRequest {
  url: string;
  dir?: string;
  name?: string;
  conns?: number;
  kind?: string;
  variantId?: string;
  label?: string;
  infoHash?: string;
  torrentPath?: string;
  selected?: number[];
  seedAfter?: boolean;
  acceptLegal?: boolean;
}

export interface TorrentFileInfo {
  index: number;
  path: string;
  length: number;
}

export interface TorrentProbeResult {
  infoHash: string;
  name: string;
  totalSize: number;
  files: TorrentFileInfo[];
  magnet?: string;
  source?: string;
}

export interface MediaVariant {
  id: string;
  label: string;
  uri: string;
  bandwidth?: number;
  width?: number;
  height?: number;
  codecs?: string;
}

export interface MediaProbeResult {
  kind: string;
  url: string;
  title?: string;
  variants: MediaVariant[];
}

export interface GrabCandidate {
  url: string;
  text?: string;
  source: string;
  ext?: string;
  page?: string;
}

export interface GrabResult {
  startUrl: string;
  pages: number;
  candidates: GrabCandidate[];
  skipped: number;
  errors?: string[];
}

export interface BatchAddResult {
  added: TaskView[];
  skipped: number;
  failures?: string[];
}

export interface UpdateCheckResult {
  current: string;
  latest?: string;
  updateAvailable: boolean;
  url?: string;
  notes?: string;
  checkedAt: string;
  error?: string;
}

export interface StatsEvent {
  tasks: number;
  active: number;
  speed: number;
  limit: number;
}

export type Filter = "downloading" | "waiting" | "stopped" | "done";
