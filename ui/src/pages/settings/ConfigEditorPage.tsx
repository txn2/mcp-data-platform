import { useState, useEffect, useCallback } from "react";
import {
  useEffectiveConfig,
  useSetConfigEntry,
  useDeleteConfigEntry,
  useSystemInfo,
} from "@/api/admin/hooks";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { cn } from "@/lib/utils";
import {
  Save,
  RotateCcw,
  Database,
  Check,
  AlertCircle,
  RefreshCw,
  XCircle,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface Props {
  configKey: string;   // e.g. "server.description"
  label: string;       // e.g. "Description"
  description: string; // e.g. "Platform identity visible to MCP clients"
}

// ---------------------------------------------------------------------------
// Shared error banner
// ---------------------------------------------------------------------------

function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex items-center gap-2 border-b bg-red-50 px-5 py-2.5 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
      <XCircle className="h-3.5 w-3.5 shrink-0" />
      <span className="flex-1">{message}</span>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium hover:bg-red-100 dark:hover:bg-red-900/30"
        >
          <RefreshCw className="h-3 w-3" />
          Retry
        </button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ConfigEditorPage
// ---------------------------------------------------------------------------

export function ConfigEditorPage({ configKey, label, description }: Props) {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: effective, error: effectiveError, refetch: refetchEffective } = useEffectiveConfig();
  const entry = (effective ?? []).find((e) => e.key === configKey);

  const [value, setValue] = useState(entry?.value ?? "");
  const [dirty, setDirty] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const setEntry = useSetConfigEntry();
  const deleteEntry = useDeleteConfigEntry();

  // Sync from server.
  useEffect(() => {
    setValue(entry?.value ?? "");
    setDirty(false);
    setSaveSuccess(false);
    setSaveError(null);
  }, [entry?.value, configKey]);

  const handleChange = useCallback(
    (newValue: string) => {
      setValue(newValue);
      setDirty(newValue !== (entry?.value ?? ""));
      setSaveSuccess(false);
      setSaveError(null);
    },
    [entry?.value],
  );

  const handleSave = useCallback(() => {
    setSaveError(null);
    setEntry.mutate(
      { key: configKey, value },
      {
        onSuccess: () => {
          setDirty(false);
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2500);
        },
        onError: (err) => {
          setSaveError(err instanceof Error ? err.message : "Failed to save");
        },
      },
    );
  }, [configKey, value, setEntry]);

  const handleRevert = useCallback(() => {
    setSaveError(null);
    deleteEntry.mutate(configKey, {
      onSuccess: () => {
        setDirty(false);
        setSaveSuccess(false);
      },
      onError: (err) => {
        setSaveError(err instanceof Error ? err.message : "Failed to revert");
      },
    });
  }, [configKey, deleteEntry]);

  const hasOverride = entry?.source === "database";
  const saving = setEntry.isPending;
  const reverting = deleteEntry.isPending;

  return (
    <div className="flex h-[calc(100vh-8rem)] flex-col overflow-hidden rounded-lg border bg-card">
      {isReadOnly && (
        <div className="flex items-center gap-2 border-b bg-amber-50/50 px-5 py-2 text-xs text-amber-700 dark:bg-amber-950/20 dark:text-amber-400">
          <AlertCircle className="h-3.5 w-3.5" />
          Configuration is read-only — no database configured. Set <code className="font-mono">database.dsn</code> to enable editing.
        </div>
      )}
      {effectiveError && (
        <ErrorBanner
          message="Failed to load configuration. The server may be unavailable."
          onRetry={() => void refetchEffective()}
        />
      )}

      {/* Header bar */}
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div className="flex items-center gap-3">
          <div>
            <h3 className="text-sm font-semibold leading-none">{label}</h3>
            <p className="mt-1 text-xs text-muted-foreground">{description}</p>
          </div>
          {hasOverride && (
            <span className="rounded-full border border-primary/20 bg-primary/5 px-2.5 py-0.5 text-xs font-medium text-primary">
              <Database className="mr-1 inline-block h-2.5 w-2.5" />
              Database override
            </span>
          )}
        </div>

        <div className="flex items-center gap-2">
          {entry?.updated_by && (
            <span className="text-xs text-muted-foreground">
              Updated by {entry.updated_by}
              {entry.updated_at &&
                ` · ${new Date(entry.updated_at).toLocaleDateString()}`}
            </span>
          )}

          {hasOverride && !isReadOnly && (
            <button
              type="button"
              onClick={handleRevert}
              disabled={reverting}
              className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
            >
              <RotateCcw className="h-3 w-3" />
              {reverting ? "Reverting..." : "Revert"}
            </button>
          )}

          {!isReadOnly && (
            <button
              type="button"
              onClick={handleSave}
              disabled={!dirty || saving}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all disabled:opacity-50",
                saveSuccess
                  ? "bg-green-600 text-white"
                  : "bg-primary text-primary-foreground hover:bg-primary/90",
              )}
            >
              {saveSuccess ? (
                <>
                  <Check className="h-3 w-3" />
                  Saved
                </>
              ) : saving ? (
                "Saving..."
              ) : (
                <>
                  <Save className="h-3 w-3" />
                  Save
                </>
              )}
            </button>
          )}
        </div>
      </div>

      {/* Error banner */}
      {saveError && (
        <ErrorBanner message={saveError} />
      )}

      {/* Unsaved changes indicator */}
      {dirty && !saveError && (
        <div className="flex items-center gap-2 border-b bg-amber-50/50 px-5 py-1.5 text-[11px] text-amber-700 dark:bg-amber-950/20 dark:text-amber-400">
          <AlertCircle className="h-3 w-3" />
          You have unsaved changes
        </div>
      )}

      {/* Editor body — takes remaining space */}
      <div className="flex-1 overflow-hidden p-4">
        <MarkdownEditor
          value={value}
          onChange={handleChange}
          readOnly={isReadOnly}
          minHeight="100%"
        />
      </div>
    </div>
  );
}
