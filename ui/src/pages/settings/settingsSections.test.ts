import { describe, it, expect } from "vitest";
import {
  SETTINGS_SECTIONS,
  isSettingsSection,
  settingsSectionFor,
  settingsSectionPath,
} from "./settingsSections";
import { isKnownRoute } from "@/lib/portalRoutes";

describe("settings sections (#2093)", () => {
  it("opens the first section on /admin/settings and on a path naming none", () => {
    expect(settingsSectionFor("/admin/settings")).toBe("email");
    expect(settingsSectionFor("/admin/settings/bogus")).toBe("email");
    expect(settingsSectionFor("/admin/settings/maps")).toBe("maps");
  });

  it("gives every section its own route the shell recognizes", () => {
    for (const s of SETTINGS_SECTIONS) {
      expect(isSettingsSection(s.id)).toBe(true);
      expect(isKnownRoute(settingsSectionPath(s.id))).toBe(true);
    }
    expect(isKnownRoute("/admin/settings")).toBe(true);
    expect(isKnownRoute("/admin/settings/bogus")).toBe(false);
    expect(isSettingsSection("bogus")).toBe(false);
  });
});
