// Picks the task provider from Settings (or env on first run) and exposes it as `tasks`.
// Call resetProvider() after Settings change; the next call builds the new one.
import { config } from "../config.js";
import { store } from "../store.js";
import { decrypt } from "../crypto.js";
import { PlaneClient } from "../plane.js";
import { BuiltinProvider } from "./builtin.js";
import { sql, setWorkspace } from "../db.js";
import type { TaskProvider } from "./types.js";

export class NotConnectedError extends Error {
  constructor() {
    super("ยังไม่ได้เชื่อมระบบจัดการงาน ไปที่หน้าตั้งค่า > การเชื่อมต่อ ก่อนนะครับ");
  }
}

/** Placeholder when nothing is connected: reads return empty, writes explain what to do. */
class NoProvider implements TaskProvider {
  readonly kind = "none" as const;
  readonly workspaceKey = "none";
  private fail = async (): Promise<never> => { throw new NotConnectedError(); };
  me = this.fail;
  allProjects = async () => [];
  projects = async () => [];
  states = async () => [];
  members = async () => [];
  labels = async () => [];
  cycles = async () => [];
  modules = async () => [];
  cycleLinks = async () => [];
  moduleItemIds = async () => [];
  workItems = async () => [];
  workItemByKey = this.fail;
  createWorkItem = this.fail;
  updateWorkItem = this.fail;
  addToCycle = this.fail;
  addComment = this.fail;
  itemUrl = () => "";
  keyUrl = () => "";
  projectUrl = () => "";
}

export function planeFrom(baseUrl: string, workspace: string, token: string, follow: string[] = []): PlaneClient {
  return new PlaneClient(baseUrl.replace(/\/$/, ""), token, workspace, fetch, follow);
}

const looksSet = (v: string) => v && !/^PASTE_HERE$/i.test(v);

function build(): TaskProvider {
  const t = store.get().connections.task;
  if (t.provider === "plane" && t.plane) {
    const token = decrypt(t.plane.tokenEnc);
    if (token) return planeFrom(t.plane.baseUrl, t.plane.workspace, token, t.plane.projects);
  }
  if (t.provider === "builtin" && sql) return new BuiltinProvider();
  if (t.provider === "env" && looksSet(config.plane.baseUrl) && looksSet(config.plane.apiKey) && looksSet(config.plane.workspace)) {
    return new PlaneClient();
  }
  return new NoProvider();
}

let current: TaskProvider | null = null;

let backfilled = false;
export function provider(): TaskProvider {
  if (!current) {
    current = build();
    setWorkspace(current.workspaceKey);
    if (sql && !backfilled && current.kind !== "none") {
      backfilled = true; // rows written before workspaces were tracked belong to the first one
      const ws = current.workspaceKey;
      void Promise.all([
        sql`update activity_log set ws = ${ws} where ws is null`,
        sql`update metrics_daily set ws = ${ws} where ws is null`,
      ]).catch((e) => console.error("ws backfill failed", e?.message ?? e));
    }
  }
  return current;
}

export function resetProvider() {
  current = null;
}

/** Always delegates to the provider currently configured. */
export const tasks: TaskProvider = new Proxy({} as TaskProvider, {
  get(_t, prop) {
    const p = provider() as unknown as Record<string | symbol, unknown>;
    const v = p[prop];
    return typeof v === "function" ? (v as Function).bind(p) : v;
  },
});
