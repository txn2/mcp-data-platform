// The #633 lifecycle axis (`sink_class`) the Memory views classify and filter
// by, in lifecycle order: the two live-for-the-capturer classes first, then the
// three reviewable classes promoted into knowledge. This is the single source of
// truth for both the filter options and the display label across the portal and
// admin Memory views, so the axis cannot drift between them.
export const SINK_CLASSES: { value: string; label: string }[] = [
  { value: "personal_preference", label: "Preference" },
  { value: "episodic_event", label: "Event" },
  { value: "business_knowledge", label: "Business knowledge" },
  { value: "operational_rule", label: "Operational rule" },
  { value: "schema_entity", label: "Schema/entity" },
];

// sinkClassLabel resolves a record's lifecycle class to display copy, falling
// back to the raw value (empty for rows captured before the axis existed, though
// migration 000069 backfills those from their dimension).
export function sinkClassLabel(sinkClass: string | undefined): string {
  const sc = sinkClass ?? "";
  return SINK_CLASSES.find((c) => c.value === sc)?.label ?? sc;
}
