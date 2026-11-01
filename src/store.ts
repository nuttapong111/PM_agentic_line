// Owner state: PostgreSQL when DATABASE_URL is set, otherwise a JSON file in DATA_DIR.
// Holds the conversation, pending confirmations, connections (secrets encrypted) and preferences.
import fs from "node:fs";
import path from "node:path";
import type Anthropic from "@anthropic-ai/sdk";
import { config } from "./config.js";
import { loadState, saveState, migrate, sql } from "./db.js";

export interface PendingAction {
  id: string;
  createdAt: number;
  /** tool_result blocks already computed for read tools in the same assistant turn */
  readyResults: Anthropic.ToolResultBlockParam[];
  /** write tool calls waiting for the owner's confirmation */
  writes: { toolUseId: string; name: string; input: unknown; preview: unknown }[];
}

/** A confirm card the bot sent on its own (digest, alerts), not from a chat turn. */
export interface ProactiveAction {
  id: string;
  createdAt: number;
  name: string;
  preview: unknown;
}

export interface Conversation {
  messages: Anthropic.MessageParam[];
  lastAt: number;
}

export interface PlaneConnection {
  baseUrl: string;
  workspace: string;
  projects: string[]; // identifiers to follow; empty = all
  tokenEnc: string;
  tokenLast4: string;
  account?: { name: string; email?: string };
  checkedAt?: number;
  ok?: boolean;
}

export interface Connections {
  task: { provider: "plane" | "builtin" | "none" | "env"; plane?: PlaneConnection };
  claude?: { keyEnc: string; last4: string; savedAt: number };
  google?: {
    clientId: string;
    clientSecretEnc: string;
    refreshEnc?: string;
    email?: string;
    connectedAt?: number;
  };
  notion?: { tokenEnc: string; last4: string; botName?: string; savedAt: number };
}

export interface Prefs {
  nicknames: Record<string, string>;
  digestTime: string; // HH:MM
  workdaysOnly: boolean;
  quietFrom: string;
  quietTo: string;
  staleDays: number;
  bugLabel: string;
  weekly: { enabled: boolean; day: number; time: string; to: string[]; cc: string[] };
  meetingPrepMinutes: number; // 0 = off
  milestoneAlertDays: number[];
  nudge: { enabled: boolean; day: number; time: string };
  leaveCalendarId: string;
  notionParentPageId: string; // where retro / CR documents are created
  manDayHours: number;
}

interface State {
  currentProjectId: string | null;
  conversation: Conversation;
  pending: PendingAction | null;
  proactive: Record<string, ProactiveAction>;
  /** dedupe keys for alerts already sent (value = epoch ms) */
  sent: Record<string, number>;
  oauth: { state: string; at: number } | null;
  connections: Connections;
  prefs: Prefs;
  usage: { month: string; inputTokens: number; outputTokens: number; cacheReadTokens: number };
}

export function defaultPrefs(): Prefs {
  const [h, m] = parseCron(config.digestCron);
  return {
    nicknames: config.plane.nicknames,
    digestTime: `${h}:${m}`,
    workdaysOnly: true,
    quietFrom: "20:00",
    quietTo: "08:00",
    staleDays: config.plane.staleDays,
    bugLabel: config.plane.bugLabel,
    weekly: { enabled: true, day: 5, time: "16:00", to: [], cc: [] },
    meetingPrepMinutes: 15,
    milestoneAlertDays: [14, 7, 3],
    nudge: { enabled: true, day: 4, time: "09:00" },
    leaveCalendarId: "",
    notionParentPageId: "",
    manDayHours: 8,
  };
}

function parseCron(c: string): [string, string] {
  const [m, h] = c.split(/\s+/);
  const pad = (x: string) => (/^\d+$/.test(x) ? x.padStart(2, "0") : "00");
  return [pad(h ?? "8"), pad(m ?? "30")];
}

function empty(): State {
  return {
    currentProjectId: null,
    conversation: { messages: [], lastAt: 0 },
    pending: null,
    proactive: {},
    sent: {},
    oauth: null,
    connections: { task: { provider: "env" } },
    prefs: defaultPrefs(),
    usage: { month: "", inputTokens: 0, outputTokens: 0, cacheReadTokens: 0 },
  };
}

/** Fill fields added in newer versions so older saved state keeps working. */
function upgrade(saved: Partial<State>): State {
  const base = empty();
  return {
    ...base,
    ...saved,
    connections: { ...base.connections, ...(saved.connections ?? {}) },
    prefs: {
      ...base.prefs,
      ...(saved.prefs ?? {}),
      weekly: { ...base.prefs.weekly, ...(saved.prefs?.weekly ?? {}) },
      nudge: { ...base.prefs.nudge, ...(saved.prefs?.nudge ?? {}) },
    },
    proactive: saved.proactive ?? {},
    sent: saved.sent ?? {},
  };
}

const file = path.join(config.dataDir, "state.json");
let state: State = load();

function load(): State {
  try {
    return upgrade(JSON.parse(fs.readFileSync(file, "utf8")));
  } catch {
    return empty();
  }
}

/** Call once at startup: creates tables and loads state from Postgres when DATABASE_URL is set. */
export async function initStore() {
  if (!sql) return;
  await migrate();
  const saved = await loadState<Partial<State>>();
  if (saved) state = upgrade(saved);
  else saveState(state); // first run with a DB: carry over the JSON file state
}

export function save() {
  // Drop old bookkeeping so the state row stays small.
  const cutoff = Date.now() - 14 * 86_400_000;
  for (const [k, v] of Object.entries(state.sent)) if (v < cutoff) delete state.sent[k];
  for (const [k, v] of Object.entries(state.proactive)) if (v.createdAt < cutoff) delete state.proactive[k];
  saveState(state);
  if (sql) return;
  fs.mkdirSync(config.dataDir, { recursive: true });
  const tmp = `${file}.tmp`;
  fs.writeFileSync(tmp, JSON.stringify(state));
  fs.renameSync(tmp, file);
}

export const store = {
  get: () => state,
  update(fn: (s: State) => void) {
    fn(state);
    save();
  },
  reset() {
    state = empty();
    save();
  },
  /** "Delete all data": forget everything, including connections. */
  wipe() {
    state = empty();
    state.connections = { task: { provider: "none" } };
    save();
  },
};

export type { State };
