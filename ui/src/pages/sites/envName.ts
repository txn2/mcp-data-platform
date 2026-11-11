// An environment's name is the second half of its address on the pages domain,
// <site>--<env>, one DNS label (#2604): the checks the server makes, shown as
// the name is typed.

const ENV_NAME_RE = /^[a-z][a-z0-9-]{0,31}$/;
const MAX_LABEL = 63;

/** Why an environment name cannot be used, "" when it can. */
export function envNameProblem(site: string, name: string): string {
  if (!name) return "";
  if (!ENV_NAME_RE.test(name) || name.includes("--") || name.endsWith("-")) {
    return "Use lowercase letters, digits and single hyphens, starting with a letter, up to 32.";
  }
  const label = `${site}--${name}`;
  if (label.length > MAX_LABEL) {
    return `${label} is ${label.length} characters, and one part of an address can be at most ${MAX_LABEL}. Choose a name of ${Math.max(1, MAX_LABEL - site.length - 2)} characters or fewer.`;
  }
  return "";
}
