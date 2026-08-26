import type { APIRouteRule, APIRouteOperation } from "@/api/admin/types";
import { matchPattern } from "./resolve";

// Pure evaluation of a persona's API route rules, mirroring
// pkg/persona/filter.go WhyAPIRouteAllowed so the editor's preview and the
// server's enforcement cannot disagree.
//
// The decision has three outcomes, not two. "allow" and "deny" come from a
// rule; "open" is what a connection no rule names gets, and it is an allow the
// connection-level grant is the sole gate for. Rendering it as a plain allow
// would tell an operator a rule is in force where none is.

export type RouteDecision = "allow" | "deny" | "open";

export interface RouteResolution {
  decision: RouteDecision;
  /** The rule that decided. */
  rule?: APIRouteRule;
  /** Its position in the persona's list, so the rail can highlight it. */
  index?: number;
}

/** matchesAny mirrors the backend's "empty means any" for a glob list. */
function matchesAny(patterns: string[] | undefined, value: string): boolean {
  if (!patterns || patterns.length === 0) return true;
  return patterns.some((p) => matchPattern(p, value));
}

/** ruleMatches reports whether a rule covers one operation. */
function ruleMatches(rule: APIRouteRule, method: string, path: string): boolean {
  return matchesAny(rule.methods, method) && matchesAny(rule.paths, path);
}

/**
 * ruleGoverns reports whether a rule covers one operation at all, regardless of
 * which rule ends up deciding it. The rail uses it to mark every row a rule the
 * pointer is on reaches, the way hovering a pattern chip marks the items that
 * pattern matches.
 */
export function ruleGoverns(
  rule: APIRouteRule,
  connection: string,
  method: string,
  path: string,
): boolean {
  if (!rule.connection || !matchPattern(rule.connection, connection)) return false;
  return ruleMatches(rule, method, path);
}

/** rulesFor narrows a persona's rules to the ones naming this connection. */
export function rulesFor(
  rules: APIRouteRule[],
  connection: string,
): { rule: APIRouteRule; index: number }[] {
  return rules
    .map((rule, index) => ({ rule, index }))
    .filter(
      ({ rule }) => !!rule.connection && matchPattern(rule.connection, connection),
    );
}

/**
 * resolveRoute answers what a persona's rules decide for one operation.
 *
 * Order matches the backend: rules that do not name the connection are skipped;
 * a matching deny wins; otherwise a matching allow is required; a connection no
 * rule names at all is open.
 *
 * path is the operation's declared path. That is the form a rule names and the
 * form a listing surface matches on, and the backend also matches it against
 * the path a call reaches, so a rule that governs the operation here governs
 * the calls it serves.
 */
export function resolveRoute(
  rules: APIRouteRule[],
  connection: string,
  method: string,
  path: string,
): RouteResolution {
  const relevant = rulesFor(rules, connection);
  if (relevant.length === 0) return { decision: "open" };

  const denied = relevant.find(
    ({ rule }) => rule.action === "deny" && ruleMatches(rule, method, path),
  );
  if (denied) return { decision: "deny", rule: denied.rule, index: denied.index };

  const allowed = relevant.find(
    ({ rule }) => rule.action !== "deny" && ruleMatches(rule, method, path),
  );
  if (allowed) return { decision: "allow", rule: allowed.rule, index: allowed.index };

  // Rules name the connection but none matched. That is a denial no single
  // rule produced, and the operator has to be told it is a consequence of the
  // set rather than of a rule they can point at.
  return { decision: "deny" };
}

/**
 * ruleForOperation is the rule a selection compiles to: this operation's own
 * method and the path exactly as its catalog declares it.
 *
 * Naming the declared path is what makes the selection precise. A glob such as
 * "/v1/orders/*" would be a different rule and would also cover the sibling
 * operations at that depth.
 */
export function ruleForOperation(
  connection: string,
  op: Pick<APIRouteOperation, "method" | "path">,
  action: "allow" | "deny",
): APIRouteRule {
  return {
    connection,
    methods: [op.method.toUpperCase()],
    paths: [op.path],
    action,
  };
}

/** sameRule reports whether two rules express the same thing. */
export function sameRule(a: APIRouteRule, b: APIRouteRule): boolean {
  const list = (v?: string[]) => (v ?? []).join(" ");
  return (
    a.connection === b.connection &&
    list(a.methods) === list(b.methods) &&
    list(a.paths) === list(b.paths) &&
    (a.action ?? "allow") === (b.action ?? "allow")
  );
}

/**
 * withOperationRule adds the rule a selection compiles to, replacing any rule
 * already naming that exact operation on that connection so a second click
 * flips the decision rather than stacking a contradictory rule beneath it.
 *
 * Only the exact single-operation rule is replaced. A broader rule the operator
 * wrote by hand is left alone: it is theirs, and the two are resolved against
 * each other by the same order the backend applies.
 */
export function withOperationRule(
  rules: APIRouteRule[],
  connection: string,
  op: Pick<APIRouteOperation, "method" | "path">,
  action: "allow" | "deny",
): APIRouteRule[] {
  const next = ruleForOperation(connection, op, action);
  const kept = rules.filter(
    (r) =>
      !sameRule(r, { ...next, action: "allow" }) &&
      !sameRule(r, { ...next, action: "deny" }),
  );
  return [...kept, next];
}

/** withoutRuleAt removes one rule by its position in the persona's list. */
export function withoutRuleAt(
  rules: APIRouteRule[],
  index: number,
): APIRouteRule[] {
  return rules.filter((_, i) => i !== index);
}

/**
 * operationKey identifies one operation row across connections, and parseKey
 * reads it back. They are a pair on purpose: the list writes the key and the
 * rail parses it, and a format defined in one file and split apart in another
 * is a contract nothing checks.
 *
 * A path may in principle carry a space, so the path is everything after the
 * method rather than the third field.
 */
export function operationKey(
  connection: string,
  op: Pick<APIRouteOperation, "method" | "path">,
): string {
  return `${connection} ${op.method.toUpperCase()} ${op.path}`;
}

export interface ParsedOperationKey {
  connection: string;
  method: string;
  path: string;
}

export function parseOperationKey(key: string): ParsedOperationKey | null {
  const [connection, method, ...rest] = key.split(" ");
  if (!connection || !method || rest.length === 0) return null;
  return { connection, method, path: rest.join(" ") };
}
