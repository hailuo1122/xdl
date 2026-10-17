import { useEffect, useId, useRef, useState } from "react";

export interface SelectOption<T extends string | number = string> {
  value: T;
  label: string;
}

interface SelectProps<T extends string | number> {
  value: T;
  options: SelectOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
}

export function Select<T extends string | number>({
  value,
  options,
  onChange,
  disabled,
  className = "",
  "aria-label": ariaLabel,
}: SelectProps<T>) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const listId = useId();
  const selected = options.find((o) => o.value === value) ?? options[0];
  const activeIndex = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  );

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const pick = (next: T) => {
    onChange(next);
    setOpen(false);
  };

  const move = (delta: number) => {
    if (!options.length) return;
    const i = (activeIndex + delta + options.length) % options.length;
    onChange(options[i]!.value);
  };

  return (
    <div
      className={`xdl-select ${open ? "open" : ""} ${className}`.trim()}
      ref={rootRef}
    >
      <button
        type="button"
        className="xdl-select-trigger"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-label={ariaLabel}
        onClick={() => !disabled && setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            if (!open) setOpen(true);
            else move(1);
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            if (!open) setOpen(true);
            else move(-1);
          } else if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setOpen((v) => !v);
          }
        }}
      >
        <span className="xdl-select-value">{selected?.label ?? ""}</span>
        <svg
          className="xdl-select-chevron"
          viewBox="0 0 16 16"
          width="12"
          height="12"
          aria-hidden="true"
        >
          <path
            d="M4 6l4 4 4-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>
      {open && (
        <ul className="xdl-select-menu" role="listbox" id={listId}>
          {options.map((o) => {
            const active = o.value === value;
            return (
              <li key={String(o.value)} role="presentation">
                <button
                  type="button"
                  role="option"
                  aria-selected={active}
                  className={`xdl-select-option ${active ? "active" : ""}`}
                  onClick={() => pick(o.value)}
                >
                  <span>{o.label}</span>
                  {active && (
                    <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
                      <path
                        d="M3.5 8.2l2.8 2.8 6.2-6.2"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="1.8"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      />
                    </svg>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
