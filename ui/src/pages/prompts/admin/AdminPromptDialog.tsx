import { useState } from "react";
import { Save, ToggleLeft, ToggleRight, X } from "lucide-react";
import { useCreateAdminPrompt, useUpdateAdminPrompt } from "@/api/admin/hooks";
import type { Prompt } from "@/api/admin/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { ModalShell } from "@/components/ModalShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Field, FormError } from "../primitives";
import { PromptNameField } from "../PromptNameField";
import { TagsField } from "../TagsField";
import { isPromptNameConflict, validatePromptName } from "../promptName";
import {
  emptyForm,
  formFromPrompt,
  parsePersonas,
  statusOptionsFor,
  type FormData,
  type PromptStatus,
} from "./adminPromptForm";

// AdminPromptDialog authors a prompt at any scope. It owns the form and the
// create/update mutations, so the page around it only decides whether the
// editor is open and on what. A modal rather than an inline card because the
// admin table it edits is long enough to scroll the form off-screen.
export function AdminPromptDialog({
  prompt,
  onClose,
}: {
  // The prompt being edited; absent means create.
  prompt?: Prompt;
  onClose: () => void;
}) {
  const isEdit = prompt !== undefined;
  const [form, setForm] = useState<FormData>(() => (prompt ? formFromPrompt(prompt) : emptyForm));
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [nameConflict, setNameConflict] = useState<string | null>(null);
  const createMutation = useCreateAdminPrompt();
  const updateMutation = useUpdateAdminPrompt();
  const pending = createMutation.isPending || updateMutation.isPending;

  // The lifecycle status the server currently holds (form.status may already
  // reflect an unsaved selection). Valid transitions are computed from this,
  // never from the in-progress form value.
  const originalStatus: PromptStatus = prompt?.status ?? form.status;

  function handleSubmit() {
    setMutationError(null);
    setNameConflict(null);
    const personas = parsePersonas(form.personas);
    const onError = (err: unknown) => {
      const msg = err instanceof Error ? err.message : "Operation failed";
      if (isPromptNameConflict(msg)) {
        setNameConflict("That name is already taken.");
      } else {
        setMutationError(msg);
      }
    };
    const common = {
      name: form.name,
      display_name: form.display_name,
      description: form.description,
      content: form.content,
      category: form.category,
      scope: form.scope,
      personas,
      tags: form.tags,
      owner_email: form.owner_email,
      enabled: form.enabled,
    };

    if (prompt) {
      updateMutation.mutate(
        {
          id: prompt.id,
          ...common,
          status: form.status,
          superseded_by: form.status === "superseded" ? form.superseded_by : undefined,
        },
        { onSuccess: onClose, onError },
      );
    } else {
      createMutation.mutate(common, { onSuccess: onClose, onError });
    }
  }

  return (
    <ModalShell
      onClose={onClose}
      width="max-w-2xl"
      label={isEdit ? "Edit Prompt" : "Create Prompt"}
      bodyClass="px-4 py-3"
      header={
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="text-sm font-semibold">{isEdit ? "Edit Prompt" : "Create Prompt"}</h3>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>
      }
      footer={
        <div className="flex justify-end gap-2 border-t px-4 py-3">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            onClick={handleSubmit}
            disabled={!form.content || pending || validatePromptName(form.name) !== null}
          >
            <Save /> {pending ? "Saving..." : isEdit ? "Save" : "Create"}
          </Button>
        </div>
      }
    >
      <div className="space-y-3">
        <AdminPromptFields
          form={form}
          setForm={setForm}
          isEdit={isEdit}
          originalStatus={originalStatus}
          nameConflict={nameConflict}
          onNameConflictClear={() => setNameConflict(null)}
        />
        <FormError message={mutationError} />
      </div>
    </ModalShell>
  );
}

function AdminPromptFields({
  form,
  setForm,
  isEdit,
  originalStatus,
  nameConflict,
  onNameConflictClear,
}: {
  form: FormData;
  setForm: (next: FormData) => void;
  isEdit: boolean;
  originalStatus: PromptStatus;
  nameConflict: string | null;
  onNameConflictClear: () => void;
}) {
  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <PromptNameField
          value={form.name}
          onChange={(v) => { setForm({ ...form, name: v }); onNameConflictClear(); }}
          serverError={nameConflict}
        />
        <Field id="admin-display-name" label="Display Name">
          <Input
            id="admin-display-name"
            value={form.display_name}
            onChange={(e) => setForm({ ...form, display_name: e.target.value })}
            placeholder="My Prompt"
          />
        </Field>
      </div>

      <Field id="admin-description" label="Description">
        <Input
          id="admin-description"
          value={form.description}
          onChange={(e) => setForm({ ...form, description: e.target.value })}
          placeholder="What this prompt does"
        />
      </Field>

      {/* The editor sizes itself to its parent, so it stays out of the grid:
          as a stretched grid item it would render taller than its own cell and
          spill over the fields below. */}
      <div className="space-y-1.5">
        <Label className="text-xs text-muted-foreground">Content</Label>
        <MarkdownEditor
          value={form.content}
          onChange={(v) => setForm({ ...form, content: v })}
          minHeight="10rem"
          placeholder="Prompt content with {arg} placeholders..."
        />
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field id="admin-scope" label="Scope">
          <Select
            value={form.scope}
            onValueChange={(v) => setForm({ ...form, scope: v as Prompt["scope"] })}
          >
            <SelectTrigger id="admin-scope" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="global">Global</SelectItem>
              <SelectItem value="persona">Persona</SelectItem>
              <SelectItem value="personal">Personal</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field id="admin-category" label="Category">
          <Input
            id="admin-category"
            value={form.category}
            onChange={(e) => setForm({ ...form, category: e.target.value })}
            placeholder="workflow"
          />
        </Field>
        <TagsField tags={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
        {isEdit && (
          <Field id="admin-status" label="Lifecycle Status">
            <Select
              value={form.status}
              onValueChange={(v) => setForm({ ...form, status: v as PromptStatus })}
            >
              <SelectTrigger id="admin-status" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {statusOptionsFor(originalStatus).map((s) => (
                  <SelectItem key={s} value={s} className="capitalize">{s}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        {isEdit && form.status === "superseded" && (
          <Field id="admin-superseded-by" label="Superseded By (prompt name)">
            <Input
              id="admin-superseded-by"
              value={form.superseded_by}
              onChange={(e) => setForm({ ...form, superseded_by: e.target.value })}
              placeholder="report-v2"
            />
          </Field>
        )}
        {form.scope === "persona" && (
          <Field id="admin-personas" label="Personas (comma-separated)" className="sm:col-span-2">
            <Input
              id="admin-personas"
              value={form.personas}
              onChange={(e) => setForm({ ...form, personas: e.target.value })}
              placeholder="analyst, data-engineer"
            />
          </Field>
        )}
        <Field id="admin-owner-email" label="Owner Email">
          <Input
            id="admin-owner-email"
            value={form.owner_email}
            onChange={(e) => setForm({ ...form, owner_email: e.target.value })}
            placeholder="user@example.com"
          />
        </Field>
        <div className="flex items-end">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setForm({ ...form, enabled: !form.enabled })}
            aria-pressed={form.enabled}
            className="px-2 text-xs font-normal text-muted-foreground"
          >
            {form.enabled ? <ToggleRight className="text-emerald-500" /> : <ToggleLeft />}
            {form.enabled ? "Enabled" : "Disabled"}
          </Button>
        </div>
      </div>
    </div>
  );
}
