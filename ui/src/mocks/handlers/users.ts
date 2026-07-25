import type {
  DirectoryUser,
  UserCreateRequest,
  UserListResponse,
  UserUpdateRequest,
} from "@/api/admin/types";
import type { DirectoryUsersResponse } from "@/api/portal/types";
import { http, HttpResponse } from "msw";
import { mockDirectoryUsers } from "../data/users";

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

// Mutable copy so create/update/delete against the mock server persist for the
// session (matching how the other handlers clone their fixtures).
const directoryUsers: DirectoryUser[] = JSON.parse(
  JSON.stringify(mockDirectoryUsers),
);

// Filter by the optional ?q= search param the hooks send (matches name or
// email, case-insensitive), mirroring the server-side directory search.
function searchUsers(url: URL): DirectoryUser[] {
  const q = url.searchParams.get("q")?.trim().toLowerCase();
  if (!q) return directoryUsers;
  return directoryUsers.filter((u) => {
    const name = `${u.first_name} ${u.last_name}`.toLowerCase();
    return name.includes(q) || u.email.toLowerCase().includes(q);
  });
}

export const userHandlers = [
  // --- Admin: known-users directory (#614) ---
  http.get(`${ADMIN_BASE}/users`, ({ request }) => {
    const users = searchUsers(new URL(request.url));
    const body: UserListResponse = { users, total: users.length };
    return HttpResponse.json(body);
  }),

  http.post(`${ADMIN_BASE}/users`, async ({ request }) => {
    const req = (await request.json()) as UserCreateRequest;
    const now = new Date().toISOString();
    const existing = directoryUsers.find((u) => u.email === req.email);
    if (existing) {
      return HttpResponse.json(existing, { status: 200 });
    }
    const created: DirectoryUser = {
      email: req.email,
      first_name: req.first_name ?? "",
      last_name: req.last_name ?? "",
      source: "admin",
      confirmed: false,
      added_by: "sarah.chen@example.com",
      created_at: now,
      updated_at: now,
    };
    directoryUsers.unshift(created);
    return HttpResponse.json(created, { status: 201 });
  }),

  http.put(`${ADMIN_BASE}/users/:email`, async ({ params, request }) => {
    const email = decodeURIComponent(String(params.email));
    const req = (await request.json()) as UserUpdateRequest;
    const user = directoryUsers.find((u) => u.email === email);
    if (!user) return new HttpResponse(null, { status: 404 });
    if (req.first_name !== undefined) user.first_name = req.first_name;
    if (req.last_name !== undefined) user.last_name = req.last_name;
    user.updated_at = new Date().toISOString();
    return HttpResponse.json(user);
  }),

  http.delete(`${ADMIN_BASE}/users/:email`, ({ params }) => {
    const email = decodeURIComponent(String(params.email));
    const idx = directoryUsers.findIndex((u) => u.email === email);
    if (idx !== -1) directoryUsers.splice(idx, 1);
    return new HttpResponse(null, { status: 204 });
  }),

  // --- Portal: directory for the share picker (#614) ---
  http.get(`${PORTAL_BASE}/users`, ({ request }) => {
    const users = searchUsers(new URL(request.url)).map((u) => ({
      email: u.email,
      first_name: u.first_name,
      last_name: u.last_name,
      confirmed: u.confirmed,
    }));
    const body: DirectoryUsersResponse = { users, total: users.length };
    return HttpResponse.json(body);
  }),

  // --- Portal: who may be @-mentioned on a thread target (#627) ---
  //
  // The real endpoint answers with the people who can open the target. The mock
  // stands in the whole directory minus the signed-in user, except for the
  // deliberately-unreachable address the composer's "no access" hint is
  // demonstrated with.
  http.get(`${PORTAL_BASE}/mention-candidates`, ({ request }) => {
    const candidates = searchUsers(new URL(request.url))
      .filter((u) => u.email !== "sarah.chen@example.com" && u.email !== UNREACHABLE_EMAIL)
      .map((u) => ({
        email: u.email,
        first_name: u.first_name,
        last_name: u.last_name,
        confirmed: u.confirmed,
      }));
    return HttpResponse.json({ candidates });
  }),
];

// UNREACHABLE_EMAIL is in the directory but never in an audience, so the
// composer's "does not have access" path is reachable in the mock UI.
const UNREACHABLE_EMAIL = "outside.contractor@example.com";
