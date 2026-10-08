import { describe, expect, it } from "vitest";
import {
  choices,
  EMPTY_FORM,
  fromSecret,
  placeholder,
  problems,
  toggled,
  toInput,
} from "./secretForm";

const secret = {
  name: "portal_password",
  description: "Vendor portal",
  allow_connections: ["grid"],
  allow_personas: [],
  created_by: "a@example.com",
  updated_by: "a@example.com",
  created_at: "2026-10-07T00:00:00Z",
  updated_at: "2026-10-07T00:00:00Z",
};

describe("secretForm", () => {
  it("refuses what the server refuses", () => {
    expect(problems(EMPTY_FORM, true)).toHaveLength(3);
    expect(
      problems(
        { ...EMPTY_FORM, name: "Bad", value: "abc", connections: ["g"] },
        true,
      ),
    ).toEqual([
      expect.stringContaining("lower case"),
      expect.stringContaining("at least 6"),
    ]);
    expect(
      problems(
        { ...EMPTY_FORM, name: "ok", value: "hunter22", connections: ["g"] },
        true,
      ),
    ).toEqual([]);
  });

  it("keeps the stored value on an edit with no value", () => {
    const form = fromSecret(secret);
    expect(form.value).toBe("");
    expect(problems(form, false)).toEqual([]);
    expect(toInput(form)).toEqual({
      description: "Vendor portal",
      allow_connections: ["grid"],
      allow_personas: [],
    });
    expect(toInput({ ...form, value: "rotated!" }).value).toBe("rotated!");
  });

  it("toggles and offers every name", () => {
    expect(toggled(["b"], "a")).toEqual(["a", "b"]);
    expect(toggled(["a", "b"], "a")).toEqual(["b"]);
    expect(choices(["grid"], ["gone", "grid"])).toEqual(["gone", "grid"]);
    expect(placeholder("pw")).toBe("{{secret:pw}}");
  });
});
