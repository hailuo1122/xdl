import { useCallback, useEffect, useRef, useState } from 'react';
import { motion, useReducedMotion } from 'motion/react';
import {
  bridge,
  installedVersion,
  previewState,
  type Mode,
  type Phase,
  type Progress,
  type SetupInfo,
} from './bridge';
import { ChannelDeck } from './components/ChannelDeck';
import { Mark } from './components/Mark';

const EASE: [number, number, number, number] = [0.16, 1, 0.3, 1];
const IDLE: Progress = { phase: 'idle', ratio: 0, status: '', detail: '' };

/**
 * Go 侧的 ratio 是「阶段进度」：files 阶段是真实字节比，host / shortcuts /
 * registry 都是 1。这里折算成一条连续的全局进度，不然写到 86% 就会显示 100%。
 */
function globalRatio(mode: Mode, p: Progress): number {
  if (mode === 'uninstall') return p.ratio;
  switch (p.phase) {
    case 'files':
      return p.ratio * 0.86;
    case 'host':
      return 0.9;
    case 'shortcuts':
      return 0.94;
    case 'registry':
      return 0.97;
    default:
      return p.ratio;
  }
}

function reasonOf(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e ?? '');
  return raw.replace(/^Error:\s*/i, '').trim() || '安装程序没有返回具体原因。';
}

