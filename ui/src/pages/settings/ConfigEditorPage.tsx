import { useState, useEffect, useCallback } from "react";
import {
  useEffectiveConfig,
  useSetConfigEntry,
  useDeleteConfigEntry,
  useSystemInfo,
  useAgentInstructionsBaseline,
} from "@/api/admin/hooks";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { RotateCcw, Database, Layers } from "lucide-react";
import { PanelShell } from "./panels";
import {
  ErrorBanner,
  ReadOnlyBanner,
  SaveButton,
  UnsavedChangesBanner,
  UpdatedByMeta,
} from "./settingsChrome";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface Props {
  configKey: string;   // e.g. "server.description"
  label: string;       // e.g. "Description"
  description: string; // e.g. "Platform identity visible to MCP clients"
  // showPlatformBaseline renders the read-only platform-owned instruction
  // baseline (#646) above the editor, so admins see the "how to operate"
  // guidance the platform always applies beneath their text and write only
  // business context. Enabled for server.agent_instructions.
  showPlatformBaseline?: boolean;
}

// ---------------------------------------------------------------------------
// Platform baseline panel (read-only)
// ---------------------------------------------------------------------------

export function PlatformBaselinePanel() {
  const { data, isLoading } = useAgentInstructionsBaseline();
  const baseline = data?.baseline?.trim();
  if (isLoading || !baseline) return null;
  return (
    <details
      open
      className="border-b bg-muted/40"
      data-testid="platform-baseline-panel"
    >
      <summary className="flex cursor-pointer list-none items-center gap-2 px-5 py-2.5 text-xs font-medium text-muted-foreground hover:text-foreground">
        <Layers className="h-3.5 w-3.5 shrink-0" />
        <span className="flex-1">
          Platform baseline
          <span className="ml-2 font-normal text-muted-foreground/80">
            always applied beneath your instructions; names only tools this deployment exposes
          </span>
        </span>
      </summary>
      <div className="px-5 pb-3">
        <pre className="whitespace-pre-wrap rounded-md border bg-background/60 p-3 font-sans text-xs leading-relaxed text-muted-foreground">
          {baseline}
        </pre>
        <p className="mt-2 text-[11px] text-muted-foreground/80">
          You don&apos;t need to restate this. Use the editor below for
          business and deployment context (which backends hold what, data
          origins, domain rules).
        </p>
      </div>
    </details>
  );
}

// ---------------------------------------------------------------------------
// ConfigEditorPage
// ---------------------------------------------------------------------------

export function ConfigEditorPage({ configKey, label, description, showPlatformBaseline }: Props) {
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
    <PanelShell
      title={label}
      description={description}
      notices={
        <>
          {isReadOnly && <ReadOnlyBanner />}
          {effectiveError && (
            <ErrorBanner
              message="Failed to load configuration. The server may be unavailable."
              onRetry={() => void refetchEffective()}
            />
          )}
        </>
      }
      action={
        <>
          {/* Where the value comes from decides what Revert does, so the
              override state is stated next to the button that clears it. */}
          {hasOverride && (
            <Badge variant="info">
              <Database />
              Database override
            </Badge>
          )}
          <UpdatedByMeta
            updatedBy={entry?.updated_by}
            updatedAt={entry?.updated_at}
          />
          {hasOverride && !isReadOnly && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={handleRevert}
              disabled={reverting}
            >
              <RotateCcw />
              {reverting ? "Reverting..." : "Revert"}
            </Button>
          )}
          {!isReadOnly && (
            <SaveButton
              dirty={dirty}
              saving={saving}
              saveSuccess={saveSuccess}
              onSave={handleSave}
            />
          )}
        </>
      }
    >
      {saveError && <ErrorBanner message={saveError} />}
      {dirty && !saveError && <UnsavedChangesBanner />}

      {/* Platform-owned instruction baseline (read-only) */}
      {showPlatformBaseline && <PlatformBaselinePanel />}

      {/* Editor body — takes remaining space */}
      <div className="flex-1 overflow-hidden p-4">
        <MarkdownEditor
          value={value}
          onChange={handleChange}
          readOnly={isReadOnly}
          minHeight="100%"
        />
      </div>
    </PanelShell>
  );
}
