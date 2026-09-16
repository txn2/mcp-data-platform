import { useCallback, useId, useState } from "react";
import { KeyRound } from "lucide-react";
import { useCreateAPIKey } from "@/api/admin/hooks";
import type { APIKeyCreateResponse, DirectoryUser } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChipInput } from "../ChipInput";
import { ConfigSelect } from "../connections/fields";
import { BindToUser } from "./BindToUser";
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
  /** The account the key is issued against, or "" for a service key (#1759). */
  userEmail: string;
  roles: string[];
  expirationPreset: string;
  /**
   * True while the roles shown are the ones the picked person holds and the
   * operator has not touched them. Editing them turns it off, which freezes
   * the set onto the key. It is not the whole answer on its own: see
   * followsUser.
   */
  rolesPrefilled: boolean;
}

function emptyDraft(): KeyDraft {
  return {
    name: "",
    email: "",
    description: "",
    userEmail: "",
    roles: [],
    expirationPreset: "",
    rolesPrefilled: false,
  };
}

// followsUser reports whether the key being made will carry whatever roles its
// person holds, rather than a set of its own.
//
// An emptied role list is the same thing as an untouched one: a key with no
// roles of its own follows its person, and "narrowed to nothing" is not a
// thing a key can be -- it would reach no persona and list no tools. Deriving
// it here rather than reading the prefilled flag alone is what keeps the form
// saying what the server will do.
function followsUser(draft: KeyDraft): boolean {
  if (!draft.userEmail) return false;
  return draft.rolesPrefilled || draft.roles.length === 0;
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
          : { ...prev, roles: [...prev.roles, trimmed], rolesPrefilled: false },
      );
      setError(null);
    },
    [],
  );

  const removeRole = useCallback((role: string) => {
    setDraft((prev) => ({
      ...prev,
      roles: prev.roles.filter((r) => r !== role),
      rolesPrefilled: false,
    }));
  }, []);

  // Picking a person fills the roles with the ones they hold, so the form shows
  // what the key will reach before it is made. Clearing the pick empties them
  // again rather than leaving somebody else's roles on a service key.
  const pickUser = useCallback((user: DirectoryUser | null) => {
    setDraft((prev) => ({
      ...prev,
      roles: user?.roles ?? [],
      rolesPrefilled: user !== null,
    }));
    setError(null);
  }, []);

  const handleSubmit = useCallback(() => {
    if (!draft.name.trim()) {
      setError("Name is required");
      return;
    }
    if (!draft.userEmail && draft.roles.length === 0) {
      setError("A service key needs at least one role, or bind it to a user.");
      return;
    }
    createMutation.mutate(
      {
        name: draft.name.trim(),
        email: draft.userEmail ? undefined : draft.email.trim() || undefined,
        description: draft.description.trim() || undefined,
        user_email: draft.userEmail || undefined,
        // A key that follows its person sends no roles at all, so the server
        // reads them on every request rather than freezing today's set.
        roles: followsUser(draft) ? undefined : draft.roles,
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
          <Label className="text-xs">Issued against</Label>
          <BindToUser
            value={draft.userEmail}
            onChange={(email) => updateDraft({ userEmail: email })}
            onPick={pickUser}
            disabled={createMutation.isPending}
          />
          {!draft.userEmail && (
            <Input
              type="email"
              value={draft.email}
              onChange={(e) => updateDraft({ email: e.target.value })}
              placeholder="Contact email (optional)"
              aria-label="Contact email for this service key"
            />
          )}
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
            <Label className="text-xs">
              Roles
              {draft.userEmail && (
                <span className="ml-2 font-normal text-muted-foreground">
                  {followsUser(draft)
                    ? "following this person - edit to narrow the key"
                    : "narrowed to this set"}
                </span>
              )}
            </Label>
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
        {followsUser(draft) && (
          <p className="text-xs text-muted-foreground">
            This key authenticates as {draft.userEmail} and carries whatever
            roles they hold, read on every request. Edit the roles above to give
            the key a narrower set instead.
          </p>
        )}
        <RoleBrowser onSelect={addRole} />
      </div>
    </div>
  );
}
