import { HOST_NAME } from "./types";

export class XDLError extends Error {
  code: string;
  detail?: unknown;

  constructor(code: string, message: string, detail?: unknown) {
    super(message);
    this.name = "XDLError";
    this.code = code;
    this.detail = detail;
  }
}

let seq = 0;

function clientInfo() {
  const ua = self.navigator?.userAgent || "";
  const browser = /\bEdg\//.test(ua) ? "edge" : /\bBrave\//.test(ua) ? "brave" : "chrome";
  return { version: chrome.runtime.getManifest().version, browser, id: chrome.runtime.id };
}

function mapLastError(message: string): string {
  const m = message.toLowerCase();
  if (m.includes("not found")) return "HOST_MISSING";
  if (m.includes("forbidden")) return "HOST_FORBIDDEN";
  if (m.includes("exited") || m.includes("terminated")) return "HOST_EXITED";
  if (m.includes("invalid") || m.includes("access")) return "HOST_INVALID";
  return "NATIVE_ERROR";
}

interface NativeRequest {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params?: unknown;
  clientRequestId?: string;
}

export interface NativeResult<T> {
  result: T;
}

export function nativeCall<T>(
  method: string,
  params: unknown,
  opts: { clientRequestId?: string; timeoutMs?: number } = {},
): Promise<T> {
  const timeoutMs = opts.timeoutMs ?? 6000;
  const req: NativeRequest = {
    jsonrpc: "2.0",
    id: ++seq,
    method,
    params,
    ...(opts.clientRequestId ? { clientRequestId: opts.clientRequestId } : {}),
  };
  return new Promise<T>((resolve, reject) => {
    let settled = false;
    const timer = setTimeout(() => {
      if (!settled) {
        settled = true;
        reject(new XDLError("NATIVE_TIMEOUT", "XDL 响应超时"));
      }
    }, timeoutMs);
    try {
      chrome.runtime.sendNativeMessage(HOST_NAME, req, (resp) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        const lastError = chrome.runtime.lastError;
        if (lastError) {
          const msg = lastError.message || "native messaging failed";
          reject(new XDLError(mapLastError(msg), msg));
          return;
        }
        const r = resp as { result?: T; error?: { code?: number; message?: string; data?: { code?: string; message?: string } } };
        if (!r) {
          reject(new XDLError("NATIVE_EMPTY", "原生宿主没有返回数据"));
          return;
        }
        if (r.error) {
          const code = r.error.data?.code || `RPC_${r.error.code ?? "ERROR"}`;
          const msg = r.error.data?.message || r.error.message || "XDL 返回错误";
          reject(new XDLError(code, msg, r.error.data));
          return;
        }
        resolve(r.result as T);
      });
    } catch (err) {
      settled = true;
      clearTimeout(timer);
      reject(new XDLError("NATIVE_THROW", err instanceof Error ? err.message : String(err)));
    }
  });
}

export interface PingResult {
  status: string;
  name: string;
  version: string;
  downloadDir?: string;
  integration?: {
    host?: {
      installed?: boolean;
      hostExists?: boolean;
      manifestExists?: boolean;
      browsers?: { browser: string; installed: boolean }[];
    };
    extensionId?: string;
    extensionLive?: boolean;
  };
}

export function pingApp(launch = false): Promise<PingResult> {
  return nativeCall<PingResult>(
    "app.ping",
    { client: clientInfo(), launch },
    { timeoutMs: launch ? 8000 : 3500 },
  );
}

export function nativeClientInfo() {
  return clientInfo();
}
