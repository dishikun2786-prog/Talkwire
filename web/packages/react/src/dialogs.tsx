import { useEffect, useState, type ReactNode } from "react";
import { useImKit } from "./context.js";

/** A modal card over the page; Escape or a click outside closes it. */
export function Dialog({ label, children, onClose }: { label: string; children: ReactNode; onClose(): void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="imkit-dialog" role="dialog" aria-modal="true" aria-label={label} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="imkit-dialog-card">{children}</div>
    </div>
  );
}

/** Asks before something that cannot be undone; the action button is drawn as destructive. */
export function ConfirmDialog({ message, action, onConfirm, onClose }: { message: string; action: string; onConfirm(): void; onClose(): void }) {
  const { strings } = useImKit();
  return (
    <Dialog label={action} onClose={onClose}>
      <p className="imkit-dialog-text">{message}</p>
      <div className="imkit-dialog-actions">
        <button type="button" className="imkit-text-button" onClick={onClose}>{strings.cancel}</button>
        <button
          type="button"
          className="imkit-text-button imkit-danger-text"
          onClick={() => {
            onClose();
            onConfirm();
          }}
        >
          {action}
        </button>
      </div>
    </Dialog>
  );
}

/** A floating menu at a point; any click or Escape closes it (not the click that opened it). */
export function PopupMenu({ x, y, children, onClose }: { x: number; y: number; children: ReactNode; onClose(): void }) {
  useEffect(() => {
    const timer = setTimeout(() => window.addEventListener("click", onClose), 0);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("click", onClose);
      window.removeEventListener("keydown", onKey);
    };
  }, [onClose]);
  return (
    <div className="imkit-menu" role="menu" style={{ left: Math.max(0, Math.min(x, window.innerWidth - 180)), top: Math.max(0, Math.min(y, window.innerHeight - 160)) }}>
      {children}
    </div>
  );
}

/** A short message that goes away by itself. */
export function useToast(): [string | null, (text: string) => void] {
  const [toast, setToast] = useState<string | null>(null);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2500);
    return () => clearTimeout(t);
  }, [toast]);
  return [toast, setToast];
}
