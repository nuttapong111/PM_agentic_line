// All settings come from environment variables. See .env.example.

function req(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`Missing required env var ${name}`);
  return v;
}

function opt(name: string, fallback: string): string {
  return process.env[name] || fallback;
}

function parseJson<T>(name: string, fallback: T): T {
  const raw = process.env[name];
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    throw new Error(`${name} must be valid JSON`);
  }
}

export const config = {
  port: Number(opt("PORT", "3000")),
  publicUrl: opt("PUBLIC_URL", "http://localhost:3000").replace(/\/$/, ""),
  timezone: opt("TZ_NAME", "Asia/Bangkok"),
  dataDir: opt("DATA_DIR", "./data"),

  line: {
    channelSecret: req("LINE_CHANNEL_SECRET"),
    accessToken: req("LINE_CHANNEL_ACCESS_TOKEN"),
    // Only this LINE user may use the bot. Leave empty on first run: the bot
    // replies with your user ID so you can set it.
    ownerUserId: opt("OWNER_LINE_USER_ID", ""),
  },

  claude: {
    model: opt("CLAUDE_MODEL", "claude-haiku-5-5"),
    effort: opt("CLAUDE_EFFORT", "low") as "low" | "medium" | "high",
    maxToolRounds: Number(opt("CLAUDE_MAX_TOOL_ROUNDS", "8")),
  },

  plane: {
    baseUrl: req("PLANE_BASE_URL").replace(/\/$/, ""),
    apiKey: req("PLANE_API_KEY"),
    workspace: req("PLANE_WORKSPACE_SLUG"),
    // Project identifiers to follow (e.g. ["FDABIGDATA","PEALICENSE"]). Empty = all.
    projects: parseJson<string[]>("PLANE_PROJECTS", []),
    // Nicknames → Plane member email or display name, e.g. {"ต้น":"ton@corp.com"}
    nicknames: parseJson<Record<string, string>>("NICKNAMES", {}),
    webhookSecret: opt("PLANE_WEBHOOK_SECRET", ""),
    staleDays: Number(opt("STALE_DAYS", "5")),
    bugLabel: opt("BUG_LABEL", "bug"),
  },

  digestCron: opt("DIGEST_CRON", "30 8 * * 1-5"),
  dashboardToken: req("DASHBOARD_TOKEN"),
};

export type Config = typeof config;
