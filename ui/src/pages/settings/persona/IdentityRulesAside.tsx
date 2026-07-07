import { Info } from "lucide-react";
import { Section, Field, ChipInput } from "./primitives";
import { RuleList, AddPatternButton } from "./RuleList";
import type { PersonaDraft, Scope, Item } from "./types";

// IdentityRulesAside is the persona editor's left rail: identity fields (name,
// display name, description, roles, priority) plus the allow/deny pattern
// editors for the active scope. Purely presentational — all mutations route
// through the handlers passed down from PersonaEditor (#766).
export function IdentityRulesAside({
  isReadOnly,
  isCreate,
  draft,
  onUpdate,
  scope,
  allowList,
  denyList,
  items,
  highlightRule,
  setHighlightRule,
  rolesDraft,
  setRolesDraft,
  addRole,
  removeRole,
  addAllow,
  addDeny,
  removeAllow,
  removeDeny,
}: {
  isReadOnly: boolean;
  isCreate: boolean;
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  scope: Scope;
  allowList: string[];
  denyList: string[];
  items: Item[];
  highlightRule: { bucket: "allow" | "deny"; pattern: string } | null;
  setHighlightRule: (
    r: { bucket: "allow" | "deny"; pattern: string } | null,
  ) => void;
  rolesDraft: string;
  setRolesDraft: (s: string) => void;
  addRole: (role: string) => void;
  removeRole: (role: string) => void;
  addAllow: (pattern: string) => void;
  addDeny: (pattern: string) => void;
  removeAllow: (pattern: string) => void;
  removeDeny: (pattern: string) => void;
}) {
  return (
    <aside className="border-b lg:overflow-y-auto lg:border-b-0 lg:border-r">
      <fieldset disabled={isReadOnly} className="contents">
        <Section title="Identity">
          <Field label="Name" required>
            <input
              type="text"
              value={draft.name}
              onChange={(e) => onUpdate({ name: e.target.value })}
              disabled={!isCreate}
              required
              placeholder="analyst"
              className="w-full rounded-md border bg-background px-2.5 py-1.5 font-mono text-xs outline-none ring-ring focus:ring-2 disabled:cursor-not-allowed disabled:opacity-60"
            />
          </Field>
          <Field label="Display Name" required>
            <input
              type="text"
              value={draft.displayName}
              onChange={(e) => onUpdate({ displayName: e.target.value })}
              required
              placeholder="Data Analyst"
              className="w-full rounded-md border bg-background px-2.5 py-1.5 text-xs outline-none ring-ring focus:ring-2"
            />
          </Field>
          <Field label="Description">
            <textarea
              value={draft.description}
              onChange={(e) => onUpdate({ description: e.target.value })}
              rows={2}
              placeholder="What this persona is for…"
              className="w-full resize-none rounded-md border bg-background px-2.5 py-1.5 text-xs outline-none ring-ring focus:ring-2"
            />
          </Field>
          <Field label="Roles">
            <ChipInput
              values={draft.roles}
              onAdd={addRole}
              onRemove={removeRole}
              draft={rolesDraft}
              onDraftChange={setRolesDraft}
              placeholder="add role + Enter"
            />
          </Field>
          <Field label="Priority">
            <input
              type="number"
              value={draft.priority}
              onChange={(e) =>
                onUpdate({ priority: parseInt(e.target.value, 10) || 0 })
              }
              className="w-24 rounded-md border bg-background px-2.5 py-1.5 text-xs outline-none ring-ring focus:ring-2"
            />
            <p className="mt-1 text-[10px] text-muted-foreground">
              Higher wins when a user matches multiple personas.
            </p>
          </Field>
        </Section>

        <Section
          title="Allow Patterns"
          meta={
            <span className="font-mono text-[10px] text-muted-foreground">
              {allowList.length}
            </span>
          }
          description={
            scope === "tools"
              ? "Tools must match at least one allow pattern to be reachable."
              : "Connections must match at least one allow pattern. An empty list grants no connections (deny-by-default)."
          }
        >
          <RuleList
            bucket="allow"
            patterns={allowList}
            items={items}
            highlightRule={highlightRule}
            onHover={(p) =>
              setHighlightRule(p ? { bucket: "allow", pattern: p } : null)
            }
            onRemove={removeAllow}
          />
          <AddPatternButton
            bucket="allow"
            onAdd={addAllow}
            items={items}
            existing={allowList}
            scope={scope}
          />
          {allowList.length === 0 && scope === "tools" && (
            <p className="mt-2 flex items-start gap-1 text-[10px] text-amber-700 dark:text-amber-400">
              <Info className="mt-0.5 h-3 w-3 shrink-0" />
              <span>No allow patterns means no tools are reachable (default deny).</span>
            </p>
          )}
        </Section>

        <Section
          title="Deny Patterns"
          meta={
            <span className="font-mono text-[10px] text-muted-foreground">
              {denyList.length}
            </span>
          }
          description="Deny is absolute. A match blocks access even if an allow pattern also matches."
        >
          <RuleList
            bucket="deny"
            patterns={denyList}
            items={items}
            highlightRule={highlightRule}
            onHover={(p) =>
              setHighlightRule(p ? { bucket: "deny", pattern: p } : null)
            }
            onRemove={removeDeny}
          />
          <AddPatternButton
            bucket="deny"
            onAdd={addDeny}
            items={items}
            existing={denyList}
            scope={scope}
          />
        </Section>
      </fieldset>
    </aside>
  );
}
