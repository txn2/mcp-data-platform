import type { ReactNode } from "react";

// SlideOver is the shared right-side panel shell used on the Feedback page for
// the thread detail and the new-feedback composer. The backdrop is non-modal:
// it dims the page and closes on click without trapping focus.
export function SlideOver({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  return (
    <>
      <div
        className="fixed inset-0 z-40 bg-black/30 backdrop-blur-[1px]"
        onClick={onClose}
        aria-hidden="true"
      />
      <div className="fixed inset-y-0 right-0 z-50 flex w-full max-w-md flex-col border-l bg-card shadow-2xl">
        {children}
      </div>
    </>
  );
}
