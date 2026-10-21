/**
 * initials is the one or two letters an avatar shows for a signed-in person
 * (#2215): the first letters of the name before an address's "@", split on
 * dots, dashes and underscores, or of an id when there is no address.
 */
export function initials(identity: string): string {
  const local = identity.split("@")[0] ?? "";
  const words = local.split(/[._\-\s]+/).filter(Boolean);
  if (words.length === 0) return "?";
  const letters = words.length > 1 ? words[0]![0]! + words[1]![0]! : words[0]!.slice(0, 2);
  return letters.toUpperCase();
}
