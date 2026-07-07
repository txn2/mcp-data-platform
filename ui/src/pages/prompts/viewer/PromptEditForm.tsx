import type { Dispatch, SetStateAction } from "react";
import type { Prompt } from "@/api/admin/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { cn } from "@/lib/utils";
import { PromptNameField } from "../PromptNameField";
import { TagsField } from "../TagsField";
import type { EditForm } from "./types";

// PromptEditForm is the editable form shown when the owner edits a prompt:
// name/display-name/description/category/tags fields, the markdown content
// editor, and the auto-synced arguments editor. Extracted verbatim from
// PromptViewerPage.tsx (#819); all state lives in the parent.
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
    <div className="rounded-lg border bg-card p-4 space-y-3">
      <div className="grid grid-cols-2 gap-3">
        <PromptNameField
          value={form.name}
          onChange={(v) => { setForm({ ...form, name: v }); setNameConflict(null); }}
          serverError={nameConflict}
        />
        <div>
          <label className="text-xs text-muted-foreground">Display Name</label>
          <input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" />
        </div>
        <div className="col-span-2">
          <label className="text-xs text-muted-foreground">Description</label>
          <textarea
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={3}
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none resize-y"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">Category</label>
          <input value={form.category} onChange={(e) => setForm({ ...form, category: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" />
        </div>
        <TagsField key={prompt.id} tags={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
      </div>
      <div>
        <label className="text-xs text-muted-foreground">Content (Markdown)</label>
        <MarkdownEditor
          value={form.content}
          onChange={onContentChange}
          minHeight="20rem"
          placeholder="Prompt content with {{arg}} placeholders..."
        />
        <p className="text-[11px] text-muted-foreground mt-1">
          Use <code className="font-mono">{"{{name}}"}</code> (preferred) or <code className="font-mono">{"{name}"}</code> to declare an argument. Rows auto-appear below as you type.
        </p>
      </div>

      {/* Arguments editor — auto-synced from content above */}
      <div>
        <label className="text-xs text-muted-foreground">Arguments</label>
        {form.arguments.length === 0 ? (
          <div className="rounded-md border bg-muted/20 px-3 py-3 text-xs text-muted-foreground">
            No arguments yet. Add a <code className="font-mono">{"{{placeholder}}"}</code> in the content above.
          </div>
        ) : (
          <div className="rounded-md border bg-card overflow-hidden">
            <div className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 border-b bg-muted/40 text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
              <div>Name</div>
              <div>Description</div>
              <div className="text-right">Required</div>
            </div>
            <ul className="divide-y">
              {form.arguments.map((a) => (
                <li key={a.name} className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 items-start">
                  <code className="text-xs font-mono text-foreground bg-muted/60 rounded px-1.5 py-0.5 break-all mt-1">
                    {`{{${a.name}}}`}
                  </code>
                  <textarea
                    value={a.description}
                    onChange={(e) => updateArgField(a.name, { description: e.target.value })}
                    placeholder="What this argument is for"
                    rows={2}
                    className="w-full rounded-md border bg-background px-2 py-1 text-xs outline-none ring-ring focus:ring-2 resize-y"
                  />
                  <label className="inline-flex items-center justify-end gap-2 text-xs cursor-pointer select-none mt-1.5">
                    <input
                      type="checkbox"
                      checked={a.required}
                      onChange={(e) => updateArgField(a.name, { required: e.target.checked })}
                      className="h-3.5 w-3.5"
                    />
                    <span className={cn("font-medium", a.required ? "text-rose-400" : "text-muted-foreground")}>
                      {a.required ? "Required" : "Optional"}
                    </span>
                  </label>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
