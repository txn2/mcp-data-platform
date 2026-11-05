import type { ReactNode } from "react";
import { useEscapeToClose } from "@/hooks/useEscapeToClose";

// SlideOver is the shared right-side panel shell used on the Feedback page for
// the thread detail and the new-feedback composer. The backdrop is non-modal:
// it dims the page without trapping focus, and a click on it does not close
// the panel, since the composer's typing would be lost (#2468). Escape closes
// it, through the hook the other shells use.
export function SlideOver({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  useEscapeToClose(onClose);
  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/30 backdrop-blur-[1px]" aria-hidden="true" />
      <div className="fixed inset-y-0 right-0 z-50 flex w-full max-w-md flex-col border-l bg-card shadow-2xl">
        {children}
      </div>
    </>
  );
}
