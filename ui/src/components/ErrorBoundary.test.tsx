import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { Suspense, lazy } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";

// React logs the caught error itself; the boundary logs it again on purpose.
// Neither is a test failure, and both are noise here.
let consoleError: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  consoleError.mockRestore();
});

function Boom(): React.ReactElement {
  throw new Error("boom");
}

describe("ErrorBoundary", () => {
  it("renders its children when nothing fails", () => {
    render(
      <ErrorBoundary fallback={<p>fallback</p>}>
        <p>the page</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("the page")).toBeInTheDocument();
    expect(screen.queryByText("fallback")).not.toBeInTheDocument();
  });

  it("shows the fallback instead of unmounting when a child throws", () => {
    render(
      <ErrorBoundary fallback={<p>fallback</p>}>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByText("fallback")).toBeInTheDocument();
  });

  // The failure this exists for: a lazy chunk that never arrives. Suspense
  // does not catch a rejected import — without the boundary React unmounts the
  // whole tree and leaves a blank document.
  it("catches a chunk that fails to load", async () => {
    const Missing = lazy(() => Promise.reject(new Error("chunk 404")));

    render(
      <ErrorBoundary fallback={<p>could not load</p>}>
        <Suspense fallback={<p>loading</p>}>
          <Missing />
        </Suspense>
      </ErrorBoundary>,
    );

    await waitFor(() => expect(screen.getByText("could not load")).toBeInTheDocument());
  });

  // Navigating away from a broken page has to clear the error, or one bad
  // route pins the fallback there until a full reload.
  it("resets when resetKey changes", () => {
    const { rerender } = render(
      <ErrorBoundary resetKey="/broken" fallback={<p>fallback</p>}>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByText("fallback")).toBeInTheDocument();

    rerender(
      <ErrorBoundary resetKey="/other" fallback={<p>fallback</p>}>
        <p>the other page</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("the other page")).toBeInTheDocument();
    expect(screen.queryByText("fallback")).not.toBeInTheDocument();
  });

  it("stays failed while resetKey is unchanged", () => {
    const { rerender } = render(
      <ErrorBoundary resetKey="/broken" fallback={<p>fallback</p>}>
        <Boom />
      </ErrorBoundary>,
    );
    rerender(
      <ErrorBoundary resetKey="/broken" fallback={<p>fallback</p>}>
        <p>never shown</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText("fallback")).toBeInTheDocument();
  });
});
