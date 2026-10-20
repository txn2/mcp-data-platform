import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { GraphQLConfigForm } from "./GraphQLConfigForm";
import { ApiGatewayConfigForm } from "./ApiGatewayConfigForm";

// A connection's max_concurrent_calls (#2209): both HTTP kinds show it and
// save it as a number, and clearing it removes the key, which is unlimited.

afterEach(cleanup);

function wrap(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

describe.each([
  ["graphql", GraphQLConfigForm],
  ["api", ApiGatewayConfigForm],
])("%s connection form", (_kind, Form) => {
  it("reads and writes max_concurrent_calls as a number", () => {
    const onChange = vi.fn();
    render(
      wrap(
        <Form
          config={{ max_concurrent_calls: 2 }}
          onChange={onChange}
          connectionName="erp"
          isCreate={false}
        />,
      ),
    );
    const field = screen.getByLabelText(/Max concurrent calls/);
    expect(field).toHaveValue(2);
    fireEvent.change(field, { target: { value: "1" } });
    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ max_concurrent_calls: 1 }),
    );
    fireEvent.change(field, { target: { value: "" } });
    expect(onChange.mock.lastCall?.[0]).not.toHaveProperty(
      "max_concurrent_calls",
      expect.anything(),
    );
  });
});
