/**
 * Format a tool name for display.
 * Priority: title from backend > fallback snake_case → Title Case.
 */
export function formatToolName(name: string, title?: string): string {
  if (title) return title;
  return name
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}
