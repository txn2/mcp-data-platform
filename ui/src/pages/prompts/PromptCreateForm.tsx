import { useState } from "react";
import { Save, X } from "lucide-react";
import { useCreateMyPrompt } from "@/api/portal/hooks";
import type { Prompt } from "@/api/admin/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ArgumentsEditor } from "./ArgumentsEditor";
import { Field, FormError } from "./primitives";
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
    <SectionCard
      title="Create Prompt"
      action={
        <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
          <X />
        </Button>
      }
    >
      <div className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <PromptNameField
            value={form.name}
            onChange={(v) => { setForm({ ...form, name: v }); setNameConflict(null); }}
            serverError={nameConflict}
          />
          <Field id="create-display-name" label="Display Name">
            <Input
              id="create-display-name"
              value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })}
              placeholder="My Prompt"
            />
          </Field>
          <Field id="create-description" label="Description" className="sm:col-span-2">
            <Textarea
              id="create-description"
              value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
              rows={3}
              placeholder="What this prompt does"
              className="resize-y"
            />
          </Field>
        </div>

        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">Content (Markdown)</Label>
          <MarkdownEditor
            value={form.content}
            onChange={handleContentChange}
            minHeight="12rem"
            placeholder="Prompt content with {{arg}} placeholders..."
          />
          <p className="text-[11px] text-muted-foreground">
            Use <code className="font-mono">{"{{name}}"}</code> (preferred) or <code className="font-mono">{"{name}"}</code> to declare an argument. Rows auto-appear below as you type.
          </p>
        </div>

        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">Arguments</Label>
          <ArgumentsEditor args={form.arguments} updateArgField={updateArgField} />
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field id="create-category" label="Category">
            <Input
              id="create-category"
              value={form.category}
              onChange={(e) => setForm({ ...form, category: e.target.value })}
              placeholder="workflow"
            />
          </Field>
          <TagsField tags={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
        </div>

        <FormError message={mutationError} />

        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            onClick={handleCreate}
            disabled={!form.content || createMutation.isPending || validatePromptName(form.name) !== null}
          >
            <Save /> {createMutation.isPending ? "Saving..." : "Create"}
          </Button>
        </div>
      </div>
    </SectionCard>
  );
}
