import { Icon } from "./Icon";

export type ToastKind = "info" | "success" | "error";

export interface Toast {
  id: number;
  kind: ToastKind;
  text: string;
}

export function Toasts({ items, onDismiss }: { items: Toast[]; onDismiss: (id: number) => void }) {
  return (
    <div className="toast-stack" aria-live="polite">
      {items.map((t) => (
        <div key={t.id} className={`toast toast-${t.kind}`}>
          <span className="toast-icon">
            <Icon
              name={t.kind === "success" ? "done" : t.kind === "error" ? "failed" : "download"}
              size={15}
            />
          </span>
          <span className="toast-text">{t.text}</span>
          <button className="toast-close" onClick={() => onDismiss(t.id)} aria-label="关闭提示">
            <Icon name="x" size={13} />
          </button>
        </div>
      ))}
    </div>
  );
}
