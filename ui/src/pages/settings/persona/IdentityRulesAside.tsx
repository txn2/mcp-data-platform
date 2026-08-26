import { useId } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ChipInput } from "../ChipInput";
import { Section, Field } from "./primitives";
import { RuleList } from "./RuleList";
import { AddPatternButton } from "./AddPatternButton";
import { ApiRulesSection } from "./ApiRulesSection";
import type { APIRouteRule } from "@/api/admin/types";
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
  apiRouteConnectionNames,
  addRouteRule,
  removeRouteRule,
  highlightRoute,
  setHighlightRoute,
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
  apiRouteConnectionNames: string[];
  addRouteRule: (rule: APIRouteRule) => void;
  removeRouteRule: (index: number) => void;
  highlightRoute: number | null;
  setHighlightRoute: (index: number | null) => void;
}) {
  const ids = useId();
  return (
    <aside className="border-b lg:overflow-y-auto lg:border-b-0 lg:border-r">
      <fieldset disabled={isReadOnly} className="contents">
        <Section title="Identity">
          <Field label="Name" htmlFor={`${ids}-name`} required>
            <Input
              id={`${ids}-name`}
              type="text"
              value={draft.name}
              onChange={(e) => onUpdate({ name: e.target.value })}
              disabled={!isCreate}
              required
              placeholder="analyst"
              className="h-8 font-mono text-xs"
            />
          </Field>
          <Field label="Display Name" htmlFor={`${ids}-display`} required>
            <Input
              id={`${ids}-display`}
              type="text"
              value={draft.displayName}
              onChange={(e) => onUpdate({ displayName: e.target.value })}
              required
              placeholder="Data Analyst"
              className="h-8 text-xs"
            />
          </Field>
          <Field label="Description" htmlFor={`${ids}-description`}>
            {/* Content-sized rather than a fixed row count: a two-row box
                clipped the third line of every real description, and the rail
                scrolls anyway. */}
            <Textarea
              id={`${ids}-description`}
              value={draft.description}
              onChange={(e) => onUpdate({ description: e.target.value })}
              placeholder="What this persona is for…"
              className="min-h-14 resize-none px-2.5 py-1.5 text-xs"
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
              label="Add role"
            />
          </Field>
          <Field label="Priority" htmlFor={`${ids}-priority`}>
            <Input
              id={`${ids}-priority`}
              type="number"
              value={draft.priority}
              onChange={(e) =>
                onUpdate({ priority: parseInt(e.target.value, 10) || 0 })
              }
              className="h-8 w-24 text-xs"
            />
            <p className="mt-1 text-[10px] text-muted-foreground">
              Higher wins when a user matches multiple personas.
            </p>
          </Field>
        </Section>

        {/* The api scope's rules are objects naming a connection, methods and
            paths, so it gets its own editor rather than the two pattern lists
            the tool and connection axes share. */}
        {scope === "api" ? (
          <ApiRulesSection
            rules={draft.apiRoutes}
            connectionNames={apiRouteConnectionNames}
            onAdd={addRouteRule}
            onRemove={removeRouteRule}
            highlightIndex={highlightRoute}
            onHighlight={setHighlightRoute}
          />
        ) : (
          <>
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
            <Alert variant="warning" className="mt-2 px-2 py-1.5">
              <AlertDescription className="text-[10px]">
                No allow patterns means no tools are reachable (default deny).
              </AlertDescription>
            </Alert>
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
          </>
        )}
      </fieldset>
    </aside>
  );
}
