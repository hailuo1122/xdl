import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { motion } from 'motion/react';
import type { Mode, Phase } from '../bridge';

const LANES = 8;
/** 每条连接的速度不一样：有的先跑满，最后两条总是慢半拍 */
const BIAS = [1.07, 0.98, 1.03, 0.91, 1.09, 0.95, 1.01, 0.87];

const EASE: [number, number, number, number] = [0.16, 1, 0.3, 1];
const clamp01 = (n: number) => Math.min(1, Math.max(0, n));

/** 数字读数追一下目标值，像仪表指针落位，而不是跳数 */
function useChase(target: number, active: boolean, reduce: boolean) {
  const [shown, setShown] = useState(target);
  const ref = useRef(target);

  useEffect(() => {
    if (reduce || !active) {
      ref.current = target;
      setShown(target);
      return;
    }
    let raf = 0;
    let last = performance.now();
    const tick = (t: number) => {
      const dt = Math.min(80, t - last);
      last = t;
      const k = 1 - Math.pow(0.0009, dt / 1000);
      ref.current += (target - ref.current) * k;
      if (Math.abs(target - ref.current) < 0.0008) ref.current = target;
      setShown(ref.current);
      if (ref.current !== target) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [target, active, reduce]);

  return shown;
}

type Props = {
  state: Phase;
  mode: Mode;
  ratio: number;
  keyLabel: string;
  keyDisabled: boolean;
  onKey: () => void;
  reduce: boolean;
};

export function ChannelDeck({ state, mode, ratio, keyLabel, keyDisabled, onKey, reduce }: Props) {
  const done = state === 'done';
  const edge = clamp01(ratio);

  const readout = mode === 'install' ? edge : 1 - edge;
  const active = state !== 'ready';
  const shown = useChase(active ? readout : 0, active, reduce);

  const laneValue = (i: number) => {
    if (state === 'ready') return mode === 'uninstall' ? 1 : 0;
    if (state === 'done') return mode === 'uninstall' ? 0 : 1;
    const v = clamp01(edge * BIAS[i]);
    return mode === 'uninstall' ? 1 - v : v;
  };

  const stateWord =
    state === 'ready'
      ? '待命'
      : state === 'working'
        ? mode === 'install'
          ? '写入中'
          : '移除中'
        : state === 'done'
          ? mode === 'install'
            ? '完成'
            : '已移除'
          : '中断';

  return (
    <motion.section
      className="deck"
      data-state={state}
      data-mode={mode}
      aria-label="传输通道"
      initial={reduce ? false : { opacity: 0, y: 16 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.68, ease: EASE }}
    >
      <div className="deck-lanes">
        <div className="deck-head">
          <span className="deck-title">传输通道</span>
          <span className="sep" aria-hidden="true" />
          <span className="deck-count">
            <b>{LANES}</b> 条连接
          </span>
          <span className="deck-state">
            <i className="dot" aria-hidden="true" />
            {stateWord}
          </span>
        </div>

        <div
          className="lanes"
          role="progressbar"
          aria-label={mode === 'install' ? '安装进度' : '卸载进度'}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={state === 'ready' ? 0 : Math.round(readout * 100)}
        >
          {Array.from({ length: LANES }, (_, i) => (
            <div
              key={i}
              className="lane"
              style={
                {
                  '--i': i,
                  '--p': laneValue(i),
                  '--d': `${done ? i * 38 : i * 16}ms`,
                } as CSSProperties
              }
            >
              <span className="track" />
              <span className="fill" />
              <span className="solid" />
              <span className="tipwrap" />
              <span className="ping">
                <i />
              </span>
            </div>
          ))}

          {state === 'error' && edge > 0 && (
            <span className="fault" style={{ '--p': mode === 'install' ? edge : 1 - edge } as CSSProperties}>
              <i />
            </span>
          )}
        </div>
      </div>

      <div className="bay">
        <div className="readout" aria-hidden="true">
          <b>{state === 'ready' ? '—' : Math.round(clamp01(shown) * 100)}</b>
          {state !== 'ready' && <span>%</span>}
        </div>
        <span className="bay-rule" aria-hidden="true" />
        <div className="key-well">
          <motion.button
            type="button"
            className="key"
            onClick={onKey}
            disabled={keyDisabled}
            initial={reduce ? false : { opacity: 0, scale: 0.975 }}
            animate={{ opacity: 1, scale: 1 }}
            transition={{ delay: 0.52, duration: 0.5, ease: EASE }}
            style={{ '--k': edge } as CSSProperties}
          >
            {keyLabel}
          </motion.button>
        </div>
      </div>

      <motion.span
        className="seal"
        aria-hidden="true"
        initial={false}
        animate={{ scaleX: done ? 1 : 0 }}
        transition={{ duration: reduce ? 0 : 0.5, ease: EASE, delay: reduce ? 0 : 0.12 }}
      />
    </motion.section>
  );
}
