import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import type { EffectiveConnection } from "@/api/admin/types";
import { ConnectionViewer } from "./ConnectionViewer";

function connection(over: Partial<EffectiveConnection> = {}): EffectiveConnection {
  return {
    kind: "trino",
    name: "acme-warehouse",
    connection: "acme-warehouse",
    source: "both",
    tools: ["trino_query"],
    config: { host: "trino.internal:8080" },
    ...over,
  };
}

function renderViewer(conn: EffectiveConnection) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ConnectionViewer
        connection={conn}
        isReadOnly={false}
        onEdit={vi.fn()}
        onDeleted={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

describe("ConnectionViewer edit and delete affordances", () => {
  // The API refuses both writes for a connection the configuration file
  // declares: a delete takes it out of every live toolkit until each replica
  // restarts, and a save reaches the running process but is discarded at the
  // next restart. The page has to say so rather than offer buttons that 409.
  it("withholds edit and delete and names the file for a file-declared connection", () => {
    renderViewer(connection({ file_declared: true }));

    expect(
      screen.queryByLabelText("Delete trino/acme-warehouse"),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(
      screen.getByText(/declared in the platform configuration file/i),
    ).toBeInTheDocument();
  });

  // source is "both" for a file-declared connection too, because the backfill
  // seeds it a stored row. Keying the affordance on source would withhold
  // delete from every admin-created connection its toolkit serves.
  it("offers edit and delete for a stored connection with the same source", () => {
    renderViewer(connection({ source: "both" }));

    expect(
      screen.getByLabelText("Delete trino/acme-warehouse"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(
      screen.queryByText(/declared in the platform configuration file/i),
    ).not.toBeInTheDocument();
    // The note this replaced claimed a config-file entry for exactly this
    // connection, which an admin-created one does not have.
    expect(
      screen.queryByText(/also exists in the config file/i),
    ).not.toBeInTheDocument();
  });

  it("offers edit and delete for a database-only connection", () => {
    renderViewer(connection({ source: "database" }));

    expect(
      screen.getByLabelText("Delete trino/acme-warehouse"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
  });
});

// A connection can carry both OAuth config vocabularies. The canonical keys
// win, so the legacy ones hold values nothing reads. Listing both sets as
// equals is how a real client id sat beside a placeholder with nothing saying
// which one the connection was live on (#1682).
describe("ConnectionViewer — a connection carrying both OAuth vocabularies", () => {
  const mixed = connection({
    kind: "api",
    name: "analytics",
    connection: "analytics",
    config: {
      base_url: "https://analytics.example.com",
      auth_mode: "oauth",
      oauth_grant: "authorization_code",
      oauth_client_id: "PENDING-REPLACE-ME",
      oauth2_client_id: "986495125425.apps.googleusercontent.com",
    },
  });

  it("marks the shadowed key and says which vocabulary is in use", () => {
    renderViewer(mixed);

    expect(screen.getByText("shadowed")).toBeInTheDocument();
    expect(
      screen.getByText(/both OAuth configuration vocabularies/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText("986495125425.apps.googleusercontent.com"),
    ).toHaveClass("line-through");
    expect(screen.getByText("PENDING-REPLACE-ME")).not.toHaveClass(
      "line-through",
    );
  });

  it("marks nothing on a single-vocabulary connection", () => {
    renderViewer(
      connection({
        kind: "api",
        config: {
          auth_mode: "oauth",
          oauth_grant: "client_credentials",
          oauth_client_id: "platform-client",
        },
      }),
    );

    expect(screen.queryByText("shadowed")).not.toBeInTheDocument();
    expect(
      screen.queryByText(/both OAuth configuration vocabularies/i),
    ).not.toBeInTheDocument();
  });
});
