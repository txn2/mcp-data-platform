import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Resource } from "@/api/resources/types";
import { ResourceContent } from "./ResourceContent";

// A managed resource was view-only in the portal: its bytes could only be
// changed by picking a whole replacement file off disk, and the one control
// named Edit opened the metadata dialog. Somebody looking at their own CSV
// pressed it and could not touch the file (#1775).
//
// CodeMirror is stubbed to a textarea, the way every other test of a page that
// hosts the shared editor does it: the editor has its own tests, and driving a
// contenteditable would test that library rather than this component.
vi.mock("@/components/SourceEditor", () => ({
  SourceEditor: ({
    content,
    contentType,
    fileName,
    onChange,
  }: {
    content: string;
    contentType: string;
    fileName?: string;
    onChange: (v: string) => void;
  }) => (
    <textarea
      aria-label="Source"
      data-content-type={contentType}
      data-file-name={fileName}
      value={content}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));

const CSV = "month,factor\njanuary,0.82\n";

const RESOURCE: Resource = {
  id: "res-1",
  scope: "user",
  scope_id: "rachel",
  path: "references",
  filename: "seasonal-factors.csv",
  display_name: "Seasonal Factors",
  description: "Monthly demand multipliers.",
  mime_type: "text/csv",
  size_bytes: CSV.length,
  s3_key: "resources/user/rachel/res-1/seasonal-factors.csv",
  uri: "mcp://user/rachel/references/seasonal-factors.csv",
  tags: [],
  uploader_sub: "rachel",
  uploader_email: "rachel@example.com",
  created_at: "2026-08-03T10:00:00Z",
  updated_at: "2026-08-17T10:00:00Z",
};

/** The requests the component made, so a save can be read off the wire. */
let calls: { url: string; init?: RequestInit }[] = [];

function stubApi(
  replace: { status: number; body?: unknown } = { status: 200 },
) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      if (init?.method === "POST") {
        return Promise.resolve(
          new Response(JSON.stringify(replace.body ?? RESOURCE), {
            status: replace.status,
          }),
        );
      }
      if (url.endsWith("/content")) {
        return Promise.resolve(new Response(CSV, { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderContent(resource: Resource = RESOURCE, canModify = true) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ResourceContent resource={resource} canModify={canModify} />
    </QueryClientProvider>,
  );
}

/** A File's contents, the only way jsdom offers to read one. */
function readFileText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  });
}

/** The one POST a save makes, as its parsed form. */
function savedForm(): FormData {
  const post = calls.find((c) => c.init?.method === "POST");
  if (!post) throw new Error("no save was sent");
  return post.init?.body as FormData;
}

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
  stubApi();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("editing a managed resource's source", () => {
  it("offers the Preview/Source switch to whoever may change the file", async () => {
    renderContent();
    expect(await screen.findByRole("button", { name: "Source" })).toBeTruthy();
  });

  it("offers no editor to a reader who may not change the file", async () => {
    renderContent(RESOURCE, false);
    await screen.findByTestId("resource-content");
    expect(screen.queryByRole("button", { name: "Source" })).toBeNull();
  });

  it("offers no editor for a family the editor cannot represent", async () => {
    renderContent({
      ...RESOURCE,
      mime_type: "image/png",
      filename: "chart.png",
    });
    await screen.findByTestId("resource-content");
    expect(screen.queryByRole("button", { name: "Source" })).toBeNull();
  });

  it("offers no editor for a file past the inline limit, which is too large to load into one", async () => {
    renderContent({ ...RESOURCE, size_bytes: 500 * 1024 * 1024 });
    expect(
      await screen.findByTestId("resource-content-too-large"),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Source" })).toBeNull();
  });

  it("saves the edited text through the replace-content route, naming what it did", async () => {
    renderContent();
    fireEvent.click(await screen.findByRole("button", { name: "Source" }));

    const editor = await screen.findByLabelText("Source");
    expect((editor as HTMLTextAreaElement).value).toBe(CSV);
    fireEvent.change(editor, {
      target: { value: "month,factor\njanuary,0.99\n" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());

    const post = calls.find((c) => c.init?.method === "POST");
    expect(post?.url).toContain("/res-1/content");

    const form = savedForm();
    // The version says why it exists, which is what tells a reader of the
    // history that this was an edit rather than a file picked off disk.
    expect(form.get("change_summary")).toBe("edited in the portal");
    // The route ignores the uploaded part's name deliberately, but sending the
    // resource's own keeps the two in step for anything that reads it.
    const file = form.get("file") as File;
    expect(file.name).toBe("seasonal-factors.csv");
    // Read through FileReader: jsdom's File implements neither text() nor a
    // Response body that stringifies it.
    expect(await readFileText(file)).toBe("month,factor\njanuary,0.99\n");

    // Before the file: the route stops its walk at the file part, so a field
    // behind it would never be read.
    const names = [...form.keys()];
    expect(names.indexOf("change_summary")).toBeLessThan(names.indexOf("file"));
  });

  it("refuses to enable Save until the text actually changes", async () => {
    renderContent();
    fireEvent.click(await screen.findByRole("button", { name: "Source" }));
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);

    fireEvent.change(await screen.findByLabelText("Source"), {
      target: { value: "changed" },
    });
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });

  it("says so when the save is refused, and keeps the edit", async () => {
    stubApi({ status: 403, body: { error: "you may not change this file" } });
    renderContent();
    fireEvent.click(await screen.findByRole("button", { name: "Source" }));
    fireEvent.change(await screen.findByLabelText("Source"), {
      target: { value: "changed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.getByText("Save failed")).toBeTruthy());
    expect(screen.getByText("you may not change this file")).toBeTruthy();
    expect((screen.getByLabelText("Source") as HTMLTextAreaElement).value).toBe(
      "changed",
    );
  });

  it("keeps an unsaved edit across a switch back to Preview", async () => {
    renderContent();
    fireEvent.click(await screen.findByRole("button", { name: "Source" }));
    fireEvent.change(await screen.findByLabelText("Source"), {
      target: { value: "month,factor\njanuary,0.99\n" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect((screen.getByLabelText("Source") as HTMLTextAreaElement).value).toBe(
      "month,factor\njanuary,0.99\n",
    );
  });
});
