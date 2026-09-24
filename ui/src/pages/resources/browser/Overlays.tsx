import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/utils";

/** One entry of the context menu. */
export interface MenuItem {
  id: string;
  label: string;
  shortcut?: string;
  danger?: boolean;
  /** Draws a rule above the item. */
  separated?: boolean;
  run: () => void;
}

/**
 * The context menu (#1872): Open, Rename, Move to, Tag, Copy URI and Delete on
 * a row; New folder and Upload here on empty space. It is placed at the
 * pointer, kept on screen, takes focus on its first item, and closes on
 * Escape, on a click elsewhere, and after an item runs.
 */
export function ContextMenu({
  x,
  y,
  items,
  onClose,
}: {
  x: number;
  y: number;
  items: MenuItem[];
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ left: x, top: y });

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setPos({
      left: Math.max(8, Math.min(x, window.innerWidth - r.width - 8)),
      top: Math.max(8, Math.min(y, window.innerHeight - r.height - 8)),
    });
    el.querySelector<HTMLButtonElement>("button")?.focus();
  }, [x, y]);

  useEffect(() => {
    const away = (e: Event) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    window.addEventListener("blur", onClose);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
      window.removeEventListener("blur", onClose);
    };
  }, [onClose]);

  const move = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const buttons = [...(ref.current?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
    const i = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next = e.key === "ArrowDown" ? (i + 1) % buttons.length : (i - 1 + buttons.length) % buttons.length;
    buttons[next]?.focus();
  };

  return createPortal(
    <div
      ref={ref}
      role="menu"
      data-testid="context-menu"
      className="fixed z-50 min-w-[190px] rounded-lg border bg-popover p-1 text-sm text-popover-foreground shadow-lg"
      style={pos}
      onKeyDown={move}
      onContextMenu={(e) => e.preventDefault()}
    >
      {items.map((item) => (
        <div key={item.id} role="none">
          {item.separated && <hr className="my-1 border-border" />}
          <button
            type="button"
            role="menuitem"
            className={cn(
              "flex w-full justify-between gap-5 rounded-[5px] px-2.5 py-1.5 text-left outline-none hover:bg-accent focus-visible:bg-accent",
              item.danger && "text-destructive",
            )}
            onClick={() => {
              onClose();
              item.run();
            }}
          >
            {item.label}
            {item.shortcut && <span className="text-xs text-muted-foreground">{item.shortcut}</span>}
          </button>
        </div>
      ))}
    </div>,
    document.body,
  );
}

/** A message at the foot of the page, with an optional Undo. */
export interface ToastState {
  id: number;
  message: string;
  undo?: () => void;
}

/**
 * useToast holds the one toast the page shows. A toast with an Undo stays six
 * seconds, long enough to reach the button; any other three.
 */
export function useToast() {
  const [toast, setToast] = useState<ToastState | null>(null);
  const seq = useRef(0);
  const show = useCallback((message: string, undo?: () => void) => {
    seq.current += 1;
    setToast({ id: seq.current, message, undo });
  }, []);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast((cur) => (cur?.id === toast.id ? null : cur)), toast.undo ? 6000 : 3200);
    return () => clearTimeout(t);
  }, [toast]);
  return { toast, show, dismiss: () => setToast(null) };
}

export function Toast({ toast, onDismiss }: { toast: ToastState | null; onDismiss: () => void }) {
  if (!toast) return null;
  return createPortal(
    <div
      role="status"
      data-testid="toast"
      className="fixed bottom-6 left-1/2 z-[60] flex -translate-x-1/2 items-center gap-3.5 rounded-lg bg-foreground px-3.5 py-2 text-[13px] text-background shadow-lg"
    >
      <span>{toast.message}</span>
      {toast.undo && (
        <button
          type="button"
          className="font-semibold text-[hsl(217_91%_65%)] underline-offset-2 hover:underline dark:text-primary"
          onClick={() => {
            onDismiss();
            toast.undo?.();
          }}
        >
          Undo
        </button>
      )}
    </div>,
    document.body,
  );
}
