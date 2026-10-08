import type { Secret, SecretInput } from "@/api/admin/types";

// The Secrets editor's form (#2051): what the person typed, the checks the
// server would refuse it for, and the body a save sends. The rules mirror
// internal/secretstore.Validate so a refusal is shown before the round trip.

export interface SecretForm {
  name: string;
  description: string;
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
  value: "",
  connections: [],
  personas: [],
};

export function fromSecret(s: Secret): SecretForm {
  return {
    name: s.name,
    description: s.description,
    value: "",
    connections: [...s.allow_connections],
    personas: [...s.allow_personas],
  };
}

/** placeholder is how a request references the secret. */
export function placeholder(name: string): string {
  return `{{secret:${name}}}`;
}

/** problems lists what the server would refuse the form for. */
export function problems(form: SecretForm, creating: boolean): string[] {
  const out: string[] = [];
  if (creating && !NAME_PATTERN.test(form.name)) {
    out.push(
      "The name is lower case letters, digits, '.', '_' and '-', starting with a letter or digit.",
    );
  }
  if (creating && form.value === "") out.push("A new secret needs a value.");
  if (form.value !== "" && form.value.length < MIN_VALUE_LENGTH) {
    out.push(`The value must be at least ${MIN_VALUE_LENGTH} characters.`);
  }
  if (form.connections.length === 0)
    out.push("Choose at least one connection the secret may be sent through.");
  return out;
}

/** toInput is the body a save sends; an empty value on an edit keeps the stored one. */
export function toInput(form: SecretForm): SecretInput {
  const body: SecretInput = {
    description: form.description.trim(),
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
