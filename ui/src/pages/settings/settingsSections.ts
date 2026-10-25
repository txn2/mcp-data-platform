// The sections of Admin > Settings (#2093), in nav order. Each is a route
// beneath SETTINGS_ROUTE; /admin/settings itself opens the first. A new
// section is one entry here and its card in AdminSettingsPage's SECTION_CARDS.

export const SETTINGS_ROUTE = "/admin/settings";

export const SECTION_GROUPS = ["Delivery", "Alerts", "Content", "Endpoints", "Access"] as const;

export const SETTINGS_SECTIONS = [
  { id: "email", label: "Email", group: "Delivery" },
  { id: "channels", label: "Channels", group: "Delivery" },
  { id: "knowledge-review", label: "Knowledge review", group: "Alerts" },
  { id: "connection-revocations", label: "Connection revocations", group: "Alerts" },
  { id: "run-failures", label: "Run failures", group: "Alerts" },
  { id: "webhooks", label: "Webhooks", group: "Alerts" },
  { id: "storage", label: "Storage", group: "Alerts" },
  { id: "maps", label: "Maps", group: "Content" },
  { id: "bot-check", label: "Bot check", group: "Endpoints" },
  { id: "access-requests", label: "Access requests", group: "Access" },
] as const satisfies readonly { id: string; label: string; group: (typeof SECTION_GROUPS)[number] }[];

export type SettingsSectionId = (typeof SETTINGS_SECTIONS)[number]["id"];

// isSettingsSection reports whether id names a section.
export function isSettingsSection(id: string): id is SettingsSectionId {
  return SETTINGS_SECTIONS.some((s) => s.id === id);
}

// settingsSectionFor is the section a route names: the first for
// /admin/settings itself and for a path naming no section.
export function settingsSectionFor(route: string): SettingsSectionId {
  const id = route.slice(SETTINGS_ROUTE.length + 1);
  return isSettingsSection(id) ? id : SETTINGS_SECTIONS[0].id;
}

// settingsSectionPath is a section's route.
export function settingsSectionPath(id: SettingsSectionId): string {
  return `${SETTINGS_ROUTE}/${id}`;
}
