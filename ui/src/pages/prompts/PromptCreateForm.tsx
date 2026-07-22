import { useState } from "react";
import { Save, X } from "lucide-react";
import { useCreateMyPrompt } from "@/api/portal/hooks";
import type { Prompt } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { extractPromptArguments } from "./promptArguments";
import { PromptNameField } from "./PromptNameField";
import { TagsField } from "./TagsField";
import { validatePromptName, isPromptNameConflict } from "./promptName";

// PromptCreateForm is the inline personal-prompt authoring card: name,
// display name, description, markdown content with auto-extracted {{arg}}
// placeholders, category, and tags. Owns its form and mutation state; the
// parent supplies navigation on success and dismissal.

interface FormData {
  name: string;
  display_name: string;
  description: string;
  content: string;
  category: string;
  tags: string[];
  arguments: Prompt["arguments"];
}

const emptyForm: FormData = {
  name: "",
  display_name: "",
  description: "",
  content: "",
  category: "",
  tags: [],
  arguments: [],
};

export function PromptCreateForm({
  onCreated,
  onClose,
}: {
  onCreated: (p: Prompt) => void;
  onClose: () => void;
}) {
  const createMutation = useCreateMyPrompt();
  const [form, setForm] = useState<FormData>(emptyForm);
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [nameConflict, setNameConflict] = useState<string | null>(null);

  function handleContentChange(next: string) {
    setForm((prev) => ({
      ...prev,
      content: next,
      arguments: extractPromptArguments(next, prev.arguments),
    }));
  }

  function updateArgField(name: string, patch: Partial<Prompt["arguments"][number]>) {
    setForm((prev) => ({
      ...prev,
      arguments: prev.arguments.map((a) => (a.name === name ? { ...a, ...patch } : a)),
    }));
  }

  function handleCreate() {
    setMutationError(null);
    setNameConflict(null);
    createMutation.mutate(form, {
      onSuccess: (p) => onCreated(p),
      onError: (err) => {
        const msg = err instanceof Error ? err.message : "Operation failed";
        if (isPromptNameConflict(msg)) {
          setNameConflict("That name is already taken.");
        } else {
          setMutationError(msg);
        }
      },
    });
  }

  return (
    <div className="rounded-lg border bg-card p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">Create Prompt</h3>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground"><X className="h-4 w-4" /></button>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <PromptNameField
          value={form.name}
          onChange={(v) => { setForm({ ...form, name: v }); setNameConflict(null); }}
          serverError={nameConflict}
        />
        <div>
          <label className="text-xs text-muted-foreground">Display Name</label>
          <input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="My Prompt" />
        </div>
        <div className="col-span-2">
          <label className="text-xs text-muted-foreground">Description</label>
          <textarea
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={3}
            placeholder="What this prompt does"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none resize-y"
          />
        </div>
        <div className="col-span-2">
          <label className="text-xs text-muted-foreground">Content (Markdown)</label>
          <MarkdownEditor
            value={form.content}
            onChange={handleContentChange}
            minHeight="12rem"
            placeholder="Prompt content with {{arg}} placeholders..."
          />
          <p className="text-[11px] text-muted-foreground mt-1">
            Use <code className="font-mono">{"{{name}}"}</code> (preferred) or <code className="font-mono">{"{name}"}</code> to declare an argument. Rows auto-appear below as you type.
          </p>
        </div>
        <div className="col-span-2">
          <label className="text-xs text-muted-foreground">Arguments</label>
          <ArgumentsEditor args={form.arguments} updateArgField={updateArgField} />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">Category</label>
          <input value={form.category} onChange={(e) => setForm({ ...form, category: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" placeholder="workflow" />
        </div>
        <TagsField tags={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
      </div>
      {mutationError && (
        <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">{mutationError}</div>
      )}
      <div className="flex justify-end gap-2 pt-2">
        <button onClick={onClose} className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted">Cancel</button>
        <button onClick={handleCreate} disabled={!form.content || createMutation.isPending || validatePromptName(form.name) !== null} className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
          <Save className="h-3.5 w-3.5" /> {createMutation.isPending ? "Saving..." : "Create"}
        </button>
      </div>
    </div>
  );
}

function ArgumentsEditor({
  args,
  updateArgField,
}: {
  args: Prompt["arguments"];
  updateArgField: (name: string, patch: Partial<Prompt["arguments"][number]>) => void;
}) {
  if (args.length === 0) {
    return (
      <div className="rounded-md border bg-muted/20 px-3 py-3 text-xs text-muted-foreground">
        No arguments yet. Add a <code className="font-mono">{"{{placeholder}}"}</code> in the content above.
      </div>
    );
  }
  return (
    <div className="rounded-md border bg-background overflow-hidden">
      <div className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 border-b bg-muted/40 text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
        <div>Name</div>
        <div>Description</div>
        <div className="text-right">Required</div>
      </div>
      <ul className="divide-y">
        {args.map((a) => (
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
  );
}
