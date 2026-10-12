import { useEffect, useId } from "react";

// A form with unsaved changes registers here, and the shell's navigate() asks
// before leaving it (#2093): a section switch, a sidebar link, any in-app
// navigation. Closing or reloading the tab is asked by the browser's own
// beforeunload prompt. Browser Back is a popstate the page learns of after the
// fact, so it is not asked.

const dirtyForms = new Set<string>();

// DISCARD_PROMPT is the one question every unsaved-changes guard asks.
export const DISCARD_PROMPT = "Discard unsaved changes?";

// confirmLeave reports whether navigation may proceed: true when no form has
// unsaved changes, or the person agreed to discard them.
export function confirmLeave(): boolean {
  if (dirtyForms.size === 0) return true;
  if (!window.confirm(DISCARD_PROMPT)) return false;
  dirtyForms.clear();
  return true;
}

// hasUnsavedChanges reports whether any registered form is dirty.
export function hasUnsavedChanges(): boolean {
  return dirtyForms.size > 0;
}

function onBeforeUnload(e: BeforeUnloadEvent) {
  if (dirtyForms.size === 0) return;
  e.preventDefault();
}

// useUnsavedChangesGuard registers the calling form while dirty is true, and
// releases it when it is saved or the form unmounts.
export function useUnsavedChangesGuard(dirty: boolean) {
  const id = useId();
  useEffect(() => {
    if (!dirty) return;
    dirtyForms.add(id);
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => {
      dirtyForms.delete(id);
      if (dirtyForms.size === 0) window.removeEventListener("beforeunload", onBeforeUnload);
    };
  }, [dirty, id]);
}
