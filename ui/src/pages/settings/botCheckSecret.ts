import type { SecretInput } from "@/api/admin/types";
import { MIN_VALUE_LENGTH, NAME_PATTERN } from "../secrets/secretForm";

// A Turnstile or hCaptcha bot check reads its provider's secret key from a
// stored secret that lists bot_check among its uses and names no personas
// (internal/botcheck.SecretUse). The bot check editor can store that secret
// itself, so an administrator never needs to know the reserved use exists
// (#2388). The key goes to the secrets API alone, which encrypts it and never
// returns it; it is not part of the bot check's own record.

/** BOT_CHECK_USE is what a provider's stored secret lists in allow_connections. */
export const BOT_CHECK_USE = "bot_check";

/** NewSecret is the secret the editor stores before it saves the check. */
export interface NewSecret {
  name: string;
  key: string;
}

/** newSecretProblems lists what would stop the secret being stored: the rules
 * internal/secretstore.Validate applies, and a name already taken, which a
 * save would otherwise replace (value, uses and personas) in place. */
export function newSecretProblems(s: NewSecret, existing: string[]): string[] {
  const out: string[] = [];
  if (!NAME_PATTERN.test(s.name)) {
    out.push("The secret's name is lower case letters, digits, '.', '_' and '-', starting with a letter or digit.");
  } else if (existing.includes(s.name)) {
    out.push(`A secret named ${s.name} already exists. Choose it from the list, or give the new one another name.`);
  }
  if (s.key.length < MIN_VALUE_LENGTH) {
    out.push(`The secret key must be at least ${MIN_VALUE_LENGTH} characters.`);
  }
  return out;
}

/** botCheckSecretInput is the body that stores a provider's secret key for one
 * check: a value secret usable by bot checks and by no persona. */
export function botCheckSecretInput(checkName: string, key: string): SecretInput {
  return {
    description: `Provider secret key for the ${checkName} bot check.`,
    kind: "value",
    value: key,
    allow_connections: [BOT_CHECK_USE],
    allow_personas: [],
  };
}

/** isBotCheckSecret reports whether a stored secret is one a provider check can
 * use: a value whose uses include bot_check and whose personas are none. */
export function isBotCheckSecret(s: { kind?: string; allow_connections: string[]; allow_personas: string[] }): boolean {
  return (s.kind ?? "value") === "value" && s.allow_connections.includes(BOT_CHECK_USE) && s.allow_personas.length === 0;
}
