import { useCallback, useId, useState } from "react";
import { KeyRound } from "lucide-react";
import { useCreateAPIKey } from "@/api/admin/hooks";
import type { APIKeyCreateResponse } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChipInput } from "../ChipInput";
import { ConfigSelect } from "../connections/fields";
import { RoleBrowser } from "./RoleBrowser";

// EXPIRATION_OPTIONS are the lifetimes offered for a new key. "Never" is an
// empty value, which the shared ConfigSelect carries under its own sentinel.
const EXPIRATION_OPTIONS = [
  { label: "Never", value: "" },
  { label: "24 hours", value: "24h" },
  { label: "7 days", value: "168h" },
  { label: "30 days", value: "720h" },
  { label: "90 days", value: "2160h" },
  { label: "1 year", value: "8760h" },
];

interface KeyDraft {
  name: string;
  email: string;
  description: string;
  roles: string[];
  expirationPreset: string;
}

function emptyDraft(): KeyDraft {
  return { name: "", email: "", description: "", roles: [], expirationPreset: "" };
}

// AddKeyForm creates an API key for programmatic access. Extracted from
// KeysPage.tsx (#1206).
export function AddKeyForm({
  onCreated,
}: {
  onCreated: (resp: APIKeyCreateResponse) => void;
}) {
  const createMutation = useCreateAPIKey();
  const ids = useId();
  const [draft, setDraft] = useState<KeyDraft>(emptyDraft());
  const [roleInput, setRoleInput] = useState("");
  const [error, setError] = useState<string | null>(null);

  const updateDraft = useCallback((partial: Partial<KeyDraft>) => {
    setDraft((prev) => ({ ...prev, ...partial }));
    setError(null);
  }, []);

  const addRole = useCallback(
    (role: string) => {
      const trimmed = role.trim();
      setRoleInput("");
      if (!trimmed) return;
      setDraft((prev) =>
        prev.roles.includes(trimmed)
          ? prev
          : { ...prev, roles: [...prev.roles, trimmed] },
      );
      setError(null);
    },
    [],
  );

  const removeRole = useCallback((role: string) => {
    setDraft((prev) => ({ ...prev, roles: prev.roles.filter((r) => r !== role) }));
  }, []);

  const handleSubmit = useCallback(() => {
    if (!draft.name.trim()) {
      setError("Name is required");
      return;
    }
    createMutation.mutate(
      {
        name: draft.name.trim(),
        email: draft.email.trim() || undefined,
        description: draft.description.trim() || undefined,
        roles: draft.roles,
        expires_in: draft.expirationPreset || undefined,
      },
      {
        onSuccess: (resp) => onCreated(resp),
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Failed to create key");
        },
      },
    );
  }, [draft, createMutation, onCreated]);

  return (
    <div className="space-y-4 border-b bg-muted/10 px-5 py-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="grid grid-cols-3 gap-3">
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-name`} className="gap-0.5 text-xs">
            Name
            <span className="text-destructive">*</span>
          </Label>
          <Input
            id={`${ids}-name`}
            type="text"
            value={draft.name}
            onChange={(e) => updateDraft({ name: e.target.value })}
            placeholder="e.g. ci-pipeline"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-email`} className="text-xs">
            Email
          </Label>
          <Input
            id={`${ids}-email`}
            type="email"
            value={draft.email}
            onChange={(e) => updateDraft({ email: e.target.value })}
            placeholder="e.g. team@example.com"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-description`} className="text-xs">
            Description
          </Label>
          <Input
            id={`${ids}-description`}
            type="text"
            value={draft.description}
            onChange={(e) => updateDraft({ description: e.target.value })}
            placeholder="What is this key used for?"
          />
        </div>
      </div>

      <div className="space-y-2">
        <div className="flex items-end gap-3">
          <div className="flex-1 space-y-1.5">
            <Label className="text-xs">Roles</Label>
            <ChipInput
              values={draft.roles}
              onAdd={addRole}
              onRemove={removeRole}
              draft={roleInput}
              onDraftChange={setRoleInput}
              placeholder="Type role + Enter"
              label="Add role"
            />
          </div>
          <div className="w-36">
            <ConfigSelect
              label="Expiration"
              value={draft.expirationPreset}
              onChange={(v) => updateDraft({ expirationPreset: v })}
              options={EXPIRATION_OPTIONS}
            />
          </div>
          <Button
            type="button"
            size="sm"
            onClick={handleSubmit}
            disabled={createMutation.isPending || !draft.name.trim()}
          >
            <KeyRound />
            {createMutation.isPending ? "Creating..." : "Create"}
          </Button>
        </div>
        <RoleBrowser onSelect={addRole} />
      </div>
    </div>
  );
}
