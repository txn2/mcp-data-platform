// The named bot checks a bot_check endpoint picks from (#2173, #2259).

export type BotCheckProvider = "pow" | "turnstile" | "hcaptcha";

export interface BotCheck {
  name: string;
  provider: BotCheckProvider;
  site_key: string;
  secret_name: string;
  difficulty: number;
  max_difficulty: number;
  rate_threshold: number;
  challenge_ttl_seconds: number;
  created_by?: string;
  updated_by?: string;
  created_at?: string;
  updated_at?: string;
  /** The endpoints that name the check; a check any endpoint names cannot be deleted. */
  used_by: string[];
  /** False for a proof of work on a deployment that cannot sign one. */
  usable: boolean;
}

export interface BotCheckList {
  checks: BotCheck[];
  /** Whether this deployment can sign a proof-of-work challenge. */
  key_available: boolean;
}

export type BotCheckInput = Partial<
  Pick<
    BotCheck,
    | "provider"
    | "site_key"
    | "secret_name"
    | "difficulty"
    | "max_difficulty"
    | "rate_threshold"
    | "challenge_ttl_seconds"
  >
>;
