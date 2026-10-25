import { useId, useState } from "react";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { ChipInput } from "../ChipInput";
import type { EffectiveAccess, AccountType, RoleOverlap } from "@/api/admin/types";
import { Field } from "./primitives";
import { EditorSection } from "./sections";
import type { PersonaDraft } from "./types";

const ACCOUNT_TYPES: { value: AccountType; label: string; help: string }[] = [
  { value: "internal", label: "Internal people", help: "People of the organization, signed in through its identity provider." },
  {
    value: "external",
    label: "External people",
    help: "People outside the organization, signed in through your identity provider. They see only what is shared with them.",
  },
  {
    value: "automated",
    label: "Automated caller",
    help: "An integration holding an API key. Its calls are audited but not added to Calls.",
  },
];

export function accountTypeOf(draft: PersonaDraft): AccountType {
  if (draft.external) return "external";
  if (draft.serviceAccount) return "automated";
  return "internal";
}

function accountTypePatch(t: AccountType): Partial<PersonaDraft> {
  return { external: t === "external", serviceAccount: t === "automated" };
}

/** winner is the persona that resolves for a person holding the shared role. */
function winner(draft: PersonaDraft, o: RoleOverlap): string {
  if (o.priority !== draft.priority) return o.priority > draft.priority ? o.persona : draft.name;
  return [o.persona, draft.name].sort()[0] ?? draft.name;
}

/** roleWarnings are the server's facts about who else a role reaches. */
export function roleWarnings(draft: PersonaDraft, report: EffectiveAccess | undefined): string[] {
  if (!report) return [];
  const out = report.assignment.role_overlaps.map(
    (o) =>
      `${o.persona} (priority ${o.priority}) also lists ${o.role}; ${winner(draft, o)} wins for a person holding it. ` +
      `A person holding ${o.role} reads this persona's folder and prompts either way.`,
  );
  return out;
}

// IdentitySection is who gets the persona (#2257): its name, roles, the
// roles role_mapping assigns it, its priority and its account type. It grants
// nothing.
export function IdentitySection({
  draft,
  onUpdate,
  isCreate,
  isReadOnly,
  report,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  isCreate: boolean;
  isReadOnly: boolean;
  report: EffectiveAccess | undefined;
}) {
  const ids = useId();
  const [roleDraft, setRoleDraft] = useState("");
  const isAdminPersona = !!report && report.admin_persona !== "" && draft.name === report.admin_persona;
  const accountType = accountTypeOf(draft);
  const warnings = roleWarnings(draft, report);
  return (
    <EditorSection id="identity" title="Identity" governs="Who gets the persona. Nothing here grants access.">
      {isAdminPersona && (
        <Badge variant="secondary" className="mb-3">
          Administrator persona
        </Badge>
      )}
      <fieldset disabled={isReadOnly} className="grid max-w-3xl gap-x-6 sm:grid-cols-2">
        <Field label="Name" htmlFor={`${ids}-name`} required>
          <Input
            id={`${ids}-name`}
            value={draft.name}
            onChange={(e) => onUpdate({ name: e.target.value })}
            disabled={!isCreate}
            required
            placeholder="analyst"
            className="h-8 font-mono text-xs"
          />
        </Field>
        <Field label="Display name" htmlFor={`${ids}-display`} required>
          <Input
            id={`${ids}-display`}
            value={draft.displayName}
            onChange={(e) => onUpdate({ displayName: e.target.value })}
            required
            placeholder="Data Analyst"
            className="h-8 text-xs"
          />
        </Field>
        <div className="sm:col-span-2">
          <Field label="Description" htmlFor={`${ids}-description`}>
            <Textarea
              id={`${ids}-description`}
              value={draft.description}
              onChange={(e) => onUpdate({ description: e.target.value })}
              placeholder="What this persona is for"
              className="min-h-14 resize-none px-2.5 py-1.5 text-xs"
            />
          </Field>
        </div>
        <Field label="Roles">
          <ChipInput
            values={draft.roles}
            onAdd={(role) => {
              const v = role.trim();
              setRoleDraft("");
              if (v && !draft.roles.includes(v)) onUpdate({ roles: [...draft.roles, v] });
            }}
            onRemove={(role) => onUpdate({ roles: draft.roles.filter((r) => r !== role) })}
            draft={roleDraft}
            onDraftChange={setRoleDraft}
            placeholder="add role + Enter"
            label="Add role"
          />
          {report && report.assignment.role_mapping.length > 0 && (
            <p className="mt-1 text-[11px] text-muted-foreground">
              Assigned by role mapping: <span className="font-mono">{report.assignment.role_mapping.join(", ")}</span>
            </p>
          )}
        </Field>
        <Field label="Priority" htmlFor={`${ids}-priority`}>
          <Input
            id={`${ids}-priority`}
            type="number"
            value={draft.priority}
            onChange={(e) => onUpdate({ priority: parseInt(e.target.value, 10) || 0 })}
            className="h-8 w-24 text-xs"
          />
          <p className="mt-1 text-[10px] text-muted-foreground">
            Higher wins when a person's roles match more than one persona. Role mapping decides first.
          </p>
        </Field>
        <div className="sm:col-span-2">
          <fieldset className="mt-2">
            <legend className="mb-1.5 text-xs font-medium">Account type</legend>
            <div className="space-y-2">
              {ACCOUNT_TYPES.map((t) => {
                const disabled = t.value === "external" && isAdminPersona;
                return (
                  <label key={t.value} className="flex items-start gap-2 text-sm">
                    <input
                      type="radio"
                      name={`${ids}-account`}
                      className="mt-1"
                      checked={accountType === t.value}
                      disabled={disabled}
                      onChange={() => onUpdate(accountTypePatch(t.value))}
                    />
                    <span>
                      <span className="font-medium">{t.label}</span>
                      <span className="block text-xs text-muted-foreground">
                        {disabled ? "The administrator persona cannot be external." : t.help}
                      </span>
                      {t.value === "automated" && report?.calls_excluded_by_config && (
                        <span className="block text-xs text-muted-foreground">Also excluded by configuration.</span>
                      )}
                    </span>
                  </label>
                );
              })}
            </div>
          </fieldset>
        </div>
      </fieldset>
      {warnings.length > 0 && (
        <ul className="mt-4 max-w-3xl space-y-1 rounded border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
    </EditorSection>
  );
}
