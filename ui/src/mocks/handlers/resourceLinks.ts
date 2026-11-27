import { http, HttpResponse } from "msw";

// Upload links (#2844) and download links (#2847) on the Resources page.
// Registered ahead of /api/v1/resources/:id, which would otherwise read
// "upload-links" as a resource id.

const inHours = (h: number) => new Date(Date.now() + h * 3600_000).toISOString();

let links = [
  {
    id: "ul-renders",
    scope: "user",
    scope_id: "dev-user",
    path: "gpulab/renders",
    content_type: "image/png",
    max_bytes: 20 * 1024 * 1024,
    max_files: 4,
    files_used: 1,
    created_at: inHours(-1),
    expires_at: inHours(5),
  },
  {
    id: "ul-weekly",
    scope: "user",
    scope_id: "dev-user",
    path: "reports",
    filename: "weekly.csv",
    content_type: "text/csv",
    max_bytes: 5 * 1024 * 1024,
    max_files: 1,
    files_used: 0,
    created_at: inHours(-2),
    expires_at: inHours(0.2),
  },
];

export const resourceLinkHandlers = [
  http.get("/api/v1/resources/upload-links", () => HttpResponse.json({ links })),
  http.post("/api/v1/resources/upload-links", async ({ request }) => {
    const body = (await request.json()) as { max_files?: number; size_bytes?: number; ttl_seconds?: number; filename?: string };
    return HttpResponse.json(
      {
        id: "ul-new",
        upload_url: "https://data.example.com/api/v1/resources/uploads/Q2xhdWRlIGlzIGF3ZXNvbWUgYnV0IHRoaXMgaXMgZmFrZQ",
        method: "PUT",
        expires_at: new Date(Date.now() + (body.ttl_seconds ?? 900) * 1000).toISOString(),
        max_bytes: body.size_bytes ?? 0,
        max_files: body.max_files ?? 1,
        name_in_url: !body.filename,
      },
      { status: 201 },
    );
  }),
  http.post("/api/v1/resources/upload-links/:id/revoke", ({ params }) => {
    links = links.filter((l) => l.id !== params.id);
    return new HttpResponse(null, { status: 204 });
  }),
  http.get("/api/v1/resources/:id/download-link", ({ params, request }) => {
    const expires = new URL(request.url).searchParams.get("expires") === "6h" ? "6h" : "single_download";
    return HttpResponse.json({
      url: `https://data.example.com/api/v1/resources/downloads/eyJ0b2tlbiI6ImZha2UifQ/${String(params.id)}`,
      expires,
      expires_at: inHours(expires === "6h" ? 6 : 1),
    });
  }),
];