export default function App() {
  const reduce = !!useReducedMotion();

  const [info, setInfo] = useState<SetupInfo | null>(null);
  const [dir, setDir] = useState('');
  const [phase, setPhase] = useState<Phase>('ready');
  const [prog, setProg] = useState<Progress>(IDLE);
  const [err, setErr] = useState('');

  const mode: Mode = info?.mode ?? 'install';
  const isInstall = mode === 'install';
  const installed = installedVersion(info);
  const blocked = isInstall && !!info && !info.hasPayload;
  const ratio = globalRatio(mode, prog);

  useEffect(() => {
    let alive = true;
    bridge
      .info()
      .then((next) => {
        if (!alive) return;
        setInfo(next);
        setDir(next.dir);
      })
      .catch(() => {
        if (!alive) return;
        setInfo({ version: '', dir: '', mode: 'install', installed: '', hasPayload: true });
      });

    const off = bridge.onProgress((p) => setProg(p));
    return () => {
      alive = false;
      off();
    };
  }, []);

  /* 预览用：?state=working|done|error 直接落到对应状态 */
  const presetApplied = useRef(false);
  useEffect(() => {
    const preset = previewState;
    if (!preset || !info || presetApplied.current) return;
    presetApplied.current = true;
    const t = setTimeout(() => {
      setPhase(preset.phase);
      setProg({
        phase: preset.phase,
        ratio: preset.ratio,
        status:
          preset.phase === 'working' ? (mode === 'install' ? '正在写入程序' : '正在删除程序') : '',
        detail:
          preset.phase === 'working'
            ? mode === 'install'
              ? '86.2 MB / 140.5 MB'
              : '保留下载记录'
            : '',
      });
      if (preset.phase === 'error') setErr('无法写入文件：目标目录正被另一个程序占用。');
    }, 80);
    return () => clearTimeout(t);
  }, [info, mode]);

  const start = useCallback(async () => {
    if (!info || phase === 'working' || blocked) return;
    setErr('');
    setProg({
      phase: 'prepare',
      ratio: 0,
      status: isInstall ? '正在检查安装包' : '正在检查程序文件',
      detail: '',
    });
    setPhase('working');
    try {
      if (isInstall) await bridge.install(dir);
      else await bridge.uninstall();
      setProg({ phase: 'done', ratio: isInstall ? 1 : 0, status: '完成', detail: '' });
      setPhase('done');
    } catch (e) {
      setErr(reasonOf(e));
      setPhase('error');
    }
  }, [info, phase, blocked, isInstall, dir]);

  const primary = useCallback(() => {
    if (phase === 'done') {
      if (isInstall) bridge.launch();
      else bridge.close();
      return;
    }
    if (phase === 'working' || blocked) return;
    void start();
  }, [phase, isInstall, blocked, start]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && phase !== 'working') {
        e.preventDefault();
        bridge.close();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [phase]);

  /* ---------------------------------- 文案 --------------------------------- */

  let title: string;
  let lede: string;
  let sub = '';
  let note: string;

  if (phase === 'working') {
    title = isInstall ? '正在安装' : '正在卸载';
    lede = prog.status || (isInstall ? '正在写入程序' : '正在删除程序');
    sub = prog.detail || '';
    note = isInstall ? '装完之前先别关窗口' : '卸完之前先别关窗口';
  } else if (phase === 'done') {
    title = isInstall ? '装好了' : '卸载完成';
    lede = isInstall
      ? '开始菜单里已经有了，浏览器接管也开好了。'
      : '程序和浏览器组件已经移除。下载记录还在。';
    note = isInstall ? '之后从开始菜单就能启动' : '下载记录没有被删除';
  } else if (phase === 'error') {
    title = isInstall ? '没有装完' : '没有卸干净';
    lede = isInstall ? '安装中断了。' : '卸载中断了。';
    sub = err || prog.detail || '安装程序没有返回具体原因。';
    note = '再试一次通常就能过去';
  } else if (blocked) {
    title = '安装包不完整';
    lede = '这个安装程序里没有附带 XDL。请重新下载完整安装包。';
    note = '缺少程序文件';
  } else if (isInstall) {
    title = installed ? '覆盖安装' : '安装到这台电脑';
    lede = installed
      ? `已经装了 ${installed}，继续会覆盖程序文件。下载记录保留。`
      : '写入当前用户目录，不需要管理员。已有的下载和历史都会保留。';
    note = installed ? '覆盖前建议先退出 XDL' : '只写入当前用户目录';
  } else {
    title = '卸载 XDL';
    lede = '会删掉程序、浏览器组件和开始菜单入口。下载记录仍然保留。';
    note = '卸载前请先退出 XDL';
  }

  const keyLabel =
    phase === 'working'
      ? isInstall
        ? '安装中'
        : '卸载中'
      : phase === 'done'
        ? isInstall
          ? '打开 XDL'
          : '关闭'
        : phase === 'error'
          ? '再试一次'
          : blocked
            ? '缺少文件'
            : isInstall
              ? installed
                ? '覆盖安装'
                : '安装'
              : '卸载';

  const keyDisabled = phase === 'working' || blocked || !info;

  const showKeep = mode === 'uninstall';
  const slotEditable = phase === 'ready' || phase === 'error';
  const hideClose = phase === 'done' && mode === 'uninstall';

  const enter = (delay: number, y = 8) =>
    reduce
      ? {}
      : {
          initial: { opacity: 0, y },
          animate: { opacity: 1, y: 0 },
          transition: { delay, duration: 0.62, ease: EASE },
        };

  const swap = reduce
    ? { initial: false as const, animate: { opacity: 1 } }
    : {
        initial: { opacity: 0, y: 4, filter: 'blur(4px)' },
        animate: { opacity: 1, y: 0, filter: 'blur(0px)' },
      };

  return (
    <div className="app">
      <span className="ruler" aria-hidden="true" />
      <span className="ruler-l" aria-hidden="true" />

      <div className="top">
        <header className="head">
          <motion.div
            className="lockup"
            initial={reduce ? false : { opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ duration: 0.4 }}
          >
            <Mark sealed={phase === 'done'} className="brand-mark" />
            <span className="brand-mask">
              <motion.span
                className="brand"
                initial={reduce ? false : { y: '112%' }}
                animate={{ y: 0 }}
                transition={{ duration: 0.86, ease: EASE, delay: 0.06 }}
              >
                XDL
              </motion.span>
            </span>
          </motion.div>

          <motion.span
            className="head-rule"
            aria-hidden="true"
            initial={reduce ? false : { scaleX: 0 }}
            animate={{ scaleX: 1 }}
            transition={{ duration: 0.8, ease: EASE, delay: 0.3 }}
          />

          <motion.div className="version" {...enter(0.42, 4)}>
            <span className="ver">{info?.version ? `v${info.version}` : ''}</span>
            {installed && isInstall && <span className="tag">已安装</span>}
          </motion.div>
        </header>

        <div className="hero">
          <p className="sr-only" role="status" aria-live="polite">
            {title}。{lede}
          </p>
          <motion.div className="hero-copy" {...enter(0.3, 10)}>
            <motion.h1 key={title} {...swap} transition={{ duration: reduce ? 0 : 0.38, ease: EASE }}>
              {title}
            </motion.h1>
            <motion.p key={lede} className="lede" {...swap} transition={{ duration: reduce ? 0 : 0.3, ease: EASE }}>
              {lede}
            </motion.p>
            {sub && (
              <motion.p
                key={sub}
                className="sub"
                initial={reduce ? false : { opacity: 0 }}
                animate={{ opacity: 1 }}
                transition={{ duration: reduce ? 0 : 0.26 }}
                title={sub}
              >
                {sub}
              </motion.p>
            )}
          </motion.div>

          <motion.div className="slot-row" {...enter(0.44, 8)}>
            {showKeep ? (
              <div className="slot slot-keep">
                <span className="k">将保留</span>
                <span className="v">下载记录</span>
              </div>
            ) : (
              <button
                type="button"
                className={`slot ${phase === 'working' ? 'is-dim' : ''}`}
                onClick={async () => {
                  if (!slotEditable) return;
                  const next = await bridge.browse(dir).catch(() => '');
                  if (next) setDir(next);
                }}
                disabled={!slotEditable}
                aria-label={`更换安装位置，当前 ${dir || '未设置'}`}
                title={dir}
              >
                <span className="k">安装位置</span>
                <span className="v">{dir || '—'}</span>
                {slotEditable && <span className="chg">更改</span>}
              </button>
            )}
          </motion.div>
        </div>
      </div>

      <ChannelDeck
        state={phase}
        mode={mode}
        ratio={ratio}
        keyLabel={keyLabel}
        keyDisabled={keyDisabled}
        onKey={primary}
        reduce={reduce}
      />

      <footer className="foot">
        <span className="note">{note}</span>
        {!hideClose && (
          <button type="button" className="close" onClick={() => bridge.close()} disabled={phase === 'working'}>
            关闭
          </button>
        )}
      </footer>
    </div>
  );
}
