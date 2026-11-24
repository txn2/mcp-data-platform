// The scheme the platform writes references under (#2776): mcp by default,
// or the deployment's own (server.reference_scheme), read from the branding
// route. Every reference the portal copies or shows is written under it, and
// every one it reads is accepted under it or mcp:.

const CANONICAL = "mcp";
const SCHEME_SHAPE = /^[a-z][a-z0-9-]{0,31}$/;

let scheme = CANONICAL;

/** setReferenceScheme records the deployment's scheme; anything else leaves mcp. */
export function setReferenceScheme(next: string | undefined | null): void {
  scheme = next && SCHEME_SHAPE.test(next) ? next : CANONICAL;
}

/** referenceScheme is the scheme references are written under. */
export function referenceScheme(): string {
  return scheme;
}

/** canonRef is a reference in the canonical mcp: form. */
export function canonRef(ref: string): string {
  const trimmed = ref.trim();
  if (scheme !== CANONICAL && trimmed.startsWith(`${scheme}:`)) {
    return `${CANONICAL}:${trimmed.slice(scheme.length + 1)}`;
  }
  return trimmed;
}

/** displayRef is a canonical reference written under the deployment's scheme. */
export function displayRef(ref: string): string {
  if (scheme !== CANONICAL && ref.startsWith(`${CANONICAL}:`)) {
    return `${scheme}:${ref.slice(CANONICAL.length + 1)}`;
  }
  return ref;
}

/** refOf is the reference of one entity, written under the deployment's scheme. */
export function refOf(kind: string, id: string): string {
  return displayRef(`${CANONICAL}:${kind}:${id}`);
}
