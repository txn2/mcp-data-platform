import { Check, Save, AlertCircle, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// EditorHeader is the persona editor's command bar: what is being edited,
// whether it can be edited, whether it has unsaved work, and the three actions
// that resolve that state. Extracted from PersonaEditor.tsx (#1206) so the
// editor file owns state and layout rather than button markup.
export function EditorHeader({
  title,
  isCreate,
  isReadOnly,
  dirty,
  canDelete,
  onDelete,
  onCancel,
  onSave,
  saveDisabled,
  saving,
  saveSuccess,
}: {
  title: React.ReactNode;
  isCreate: boolean;
  isReadOnly: boolean;
  dirty: boolean;
  canDelete: boolean;
  onDelete?: () => void;
  onCancel: () => void;
  onSave: () => void;
  saveDisabled: boolean;
  saving: boolean;
  saveSuccess: boolean;
}) {
  return (
    <div className="flex items-center justify-between border-b bg-muted/10 px-6 py-3">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-semibold">{title}</h2>
        {isReadOnly && (
          <Badge variant="muted" className="rounded text-[10px] uppercase tracking-wider">
            Read only
          </Badge>
        )}
        {dirty && (
          <Badge variant="warning" className="rounded">
            <AlertCircle />
            Unsaved
          </Badge>
        )}
      </div>
      <div className="flex items-center gap-2">
        {canDelete && onDelete && (
          <Button
            type="button"
            variant="outline"
            size="icon-sm"
            onClick={onDelete}
            aria-label="Delete persona"
            className="text-muted-foreground hover:border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 />
          </Button>
        )}
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          {isCreate ? "Cancel" : "Revert"}
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={onSave}
          disabled={saveDisabled}
          className={cn(saveSuccess && "bg-emerald-600 text-white hover:bg-emerald-600")}
        >
          {saveSuccess ? (
            <>
              <Check />
              Saved
            </>
          ) : saving ? (
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
  );
}
