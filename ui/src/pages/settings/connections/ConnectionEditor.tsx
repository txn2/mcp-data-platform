import { useId } from "react";
import { Save, Check, AlertCircle } from "lucide-react";
import type { EffectiveConnection } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AVAILABLE_KINDS } from "./constants";
import { ConfigSelect } from "./fields";
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

const KIND_OPTIONS = AVAILABLE_KINDS.map((k) => ({ value: k, label: k }));

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
  const nameID = useId();
  const nameHelpID = `${nameID}-help`;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b bg-muted/10 px-6 py-3">
        <h2 className="text-sm font-semibold">
          {connection ? `Edit: ${connection.kind}/${connection.name}` : "New Connection"}
        </h2>
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={handleSave}
            disabled={isPending || !isConfigValid || (isCreate && (!name.trim() || !nameValid))}
            // Save confirmation is a transient success state on the button
            // itself, so the confirmation lands where the click did.
            className={cn(saveSuccess && "bg-emerald-600 text-white hover:bg-emerald-600")}
          >
            {saveSuccess ? (
              <>
                <Check />
                Saved
              </>
            ) : isPending ? (
              "Saving..."
            ) : (
              <>
                <Save />
                {isCreate ? "Create" : "Save"}
              </>
            )}
          </Button>
        </div>
      </div>

      {saveError && (
        <Alert variant="destructive" className="rounded-none border-x-0 border-t-0">
          <AlertCircle />
          <AlertDescription>{saveError}</AlertDescription>
        </Alert>
      )}

      {/* Form */}
      <div className="flex-1 space-y-6 overflow-auto p-6">
        {/* Kind & Name */}
        <div className="grid grid-cols-2 gap-4">
          <ConfigSelect
            label="Kind"
            value={kind}
            onChange={setKind}
            options={KIND_OPTIONS}
            disabled={!isCreate}
            help="Connection type. Cannot be changed after creation."
          />
          <div className="space-y-1.5">
            <Label htmlFor={nameID} className="text-xs">
              Identifier
            </Label>
            <Input
              id={nameID}
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
              aria-describedby={nameHelpID}
              aria-invalid={isCreate && name.length > 0 && !nameValid ? true : undefined}
              className="font-mono"
            />
            <p id={nameHelpID} className="text-xs text-muted-foreground">
              Machine identifier used in API routes and persona patterns.
              Lowercase letters, digits, hyphens, underscores. Must start with
              a letter. Cannot be changed after creation.
            </p>
            {isCreate && name.length > 0 && !nameValid && (
              <p className="text-xs text-destructive">
                Identifier must start with a lowercase letter.
              </p>
            )}
          </div>
        </div>

        {/* Description. MarkdownEditor sizes itself to its parent, so it gets a
            plain block parent rather than a stretched grid cell. */}
        <div className="space-y-1.5">
          <Label className="text-xs">Description</Label>
          <MarkdownEditor
            value={description}
            onChange={setDescription}
            minHeight="160px"
            placeholder="What this connection is for... (usage notes, datasets/schemas, gotchas, owner/contact)"
          />
        </div>

        {/* Kind-specific configuration form */}
        <SectionCard title="Configuration">
          <div className="space-y-4">
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
        </SectionCard>
      </div>
    </div>
  );
}
