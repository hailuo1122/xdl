import { motion, useReducedMotion } from 'motion/react';

type Props = {
  /** 完成后第二枚箭头向第一枚方向轻轻一磕 */
  sealed?: boolean;
  className?: string;
};

/**
 * 品牌符号：两枚右向 chevron。
 * 第一枚是墨色，第二枚是铜色 —— 铜只出现在这里，用来点出「第二股流」。
 */
export function Mark({ sealed = false, className = '' }: Props) {
  const reduce = !!useReducedMotion();

  const common = {
    strokeWidth: 3.8,
    strokeLinejoin: 'miter' as const,
    strokeLinecap: 'butt' as const,
    initial: reduce ? false : { pathLength: 0 },
    animate: { pathLength: 1 },
    transition: { duration: 0.46, delay: 0.34, ease: [0.22, 1, 0.36, 1] as const },
  };

  return (
    <svg
      className={`mark ${className}`}
      viewBox="0 0 40 40"
      fill="none"
      aria-hidden="true"
      focusable="false"
    >
      <motion.path d="M6 10.5 L18.5 20 L6 29.5" stroke="currentColor" {...common} />
      <motion.path
        d="M19.5 10.5 L32 20 L19.5 29.5"
        stroke="var(--brass)"
        {...common}
        initial={reduce ? false : { pathLength: 0, x: 0 }}
        animate={{ pathLength: 1, x: sealed ? [0, -2.6, 0] : 0 }}
        transition={{
          pathLength: { duration: 0.46, delay: 0.4, ease: [0.22, 1, 0.36, 1] },
          x: { duration: 0.42, times: [0, 0.42, 1], ease: 'easeOut' },
        }}
      />
    </svg>
  );
}
