import type { TaskState, TaskView } from "./types";

export function bytes(n: number): string {
  if (!n || n <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? Math.round(v) : v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}

export function speed(n: number): string {
  if (!n || n <= 0) return "—";
  return `${bytes(n)}/s`;
}

export function eta(t: TaskView): string {
  if (t.state === "done") return "—";
  if (!t.speed || t.speed <= 0 || !t.size) return "—";
  const left = Math.max(0, t.size - t.done);
  const sec = Math.ceil(left / t.speed);
  if (sec <= 0) return "—";
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${sec % 60}s`;
  return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`;
}

export function percent(t: TaskView): number {
  if (t.state === "done") return 100;
  if (!t.size) return 0;
  return Math.min(100, Math.max(0, (t.done / t.size) * 100));
}

export function pctLabel(t: TaskView): string {
  if (!t.size) return t.done > 0 ? bytes(t.done) : "—";
  return `${percent(t).toFixed(percent(t) > 99.9 ? 0 : 1)}%`;
}

const stateLabels: Record<TaskState, string> = {
  queued: "排队中",
  resolving: "解析中",
  running: "下载中",
  paused: "已暂停",
  waiting_user: "等待操作",
  verifying: "校验中",
  done: "已完成",
  failed: "失败",
  canceled: "已取消",
};

export function stateLabel(s: TaskState): string {
  return stateLabels[s] || s;
}

export function displayName(t: TaskView): string {
  if (t.name) return t.name;
  try {
    const u = new URL(t.url);
    const leaf = decodeURIComponent(u.pathname.split("/").filter(Boolean).pop() || "");
    return leaf || u.hostname;
  } catch {
    return t.url;
  }
}

export function hostOf(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return url;
  }
}

export function formatTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatLimit(bps: number): string {
  if (!bps) return "不限速";
  return speed(bps);
}
