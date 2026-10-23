import { useEffect } from "react";
import { AlertCircle, Info } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import type { PersonaReferences } from "@/api/admin/types";
import type { PersonaDraft } from "./persona/types";
import { EditorHeader } from "./persona/EditorHeader";
import { SectionIndex } from "./persona/sections";
import { IdentitySection } from "./persona/IdentitySection";
import { AccessSummary } from "./persona/AccessSummary";
import { ToolsSection, ConnectionsSection, ApiEndpointsSection } from "./persona/RuleSections";
import { InstructionsSection } from "./persona/InstructionsSection";
import { PortalAccessSection } from "./persona/PortalAccessSection";
import { usePersonaEditor } from "./persona/usePersonaEditor";

// Public re-exports keep the import surface stable for existing consumers:
// PersonasPanel imports { PersonaEditor, type PersonaDraft } and the
// PersonaEditor.resolve.test.ts suite imports { resolve } from this module.
export { resolve } from "./persona/resolve";
export type { PersonaDraft } from "./persona/types";

// PersonaEditor is one page of sections grouped by the surface each setting
// governs (#2257): who gets the persona, what it reaches as the server
// computes it, the AI assistant's tools, connections, API endpoints and
// instructions, and the portal. The index on the left jumps between them.

interface PersonaEditorProps {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  onSave: () => void;
  onCancel: () => void;
  isCreate: boolean;
  dirty: boolean;
  selectedName: string | null;
  canDelete?: boolean;
  onDelete?: () => void;
  /** The references naming the saved persona, which a delete lists. */
  onReferences?: (refs: PersonaReferences | undefined) => void;
  sourceNote?: string | null;
  isReadOnly?: boolean;
}

export function PersonaEditor({
  draft,
  onUpdate,
  onSave,
  onCancel,
  isCreate,
  dirty,
  selectedName,
  canDelete = false,
  onDelete,
  onReferences,
  sourceNote = null,
  isReadOnly = false,
}: PersonaEditorProps) {
  const editor = usePersonaEditor({ draft, onSave, isCreate, selectedName });
  const { report } = editor;
  const isAdminPersona = !!report && report.admin_persona !== "" && draft.name === report.admin_persona;

  useEffect(() => {
    onReferences?.(report?.references);
  }, [report?.references, onReferences]);

  return (
    <div className="flex h-full flex-col">
      <EditorHeader
        title={isCreate ? "New Persona" : draft.displayName || selectedName}
        isCreate={isCreate}
        isReadOnly={isReadOnly}
        dirty={dirty}
        canDelete={canDelete && !isAdminPersona}
        onDelete={onDelete}
        onCancel={onCancel}
        onSave={editor.handleSave}
        saveDisabled={isReadOnly || editor.isPending || (!dirty && !isCreate) || !draft.name || !draft.displayName}
        saving={editor.isPending}
        saveSuccess={editor.saveSuccess}
      />

      {sourceNote && (
        <Alert className="rounded-none border-x-0 border-t-0 bg-muted/30 px-6 py-2">
          <Info />
          <AlertDescription className="text-xs">{sourceNote}</AlertDescription>
        </Alert>
      )}

      {editor.saveError && (
        <Alert variant="destructive" className="rounded-none border-x-0 border-t-0 px-6 py-2">
          <AlertCircle />
          <AlertDescription className="text-xs">{editor.saveError}</AlertDescription>
        </Alert>
      )}

      <div className="flex min-h-0 flex-1 flex-col lg:grid lg:grid-cols-[200px_minmax(0,1fr)]">
        <aside className="hidden border-r lg:block lg:overflow-y-auto">
          <SectionIndex active={null} />
        </aside>
        <div className="min-w-0 lg:overflow-y-auto">
          <IdentitySection draft={draft} onUpdate={onUpdate} isCreate={isCreate} isReadOnly={isReadOnly} report={report} />
          <AccessSummary draft={draft} report={report} isLoading={editor.reportLoading} />
          <ToolsSection draft={draft} onUpdate={onUpdate} report={report} isReadOnly={isReadOnly} />
          <ConnectionsSection draft={draft} onUpdate={onUpdate} report={report} isReadOnly={isReadOnly} />
          <ApiEndpointsSection draft={draft} onUpdate={onUpdate} report={report} isReadOnly={isReadOnly} />
          <InstructionsSection draft={draft} onUpdate={onUpdate} isReadOnly={isReadOnly} />
          <PortalAccessSection draft={draft} onUpdate={onUpdate} report={report} isReadOnly={isReadOnly} />
        </div>
      </div>
    </div>
  );
}
