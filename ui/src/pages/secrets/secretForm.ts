import type { Secret, SecretInput, SecretKind } from "@/api/admin/types";

// The Secrets editor's form (#2051): what the person typed, the checks the
// server would refuse it for, and the body a save sends. The rules mirror
// internal/secretstore.Validate so a refusal is shown before the round trip.

export interface SecretForm {
  name: string;
  description: string;
  /** kind is a value, or an authenticator seed whose codes are sent (#2065). */
  kind: SecretKind;
  /** value is empty on an edit that keeps the stored value. */
  value: string;
  connections: string[];
  personas: string[];
}

export const NAME_PATTERN = /^[a-z0-9][a-z0-9_.-]{0,62}$/;
export const MIN_VALUE_LENGTH = 6;

export const EMPTY_FORM: SecretForm = {
  name: "",
  description: "",
  kind: "value",
  value: "",
  connections: [],
  personas: [],
};

export function fromSecret(s: Secret): SecretForm {
  return {
    name: s.name,
    description: s.description,
    kind: s.kind ?? "value",
    value: "",
    connections: [...s.allow_connections],
    personas: [...s.allow_personas],
  };
}

/** placeholder is how a request references the secret: its value, or the
 * current code of an authenticator seed. */
export function placeholder(name: string, kind: SecretKind = "value"): string {
  return kind === "totp" ? `{{totp:${name}}}` : `{{secret:${name}}}`;
}

/** problems lists what the server would refuse the form for. stored is the
 * kind the secret has now, when it is being edited. */
export function problems(form: SecretForm, creating: boolean, stored?: SecretKind): string[] {
  const out: string[] = [];
  if (creating && !NAME_PATTERN.test(form.name)) {
    out.push(
      "The name is lower case letters, digits, '.', '_' and '-', starting with a letter or digit.",
    );
  }
  out.push(...valueProblems(form, creating, stored));
  if (form.connections.length === 0)
    out.push("Choose at least one connection the secret may be sent through.");
  return out;
}

/** The refusal for a value or seed that is required and missing, by kind. */
const MISSING = {
  value: { create: "A new secret needs a value.", change: "Changing to a value needs the value." },
  totp: { create: "A new authenticator seed needs its seed.", change: "Changing to an authenticator seed needs the seed." },
} as const;

/** valueProblems lists what is wrong with the value or seed typed. */
function valueProblems(form: SecretForm, creating: boolean, stored?: SecretKind): string[] {
  const empty = form.value === "";
  const kindChanged = !creating && stored !== undefined && stored !== form.kind;
  if (empty && creating) return [MISSING[form.kind].create];
  if (empty && kindChanged) return [MISSING[form.kind].change];
  const short = form.kind === "value" && !empty && form.value.length < MIN_VALUE_LENGTH;
  return short ? [`The value must be at least ${MIN_VALUE_LENGTH} characters.`] : [];
}

/** toInput is the body a save sends; an empty value on an edit keeps the stored one. */
export function toInput(form: SecretForm): SecretInput {
  const body: SecretInput = {
    description: form.description.trim(),
    kind: form.kind,
    allow_connections: form.connections,
    allow_personas: form.personas,
  };
  if (form.value !== "") body.value = form.value;
  return body;
}

/** toggled adds item to list, or removes it when it is there. */
export function toggled(list: string[], item: string): string[] {
  return list.includes(item)
    ? list.filter((x) => x !== item)
    : [...list, item].sort();
}

/** choices is the names to offer: the ones known, and any the secret already
 * names that are not among them (a connection since removed), so saving never
 * drops one silently. */
export function choices(known: string[], chosen: string[]): string[] {
  return [...new Set([...known, ...chosen])].sort();
}
