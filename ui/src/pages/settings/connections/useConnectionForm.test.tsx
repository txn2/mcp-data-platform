import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import type { EffectiveConnection } from "@/api/admin/types";

// The save mutation is the hook's only external dependency. Mock it so the
// tests exercise the create/edit lifecycle (validity, dirty tracking, and the
// ref-backed config read at save time) without a QueryClient or network.
const mutate = vi.fn();
vi.mock("@/api/admin/hooks", () => ({
  useSetConnectionInstance: () => ({ mutate, isPending: false }),
}));

import { useConnectionForm } from "./useConnectionForm";

const noop = () => {};

beforeEach(() => {
  mutate.mockReset();
});

describe("useConnectionForm — create mode", () => {
  it("defaults to trino and is invalid until the required host is set", () => {
    const { result } = renderHook(() =>
      useConnectionForm({ connection: null, onSave: noop, onDirtyChange: noop }),
    );
    expect(result.current.kind).toBe("trino");
    expect(result.current.isConfigValid).toBe(false);

    act(() => result.current.updateConfig({ host: "trino.example.com" }));
    expect(result.current.isConfigValid).toBe(true);
  });

  it("validates the identifier: must start with a lowercase letter", () => {
    const { result } = renderHook(() =>
      useConnectionForm({ connection: null, onSave: noop, onDirtyChange: noop }),
    );
    act(() => result.current.setName("1bad"));
    expect(result.current.nameValid).toBe(false);
    act(() => result.current.setName("good-name_1"));
    expect(result.current.nameValid).toBe(true);
  });

  it("applies per-kind required-field validity", () => {
    const { result } = renderHook(() =>
      useConnectionForm({ connection: null, onSave: noop, onDirtyChange: noop }),
    );
    act(() => result.current.setKind("api"));
    expect(result.current.isConfigValid).toBe(false);
    act(() => result.current.updateConfig({ base_url: "https://api.example.com" }));
    expect(result.current.isConfigValid).toBe(true);

    act(() => result.current.setKind("mcp"));
    // kind change resets config in create mode → endpoint missing again.
    expect(result.current.isConfigValid).toBe(false);
    act(() => result.current.updateConfig({ endpoint: "https://x.example/mcp" }));
    expect(result.current.isConfigValid).toBe(true);

    act(() => result.current.setKind("s3"));
    // s3 has no hard-required field, so it is valid immediately.
    expect(result.current.isConfigValid).toBe(true);
  });

  it("reports dirty once a name is entered", () => {
    const onDirtyChange = vi.fn();
    const { result } = renderHook(() =>
      useConnectionForm({ connection: null, onSave: noop, onDirtyChange }),
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    act(() => result.current.setName("prod"));
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
  });

  it("saves the latest config read through the ref, not stale state", () => {
    const onSave = vi.fn();
    const { result } = renderHook(() =>
      useConnectionForm({ connection: null, onSave, onDirtyChange: noop }),
    );
    act(() => result.current.setName("prod"));
    act(() => result.current.updateConfig({ host: "h1" }));
    act(() => result.current.handleSave());
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(mutate.mock.calls[0]![0]).toMatchObject({
      kind: "trino",
      name: "prod",
      config: { host: "h1" },
    });
  });
});

describe("useConnectionForm — edit mode", () => {
  const existing: EffectiveConnection = {
    kind: "trino",
    name: "prod",
    connection: "prod",
    source: "database",
    tools: [],
    description: "orig",
    config: { host: "h1" },
  };

  it("starts clean and becomes dirty when a field changes", () => {
    const onDirtyChange = vi.fn();
    const { result } = renderHook(() =>
      useConnectionForm({ connection: existing, onSave: noop, onDirtyChange }),
    );
    expect(result.current.isCreate).toBe(false);
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    act(() => result.current.setDescription("changed"));
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
  });

  it("surfaces a save error from the mutation", () => {
    mutate.mockImplementation((_args, { onError }) => onError(new Error("boom")));
    const { result } = renderHook(() =>
      useConnectionForm({ connection: existing, onSave: noop, onDirtyChange: noop }),
    );
    act(() => result.current.handleSave());
    expect(result.current.saveError).toBe("boom");
  });
});
