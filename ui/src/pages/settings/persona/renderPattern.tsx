// renderPattern highlights the `*` wildcards in a pattern string so the rule
// list and resolution trace render them in the accent color. Shared by
// RuleList and Trace (#766).
export function renderPattern(p: string) {
  return p.split("*").map((part, i, arr) => (
    <span key={i}>
      {part}
      {i < arr.length - 1 && (
        <span className="text-violet-600 dark:text-violet-400">*</span>
      )}
    </span>
  ));
}
