// The bot check a public endpoint requires (#2173).

export type BotCheckProvider = "pow" | "turnstile" | "hcaptcha" | "none";

export interface BotCheckSettings {
  provider: BotCheckProvider;
  site_key: string;
  secret_name: string;
  allow_public_without_check: boolean;
  difficulty: number;
  max_difficulty: number;
  rate_threshold: number;
  challenge_ttl_seconds: number;
  updated_by?: string;
  updated_at?: string;
  /** Whether this deployment can sign a proof-of-work challenge. */
  key_available: boolean;
  /** Whether a public endpoint can be bound and answered under these settings. */
  public_allowed: boolean;
}

export type BotCheckSettingsInput = Partial<
  Omit<BotCheckSettings, "updated_by" | "updated_at" | "key_available" | "public_allowed">
>;
