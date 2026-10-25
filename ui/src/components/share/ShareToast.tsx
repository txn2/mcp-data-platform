import { useCallback, useEffect, useRef, useState } from "react";

/** One message the dialog shows, with an optional Undo. */
interface ToastState {
  id: number;
  message: string;
  undo?: () => void;
}

/** How long a toast with an Undo stays, which is how long the Undo is offered. */
export const UNDO_MS = 5000;
const PLAIN_MS = 3000;

/**
 * useShareToast holds the one toast the share dialog shows. It is drawn inside
 * the dialog rather than on the page: a modal dialog takes the pointer from
 * everything outside it, and the Undo has to be reachable.
 */
export function useShareToast() {
  const [toast, setToast] = useState<ToastState | null>(null);
  const seq = useRef(0);
  const show = useCallback((message: string, undo?: () => void) => {
    seq.current += 1;
    setToast({ id: seq.current, message, undo });
  }, []);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast((cur) => (cur?.id === toast.id ? null : cur)), toast.undo ? UNDO_MS : PLAIN_MS);
    return () => clearTimeout(t);
  }, [toast]);
  return { toast, show, dismiss: () => setToast(null) };
}

/** ShareToast is the toast, announced to a screen reader as it appears. */
export function ShareToast({ toast, onDismiss }: { toast: ToastState | null; onDismiss: () => void }) {
  return (
    <div role="status" aria-live="polite" className="pointer-events-none absolute inset-x-0 bottom-20 z-10 flex justify-center px-4">
      {toast && (
        <div
          data-testid="share-toast"
          className="pointer-events-auto flex items-center gap-3.5 rounded-lg bg-foreground px-3.5 py-2 text-[13px] text-background shadow-lg"
        >
          <span>{toast.message}</span>
          {toast.undo && (
            <button
              type="button"
              className="font-semibold text-blue-300 underline-offset-2 hover:underline dark:text-blue-700"
              onClick={() => {
                onDismiss();
                toast.undo?.();
              }}
            >
              Undo
            </button>
          )}
        </div>
      )}
    </div>
  );
}
