import { useCallback, useEffect, useState } from "react";
import { useUnsavedChangesGuard } from "@/lib/navigationGuard";

// useSettingsDraft is the edit state a settings card keeps over the value the
// server holds: the draft, whether it differs from what was loaded, and the
// save's outcome. It is reset to the server's value each time that loads.
export function useSettingsDraft<T>(server: T | undefined) {
  const [draft, setDraft] = useState<T | undefined>(server);
  const [dirty, setDirty] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  useUnsavedChangesGuard(dirty);

  useEffect(() => {
    if (!server) return;
    setDraft(server);
    setDirty(false);
  }, [server]);

  const edit = useCallback((next: T) => {
    setDraft(next);
    setDirty(true);
    setSaveSuccess(false);
    setSaveError(null);
  }, []);

  const saved = useCallback(() => {
    setDirty(false);
    setSaveSuccess(true);
    setTimeout(() => setSaveSuccess(false), 2500);
  }, []);

  const failed = useCallback((err: unknown) => {
    setSaveError(err instanceof Error ? err.message : "Failed to save");
  }, []);

  return { draft, edit, dirty, saveSuccess, saveError, saved, failed, clearError: () => setSaveError(null) };
}
