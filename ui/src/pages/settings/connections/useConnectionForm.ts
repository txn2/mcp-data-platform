import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useSetConnectionInstance } from "@/api/admin/hooks";
import type { EffectiveConnection } from "@/api/admin/types";

// useConnectionForm owns the common create/edit lifecycle for a connection
// instance: kind/name/description/config state, dirty tracking, per-kind
// validity, and the save mutation. Extracted from ConnectionsPanel.tsx (#766)
// so the editor's state transitions are exercisable in isolation rather than
// buried in the panel's render, and so per-kind config forms stay stateless
// controlled components that only receive `config` + `onChange`.
export interface UseConnectionFormArgs {
  connection: EffectiveConnection | null; // null = create mode
  onSave: (savedKind: string, savedName: string) => void;
  onDirtyChange: (dirty: boolean) => void;
}

export function useConnectionForm({
  connection,
  onSave,
  onDirtyChange,
}: UseConnectionFormArgs) {
  const isCreate = !connection;
  const setMutation = useSetConnectionInstance();
  const [kind, setKind] = useState(connection?.kind ?? "trino");
  const [name, setName] = useState(connection?.name ?? "");
  const nameValid = !isCreate || /^[a-z][a-z0-9_-]*$/.test(name);
  const [description, setDescription] = useState(
    connection?.description || (connection?.config?.description as string) || "",
  );
  const [configObj, setConfigObj] = useState<Record<string, unknown>>(
    connection?.config ? { ...connection.config } : {},
  );
  // configObjRef mirrors configObj synchronously so handleSave can
  // read the latest value even when the Save click follows a child
  // editor's onChange in the same task. React schedules setConfigObj
  // asynchronously; the closure-captured configObj is one render
  // behind. The ref bridges the gap so a keystroke that landed in
  // the keystroke-eager SensitiveKeyValueEditor immediately before
  // the Save click is included in the PUT body.
  const configObjRef = useRef(configObj);
  const updateConfig = useCallback((next: Record<string, unknown>) => {
    configObjRef.current = next;
    setConfigObj(next);
  }, []);
  const configJson = JSON.stringify(configObj); // for dirty tracking
  const isConfigValid = useMemo(() => {
    switch (kind) {
      case "trino": return Boolean(configObj.host);
      case "mcp": return Boolean(configObj.endpoint);
      case "api": return Boolean(configObj.base_url);
      default: return true;
    }
  }, [kind, configObj]);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Track dirty state
  useEffect(() => {
    if (isCreate) {
      onDirtyChange(!!name.trim());
    } else {
      const origDesc = connection?.description || (connection?.config?.description as string) || "";
      const origJson = JSON.stringify(connection?.config ?? {});
      onDirtyChange(
        description !== origDesc || configJson !== origJson,
      );
    }
  }, [kind, name, description, configJson, connection, isCreate, onDirtyChange]);

  // Reset config when kind changes in create mode
  useEffect(() => {
    if (isCreate) {
      configObjRef.current = {};
      setConfigObj({});
    }
  }, [kind, isCreate]);

  const handleSave = useCallback(() => {
    setSaveError(null);
    // Read from ref, not state, so a keystroke that just propagated
    // through a child editor's onChange is included even when the
    // Save button is the next click in the same task. The closure-
    // captured configObj is one render behind; configObjRef is
    // updated synchronously by updateConfig before React rerenders.
    const config = configObjRef.current;

    setMutation.mutate(
      {
        kind,
        name,
        config,
        description: description || undefined,
      },
      {
        onSuccess: () => {
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2000);
          onSave(kind, name);
        },
        onError: (err) => {
          setSaveError(err instanceof Error ? err.message : "Failed to save");
        },
      },
    );
  }, [kind, name, description, setMutation, onSave]);

  return {
    isCreate,
    kind,
    setKind,
    name,
    setName,
    nameValid,
    description,
    setDescription,
    configObj,
    updateConfig,
    isConfigValid,
    saveSuccess,
    saveError,
    handleSave,
    isPending: setMutation.isPending,
  };
}
