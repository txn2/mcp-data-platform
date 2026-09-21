import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("@/api/admin/hooks", () => ({
  useTestConnectionInstance: vi.fn(),
}));

import { useTestConnectionInstance } from "@/api/admin/hooks";
import { ConnectionTestButton, canTestConnection } from "./ConnectionTestButton";

const mockTest = vi.mocked(useTestConnectionInstance);

type TestMutation = ReturnType<typeof useTestConnectionInstance>;

// renderButton wires the component to a mutation that resolves or rejects
// however a case needs.
function renderButton(mutateAsync: () => Promise<unknown>) {
  mockTest.mockReturnValue({
    mutateAsync,
    isPending: false,
  } as unknown as TestMutation);
  return render(<ConnectionTestButton kind="trino" name="warehouse" />);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("ConnectionTestButton", () => {
  // A success has to say what answered. A bare tick against the wrong
  // credential looks identical to one against the right credential, which is
  // the ambiguity this whole endpoint exists to remove (#1805).
  it("reports what answered on a successful test", async () => {
    renderButton(() =>
      Promise.resolve({
        kind: "trino",
        name: "warehouse",
        ok: true,
        detail: "the query engine answered SELECT 1",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /test connection/i }));

    expect(await screen.findByText(/the connection answered/i)).toBeInTheDocument();
    expect(screen.getByText(/answered SELECT 1/)).toBeInTheDocument();
  });

  // The failing half is the one that matters: the upstream's own words are
  // what tell an operator whether the credential, the host or the route is
  // wrong, so they must reach the panel rather than being flattened into
  // "failed".
  it("shows the upstream's own error when the connection does not answer", async () => {
    renderButton(() =>
      Promise.resolve({
        kind: "trino",
        name: "warehouse",
        ok: false,
        detail: 'connection "warehouse" could not be opened',
        error: 'invalid DSN: parse "https://${TRINO_USER}:...": net/url: invalid userinfo',
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /test connection/i }));

    expect(await screen.findByText(/the connection did not answer/i)).toBeInTheDocument();
    expect(screen.getByText(/invalid userinfo/)).toBeInTheDocument();
  });

  // A test that could not be run at all is a different thing from a connection
  // that answered badly, and is reported as such rather than as an unhealthy
  // connection.
  it("separates a test that could not run from a connection that failed", async () => {
    renderButton(() => Promise.reject(new Error("no trino toolkit is registered in this process")));

    fireEvent.click(screen.getByRole("button", { name: /test connection/i }));

    expect(await screen.findByText(/could not run the test/i)).toBeInTheDocument();
    expect(screen.getByText(/no trino toolkit is registered/)).toBeInTheDocument();
    expect(screen.queryByText(/the connection did not answer/i)).not.toBeInTheDocument();
  });

  // The action is offered only for the kinds the endpoint serves. A datahub
  // connection is configured in the platform's YAML rather than managed as an
  // instance, so the endpoint answers "unknown connection kind" and a button
  // offering it would be a refusal with no path in.
  it("is offered only for the kinds that can be tested", () => {
    for (const kind of ["trino", "s3", "api", "graphql"]) {
      expect(canTestConnection(kind)).toBe(true);
    }
    for (const kind of ["datahub", "mcp"]) {
      expect(canTestConnection(kind)).toBe(false);
    }
  });
});
