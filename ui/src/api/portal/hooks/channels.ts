import { useQuery, useMutation } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- Post to a channel (#1723) and a run's posts ---

// PortalChannel is one channel the signed-in person can post to, as the
// notify tool's list action reports it.
export interface PortalChannel {
  name: string;
  kind: string;
  mode: string;
  description?: string;
  carries: string;
}

export interface PortalChannelList {
  channels: PortalChannel[];
  // note says why the list is empty.
  note?: string;
}

// ChannelPostResult is what the notify tool answered a post with.
export interface ChannelPostResult {
  channel: string;
  queued: number;
  suppressed?: boolean;
  detail: string;
}

// usePortalChannels lists the channels the signed-in person reaches. It is
// read only while the dialog that needs it is open.
export function usePortalChannels(enabled: boolean) {
  return useQuery({
    queryKey: ["portal-channels"],
    queryFn: () => apiFetch<PortalChannelList>("/channels"),
    enabled,
  });
}

// usePublishToChannel posts an asset to a channel.
export function usePublishToChannel() {
  return useMutation({
    mutationFn: ({ channel, assetId, message }: { channel: string; assetId: string; message?: string }) =>
      apiFetch<ChannelPostResult>(`/channels/${encodeURIComponent(channel)}/publish`, {
        method: "POST",
        body: JSON.stringify({ asset_id: assetId, message: message || undefined }),
      }),
  });
}

// RunPost is one post a script run made, and what became of it.
export interface RunPost {
  channel: string;
  title: string;
  queued_at: string;
  status: "sent" | "failed" | "queued";
  deliveries: number;
  sent: number;
  failed: number;
  sent_at?: string;
  last_error?: string;
}

// useRunPosts reads the posts one run made.
export function useRunPosts(scriptId: string, runId: string) {
  return useQuery({
    queryKey: ["script-run-posts", scriptId, runId],
    queryFn: () =>
      apiFetch<{ posts: RunPost[] }>(
        `/scripts/${encodeURIComponent(scriptId)}/runs/${encodeURIComponent(runId)}/posts`,
      ),
    enabled: Boolean(scriptId && runId),
  });
}
