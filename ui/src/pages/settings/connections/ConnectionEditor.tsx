import { Save, Check, AlertCircle } from "lucide-react";
import type { EffectiveConnection } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { AVAILABLE_KINDS } from "./constants";
import { useConnectionForm } from "./useConnectionForm";
import { TrinoConfigForm } from "./TrinoConfigForm";
import { S3ConfigForm } from "./S3ConfigForm";
import { GatewayConfigForm } from "./GatewayConfigForm";
import { ApiGatewayConfigForm } from "./ApiGatewayConfigForm";

interface EditorProps {
  connection: EffectiveConnection | null; // null = create mode
  onSave: (savedKind: string, savedName: string) => void;
  onCancel: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

export function ConnectionEditor({ connection, onSave, onCancel, onDirtyChange }: EditorProps) {
  const {
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
    isPending,
  } = useConnectionForm({ connection, onSave, onDirtyChange });

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-6 py-3 bg-muted/10">
        <h2 className="text-sm font-semibold">
          {connection ? `Edit: ${connection.kind}/${connection.name}` : "New Connection"}
        </h2>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={isPending || !isConfigValid || (isCreate && (!name.trim() || !nameValid))}
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
            ) : isPending ? (
              "Saving..."
            ) : (
              <>
                <Save className="h-3 w-3" />
                {isCreate ? "Create" : "Save"}
              </>
            )}
          </button>
        </div>
      </div>

      {saveError && (
        <div className="flex items-center gap-2 border-b bg-red-50 px-6 py-2 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
          <AlertCircle className="h-3.5 w-3.5" />
          {saveError}
        </div>
      )}

      {/* Form */}
      <div className="flex-1 overflow-auto p-6 space-y-6">
        {/* Kind & Name */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="mb-1 block text-xs font-medium">Kind</label>
            <select
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              disabled={!isCreate}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {AVAILABLE_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
            <p className="mt-1 text-xs text-muted-foreground">
              Connection type. Cannot be changed after creation.
            </p>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium">
              Identifier
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => {
                if (!isCreate) return;
                const raw = e.target.value.toLowerCase();
                const cleaned = raw.replace(/[^a-z0-9_-]/g, "");
                setName(cleaned);
              }}
              disabled={!isCreate}
              placeholder="prod-trino"
              pattern="^[a-z][a-z0-9_-]*$"
              maxLength={64}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              aria-describedby="connection-name-help"
              className="w-full rounded-md border bg-background px-3 py-2 text-sm font-mono outline-none ring-ring focus:ring-2 disabled:opacity-50 disabled:cursor-not-allowed"
            />
            <p
              id="connection-name-help"
              className="mt-1 text-xs text-muted-foreground"
            >
              Machine identifier used in API routes and persona patterns.
              Lowercase letters, digits, hyphens, underscores. Must start with
              a letter. Cannot be changed after creation.
            </p>
            {isCreate && name.length > 0 && !nameValid && (
              <p className="mt-1 text-xs text-destructive">
                Identifier must start with a lowercase letter.
              </p>
            )}
          </div>
        </div>

        {/* Description */}
        <div>
          <label className="mb-1 block text-xs font-medium">Description</label>
          <MarkdownEditor
            value={description}
            onChange={setDescription}
            minHeight="160px"
            placeholder="What this connection is for... (usage notes, datasets/schemas, gotchas, owner/contact)"
          />
        </div>

        {/* Kind-specific configuration form */}
        <div className="rounded-lg border">
          <div className="px-4 py-3 border-b bg-muted/10">
            <span className="text-sm font-medium">Configuration</span>
          </div>
          <div className="px-4 py-4 space-y-4">
            {kind === "trino" && (
              <TrinoConfigForm config={configObj} onChange={updateConfig} />
            )}
            {kind === "s3" && (
              <S3ConfigForm config={configObj} onChange={updateConfig} />
            )}
            {kind === "mcp" && (
              <GatewayConfigForm config={configObj} onChange={updateConfig} />
            )}
            {kind === "api" && (
              <ApiGatewayConfigForm
                config={configObj}
                onChange={updateConfig}
                connectionName={name}
                isCreate={isCreate}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
