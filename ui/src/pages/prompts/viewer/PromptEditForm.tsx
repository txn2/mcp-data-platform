import type { Dispatch, SetStateAction } from "react";
import type { Prompt } from "@/api/admin/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ArgumentsEditor } from "../ArgumentsEditor";
import { Field } from "../primitives";
import { PromptNameField } from "../PromptNameField";
import { TagsField } from "../TagsField";
import type { EditForm } from "./types";

// PromptEditForm is the editable form shown when the owner edits a prompt:
// name/display-name/description/category/tags fields, the markdown content
// editor, and the auto-synced arguments editor. All state lives in the parent.
export function PromptEditForm({
  prompt,
  form,
  setForm,
  nameConflict,
  setNameConflict,
  onContentChange,
  updateArgField,
}: {
  prompt: Prompt;
  form: EditForm;
  setForm: Dispatch<SetStateAction<EditForm>>;
  nameConflict: string | null;
  setNameConflict: (v: string | null) => void;
  onContentChange: (next: string) => void;
  updateArgField: (name: string, patch: Partial<Prompt["arguments"][number]>) => void;
}) {
  return (
    <Card className="py-4">
      <CardContent className="space-y-3 px-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <PromptNameField
            value={form.name}
            onChange={(v) => { setForm({ ...form, name: v }); setNameConflict(null); }}
            serverError={nameConflict}
          />
          <Field id="edit-display-name" label="Display Name">
            <Input
              id="edit-display-name"
              value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })}
            />
          </Field>
          <Field id="edit-description" label="Description" className="sm:col-span-2">
            <Textarea
              id="edit-description"
              value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
              rows={3}
              className="resize-y"
            />
          </Field>
          <Field id="edit-category" label="Category">
            <Input
              id="edit-category"
              value={form.category}
              onChange={(e) => setForm({ ...form, category: e.target.value })}
            />
          </Field>
          <TagsField key={prompt.id} tags={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
        </div>

        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">Content (Markdown)</Label>
          <MarkdownEditor
            value={form.content}
            onChange={onContentChange}
            minHeight="20rem"
            placeholder="Prompt content with {{arg}} placeholders..."
          />
          <p className="text-[11px] text-muted-foreground">
            Use <code className="font-mono">{"{{name}}"}</code> (preferred) or <code className="font-mono">{"{name}"}</code> to declare an argument. Rows auto-appear below as you type.
          </p>
        </div>

        {/* Arguments editor — auto-synced from content above */}
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">Arguments</Label>
          <ArgumentsEditor args={form.arguments} updateArgField={updateArgField} />
        </div>
      </CardContent>
    </Card>
  );
}
