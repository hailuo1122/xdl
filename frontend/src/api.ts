import { Call } from "@wailsio/runtime";
import type {
  AddRequest,
  AppStatus,
  BatchAddResult,
  GrabResult,
  IntegrationStatus,
  MediaProbeResult,
  Settings,
  TaskDetail,
  TaskView,
  TorrentProbeResult,
  UpdateCheckResult,
} from "./types";

const bindingPackage = "github.com/xdl-app/xdl/internal/app";

async function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return (await Call.ByName(`${bindingPackage}.Service.${method}`, ...args)) as T;
}

export const api = {
  status: () => call<AppStatus>("Status"),
  list: () => call<TaskView[]>("List"),
  detail: (id: string) => call<TaskDetail>("Detail", id),
  add: (req: AddRequest) => call<TaskView>("AddAdvanced", req),
  mediaProbe: (req: AddRequest) => call<MediaProbeResult>("MediaProbe", req),
  mediaStart: (req: AddRequest) => call<TaskView>("MediaStart", req),
  torrentProbe: (req: AddRequest) => call<TorrentProbeResult>("TorrentProbe", req),
  torrentStart: (req: AddRequest) => call<TaskView>("TorrentStart", req),
  grabProbe: (req: {
    url: string;
    maxDepth?: number;
    maxPages?: number;
    sameOrigin?: boolean;
    extensions?: string[];
    regex?: string;
  }) => call<GrabResult>("GrabProbe", req),
  grabEnqueue: (req: { urls: string[]; dir?: string; conns?: number }) =>
    call<BatchAddResult>("GrabEnqueue", req),
  batchAdd: (req: { text?: string; urls?: string[]; dir?: string; conns?: number }) =>
    call<BatchAddResult>("BatchAdd", req),
  checkUpdate: () => call<UpdateCheckResult>("CheckUpdate"),
  acceptBTLegal: () => call<Settings>("AcceptBTLegal"),
  chooseTorrentFile: () => call<string>("ChooseTorrentFile"),
  chooseURLListFile: () => call<string>("ChooseURLListFile"),
  readTextFile: (path: string) => call<string>("ReadTextFile", path),
  findDuplicate: (url: string, dir: string, name: string) =>
    call<TaskView | null>("FindDuplicate", url, dir, name),
  pause: (id: string) => call<void>("Pause", id),
  resume: (id: string) => call<void>("Resume", id),
  setPriority: (id: string, priority: number) => call<void>("SetPriority", id, priority),
  setLimitBps: (id: string, bps: number) => call<void>("SetLimitBps", id, bps),
  remove: (id: string) => call<void>("Remove", id),
  pauseAll: () => call<number>("PauseAll"),
  resumeAll: () => call<number>("ResumeAll"),
  openFile: (id: string) => call<void>("OpenFile", id),
  revealFile: (id: string) => call<void>("RevealFile", id),
  chooseDirectory: () => call<string>("ChooseDirectory"),
  getSettings: () => call<Settings>("GetSettings"),
  saveSettings: (s: Settings) => call<Settings>("SaveSettings", s),
  integration: () => call<IntegrationStatus>("Integration"),
  repairIntegration: () => call<IntegrationStatus>("RepairIntegration"),
  extensionPath: () => call<string>("ExtensionPath"),
  openExtensionFolder: () => call<void>("OpenExtensionFolder"),
  diagnostics: () => call<string>("Diagnostics"),
  openLogFolder: () => call<void>("OpenLogFolder"),
  openURL: (url: string) => call<void>("OpenURL", url),
  quit: () => call<void>("Quit"),
};

export function errorText(err: unknown): string {
  if (!err) return "未知错误";
  if (typeof err === "string") return err;
  const anyErr = err as { message?: string; cause?: { message?: string } };
  return anyErr.cause?.message || anyErr.message || String(err);
}
