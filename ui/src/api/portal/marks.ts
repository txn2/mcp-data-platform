// An asset's status, Verified badge and links (#2502, #2503, #2432): the
// shapes the asset routes and the status, verification and links routes carry.

/** The statuses an asset may carry (#2502). */
export type AssetStatusValue = "draft" | "published" | "deprecated";

/** An asset's status, with who set it and when. */
export interface AssetStatus {
  value: AssetStatusValue;
  /** Shown with the status; a deprecated asset shows it to every reader. */
  note?: string;
  set_by: string;
  set_at: string;
}

/** An asset's Verified badge (#2503). */
export interface AssetVerification {
  verified_by: string;
  verified_at: string;
  /** The version that was checked. */
  verified_version: number;
  note?: string;
  /** When the badge is removed; absent never expires. */
  expires_at?: string;
  /** A version newer than the verified one exists. */
  changed_since_verified: boolean;
}

/** One recorded change to an asset's status or verification. */
export interface AssetMarkEvent {
  kind: "status" | "verification";
  from?: string;
  to?: string;
  note?: string;
  actor: string;
  at: string;
}

/** An asset in a Linked from list (#2432). */
export interface LinkedAssetRef {
  id: string;
  name: string;
  content_type: string;
}

/**
 * What an asset's current content links to (asset ids), and the assets
 * linking to it that the caller can open (#2432).
 */
export interface AssetLinks {
  links: string[];
  linked_from: LinkedAssetRef[];
}
