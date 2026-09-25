/**
 * A content read that produced no body: the server answered with a non-2xx
 * status, or the request never got an answer. `status` is undefined for the
 * second, so a surface can say which one happened rather than wait forever for
 * a body that is not coming (#1874).
 */
export class ContentFetchError extends Error {
  constructor(public status: number | undefined) {
    super(status === undefined ? "network error" : `HTTP ${status}`);
    this.name = "ContentFetchError";
  }
}

/**
 * Reads a content endpoint as text, turning every way it can fail to produce
 * a body into a ContentFetchError.
 */
export async function fetchContentText(request: () => Promise<Response>): Promise<string> {
  let res: Response;
  try {
    res = await request();
  } catch {
    throw new ContentFetchError(undefined);
  }
  if (!res.ok) throw new ContentFetchError(res.status);
  return res.text();
}

/** What failed, in the words an error state names it by. */
export function contentErrorLabel(error: unknown): string {
  if (error instanceof ContentFetchError) return error.message;
  if (error instanceof Error && error.message) return error.message;
  return "unknown error";
}
