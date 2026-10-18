/**
 * 安装器与 Go / WebView2 之间的桥。
 *
 * Go 侧绑定（cmd/setup/main_windows.go）：
 *   setupInfo()          -> { version, dir, mode, installed, hasPayload }
 *                          installed 是已装版本号，空字符串表示没装
 *   setupBrowse(initial) -> path | ""
 *   setupInstall(dir)    -> resolve 表示装完，reject 的 message 会显示在错误态
 *   setupUninstall()
 *   setupLaunch()
 *   setupClose()
 *   window.onProgress(p) <- { phase, ratio, status, detail }
 *
 * 没有桥时（浏览器直接打开 dist/index.html）走下面的预览实现，
 * 方便在 WebView 外面验收状态和动效。
 */

export type Mode = 'install' | 'uninstall';
export type Phase = 'ready' | 'working' | 'done' | 'error';

export interface SetupInfo {
  version: string;
  dir: string;
  mode: Mode;
  /** Go 返回的是版本号字符串；也兼容布尔值 */
  installed: string | boolean;
  hasPayload: boolean;
}

export interface Progress {
  phase: string;
  ratio: number;
  status: string;
  detail: string;
}

interface NativeBridge {
  setupInfo(): SetupInfo | Promise<SetupInfo>;
  setupBrowse(initial: string): string | Promise<string>;
  setupInstall(dir: string): void | Promise<void>;
  setupUninstall(): void | Promise<void>;
  setupLaunch(): void;
  setupClose(): void;
  onProgress?: (p: Progress) => void;
}

const native = window as unknown as Partial<NativeBridge>;
export const hasNative = typeof native.setupInfo === 'function';

/** 已装版本："" 表示没装 */
export function installedVersion(info: SetupInfo | null): string {
  if (!info) return '';
  if (typeof info.installed === 'string') return info.installed;
  return info.installed ? info.version : '';
}

/* ------------------------------------------------------------------ */
/* 预览实现：只在没有原生桥时生效                                        */
/* ------------------------------------------------------------------ */

const query = new URLSearchParams(location.search);
const demoMode: Mode = query.get('mode') === 'uninstall' ? 'uninstall' : 'install';
const demoDir = 'C:\\Users\\Administrator\\AppData\\Local\\Programs\\XDL';
const altDir = 'C:\\Users\\Administrator\\AppData\\Local\\XDL';

type Step = { at: number; status: string; detail: string; phase: string };

/** 和 Go 侧真实阶段一致：install 的 host/shortcuts/registry 都是 ratio=1 的「阶段完成」 */
const STEPS: Record<Mode, Step[]> = {
  install: [
    { at: 0, phase: 'files', status: '正在写入程序', detail: '准备解压' },
    { at: 0.5, phase: 'files', status: '正在写入程序', detail: '写入 xdl.exe' },
    { at: 0.86, phase: 'host', status: '正在注册浏览器组件', detail: '--install-native-host' },
    { at: 0.92, phase: 'shortcuts', status: '正在创建快捷方式', detail: 'XDL.lnk' },
    { at: 0.96, phase: 'registry', status: '正在写入卸载信息', detail: 'HKCU\\...\\Uninstall\\XDL' },
  ],
  uninstall: [
    { at: 0, phase: 'host', status: '正在卸下浏览器组件', detail: 'native host' },
    { at: 0.55, phase: 'files', status: '正在删除程序', detail: '保留下载记录' },
    { at: 0.8, phase: 'shortcuts', status: '正在清理快捷方式', detail: 'XDL.lnk' },
    { at: 1, phase: 'registry', status: '正在完成卸载', detail: 'HKCU' },
  ],
};

const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

function runPreview(mode: Mode, emit: (p: Progress) => void): Promise<void> {
  const total = mode === 'install' ? 6200 : 3600;
  const steps = STEPS[mode];
  const t0 = performance.now();

  return new Promise<void>((resolve) => {
    const tick = () => {
      const k = Math.min(1, (performance.now() - t0) / total);
      const ratio = 1 - Math.pow(1 - k, 1.5);
      let step = steps[0];
      for (const s of steps) if (ratio >= s.at) step = s;
      emit({
        phase: step.phase,
        // install 的文件阶段给真实 0..1，后续阶段和 Go 一样给 1
        ratio: mode === 'install' && step.phase === 'files' ? Math.min(ratio / 0.86, 1) : 1,
        status: step.status,
        detail: step.detail,
      });
      if (k >= 1) {
        emit({ phase: 'done', ratio: 1, status: '完成', detail: '' });
        resolve();
      } else {
        setTimeout(tick, 140);
      }
    };
    tick();
  });
}

/** 预览时用 ?state=working|done|error&ratio=.6 直接落到某个状态 */
export const previewState: { phase: Phase; ratio: number } | null = (() => {
  if (hasNative) return null;
  const s = query.get('state');
  const r = Number(query.get('ratio') ?? '');
  const ratio = Number.isFinite(r) && r > 0 ? r : 0.62;
  if (s === 'working') return { phase: 'working', ratio };
  if (s === 'done') return { phase: 'done', ratio: 1 };
  if (s === 'error') return { phase: 'error', ratio };
  return null;
})();

/* ------------------------------------------------------------------ */
/* 对外接口                                                            */
/* ------------------------------------------------------------------ */

let listener: ((p: Progress) => void) | null = null;

export const bridge = {
  async info(): Promise<SetupInfo> {
    if (!hasNative) {
      return {
        version: '0.0.5-m5',
        dir: demoDir,
        mode: demoMode,
        installed: query.get('installed') === '1' ? '0.0.4' : '',
        hasPayload: query.get('nopayload') !== '1',
      };
    }
    return await Promise.resolve(native.setupInfo!());
  },

  async browse(initial: string): Promise<string> {
    if (!hasNative) {
      await wait(280);
      return initial.endsWith('Programs\\XDL') ? altDir : demoDir;
    }
    return await Promise.resolve(native.setupBrowse!(initial));
  },

  async install(dir: string): Promise<void> {
    if (!hasNative) {
      if (query.get('instant') === '1') return;
      return await runPreview('install', (p) => listener?.(p));
    }
    return await Promise.resolve(native.setupInstall!(dir));
  },

  async uninstall(): Promise<void> {
    if (!hasNative) {
      if (query.get('instant') === '1') return;
      return await runPreview('uninstall', (p) => listener?.(p));
    }
    return await Promise.resolve(native.setupUninstall!());
  },

  launch(): void {
    void Promise.resolve(native.setupLaunch?.()).catch(() => {});
  },

  close(): void {
    void Promise.resolve(native.setupClose?.()).catch(() => {});
  },

  onProgress(fn: (p: Progress) => void): () => void {
    listener = fn;
    if (hasNative) {
      native.onProgress = fn;
      return () => {
        if (native.onProgress === fn) native.onProgress = undefined;
        if (listener === fn) listener = null;
      };
    }
    return () => {
      if (listener === fn) listener = null;
    };
  },
};
