// Settings page API: connections (Plane / built-in, Claude, Google, Notion), followed projects,
// nicknames, notification times, data export and delete. Secrets are stored encrypted and never
// sent back to the browser (only the last 4 characters).
import type { Hono, Context } from "hono";
import Anthropic from "@anthropic-ai/sdk";
import { config } from "./config.js";
import { store, type Prefs } from "./store.js";
import { encrypt, decrypt, last4 } from "./crypto.js";
import { provider, resetProvider, planeFrom } from "./providers/index.js";
import { createBuiltinProject, migrateBuiltin } from "./providers/builtin.js";
import { sql, dataSummary, exportWorkspace, deleteWorkspace, workspace } from "./db.js";
import { authUrl, handleCallback, disconnectGoogle, redirectUri } from "./google.js";
import { notionWhoami } from "./notion.js";
import { memberName } from "./metrics.js";

type Auth = (c: Context) => boolean;

const hostOf = (u: string) => { try { return new URL(u).host; } catch { return u; } };
const cleanUrl = (u: string) => {
  const s = String(u ?? "").trim().replace(/\/+$/, "");
  return /^https?:\/\//i.test(s) ? s : s ? `https://${s}` : "";
};
const envPlaneSet = () => !!(config.plane.baseUrl && config.plane.apiKey && config.plane.workspace && !/^PASTE_HERE$/i.test(config.plane.apiKey));

/** The token to use for a Plane test/save: a new one, or the saved one only when server + workspace are unchanged. */
function planeToken(baseUrl: string, ws: string, token?: string): string {
  if (token?.trim()) return token.trim();
  const saved = store.get().connections.task.plane;
  if (saved && saved.baseUrl === baseUrl && saved.workspace === ws) {
    const t = decrypt(saved.tokenEnc);
    if (t) return t;
  }
  if (envPlaneSet() && config.plane.baseUrl === baseUrl && config.plane.workspace === ws) return config.plane.apiKey;
  throw new Error("เปลี่ยน Server หรือ Workspace ต้องใส่ API token ใหม่ทุกครั้ง (token เดิมจะไม่ถูกส่งไปที่ server ใหม่)");
}

async function usageThisMonth(): Promise<number | null> {
  if (!sql) return null;
  const [r] = await sql<{ n: number }[]>`select count(*)::int as n from usage_log where at >= date_trunc('month', now())`;
  return r.n;
}

async function snapshot() {
  const s = store.get();
  const t = s.connections.task;
  const p = provider();
  const projects = await p.allProjects().catch(() => []);
  const followed = new Set((await p.projects().catch(() => [])).map((x) => x.id));
  const members = new Map<string, { name: string; email?: string }>();
  for (const pr of projects.filter((x) => followed.has(x.id)).slice(0, 5)) {
    for (const m of await p.members(pr.id).catch(() => [])) members.set(m.id, { name: memberName(m), email: m.email });
  }
  const claudeSaved = s.connections.claude;
  const envKey = process.env.ANTHROPIC_API_KEY && !/^PASTE_HERE$/i.test(process.env.ANTHROPIC_API_KEY) ? process.env.ANTHROPIC_API_KEY : "";
  return {
    db: !!sql,
    task: {
      provider: p.kind === "plane" && t.provider === "env" ? "env" : t.provider,
      active: p.kind,
      plane: t.plane ? { baseUrl: t.plane.baseUrl, host: hostOf(t.plane.baseUrl), workspace: t.plane.workspace, tokenLast4: t.plane.tokenLast4, account: t.plane.account, checkedAt: t.plane.checkedAt, ok: t.plane.ok }
        : envPlaneSet() ? { baseUrl: config.plane.baseUrl, host: hostOf(config.plane.baseUrl), workspace: config.plane.workspace, tokenLast4: last4(config.plane.apiKey), fromEnv: true } : null,
    },
    claude: claudeSaved ? { source: "settings", last4: claudeSaved.last4, savedAt: claudeSaved.savedAt } : envKey ? { source: "env", last4: last4(envKey) } : { source: "none" },
    model: config.claude.model,
    calls: await usageThisMonth(),
    usage: s.usage,
    google: s.connections.google ? { clientId: s.connections.google.clientId, hasSecret: !!s.connections.google.clientSecretEnc, email: s.connections.google.email, connectedAt: s.connections.google.connectedAt, connected: !!s.connections.google.refreshEnc } : null,
    googleRedirectUri: redirectUri(),
    notion: s.connections.notion ? { last4: s.connections.notion.last4, botName: s.connections.notion.botName, savedAt: s.connections.notion.savedAt } : null,
    line: { ownerSet: !!config.line.ownerUserId },
    prefs: s.prefs,
    projects: projects.map((x) => ({ id: x.id, name: x.name, identifier: x.identifier, followed: followed.has(x.id) })),
    members: [...members.values()],
    data: { current: workspace(), workspaces: await dataSummary().catch(() => []) },
  };
}

function afterSwitch() {
  resetProvider();
  const p = provider();
  void p.projects().then((ps) => {
    if (!ps.some((x) => x.id === store.get().currentProjectId)) store.update((s) => { s.currentProjectId = ps.length === 1 ? ps[0].id : null; });
  }).catch(() => undefined);
  store.update((s) => { s.conversation = { messages: [], lastAt: 0 }; s.pending = null; });
}

const PREF_KEYS: (keyof Prefs)[] = ["nicknames", "digestTime", "workdaysOnly", "quietFrom", "quietTo", "staleDays", "bugLabel", "weekly", "meetingPrepMinutes", "milestoneAlertDays", "nudge", "leaveCalendarId", "notionParentPageId", "manDayHours"];
const isTime = (v: unknown) => typeof v === "string" && /^([01]\d|2[0-3]):[0-5]\d$/.test(v);
const emailList = (v: unknown) => (Array.isArray(v) ? v : String(v ?? "").split(/[,\s;]+/)).map((x) => String(x).trim()).filter((x) => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(x));

function cleanPrefs(input: any): Partial<Prefs> {
  const out: any = {};
  for (const k of PREF_KEYS) if (k in input) out[k] = input[k];
  for (const k of ["digestTime", "quietFrom", "quietTo"] as const) if (k in out && !isTime(out[k])) throw new Error(`${k} ต้องเป็นเวลา HH:MM`);
  if (out.nicknames) {
    out.nicknames = Object.fromEntries(Object.entries(out.nicknames as Record<string, string>).map(([a, b]) => [String(a).trim(), String(b).trim()]).filter(([a, b]) => a && b));
  }
  if (out.weekly) {
    const w = out.weekly;
    if (w.time && !isTime(w.time)) throw new Error("เวลารายงานต้องเป็น HH:MM");
    out.weekly = { enabled: !!w.enabled, day: Number(w.day) || 5, time: w.time ?? "16:00", to: emailList(w.to), cc: emailList(w.cc) };
  }
  if (out.nudge) {
    if (out.nudge.time && !isTime(out.nudge.time)) throw new Error("เวลาทวงอัปเดตต้องเป็น HH:MM");
    out.nudge = { enabled: !!out.nudge.enabled, day: Number(out.nudge.day) || 4, time: out.nudge.time ?? "09:00" };
  }
  if ("staleDays" in out) out.staleDays = Math.max(1, Math.min(60, Number(out.staleDays) || 5));
  if ("meetingPrepMinutes" in out) out.meetingPrepMinutes = Math.max(0, Math.min(120, Number(out.meetingPrepMinutes) || 0));
  if ("manDayHours" in out) out.manDayHours = Math.max(1, Math.min(24, Number(out.manDayHours) || 8));
  if ("milestoneAlertDays" in out) out.milestoneAlertDays = (Array.isArray(out.milestoneAlertDays) ? out.milestoneAlertDays : String(out.milestoneAlertDays).split(/[,\s]+/)).map(Number).filter((n: number) => n > 0 && n < 120);
  if ("workdaysOnly" in out) out.workdaysOnly = !!out.workdaysOnly;
  return out;
}

function csv(rows: Record<string, unknown>[]): string {
  if (!rows.length) return "﻿";
  const cols = Object.keys(rows[0]);
  const esc = (v: unknown) => {
    const s = v === null || v === undefined ? "" : typeof v === "object" && !(v instanceof Date) ? JSON.stringify(v) : v instanceof Date ? v.toISOString() : String(v);
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return "﻿" + [cols.join(","), ...rows.map((r) => cols.map((c) => esc(r[c])).join(","))].join("\n");
}

export function registerSettings(app: Hono, auth: Auth, settingsHtml: () => string) {
  const guard = (c: Context) => (auth(c) ? null : c.json({ error: "unauthorized" }, 401));
  const body = async (c: Context) => (await c.req.json().catch(() => ({}))) as any;
  const fail = (c: Context, e: unknown, status: 400 | 502 = 400) => c.json({ error: e instanceof Error ? e.message : String(e) }, status);

  app.get("/settings", (c) => {
    if (!auth(c)) return c.text("ลิงก์ไม่ถูกต้อง พิมพ์ \"ตั้งค่า\" ในแชตบอทเพื่อรับลิงก์ใหม่", 401);
    return c.html(settingsHtml(), 200, { "Cache-Control": "no-store", "Referrer-Policy": "no-referrer" });
  });

  app.get("/api/settings", async (c) => guard(c) ?? c.json(await snapshot()));

  // ---------------- task tool
  app.post("/api/settings/plane/test", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    try {
      const baseUrl = cleanUrl(b.baseUrl), ws = String(b.workspace ?? "").trim();
      if (!baseUrl || !ws) throw new Error("ใส่ Server URL และ Workspace slug");
      const client = planeFrom(baseUrl, ws, planeToken(baseUrl, ws, b.token));
      const [me, projects] = await Promise.all([client.me(), client.allProjects()]);
      return c.json({ ok: true, account: { name: memberName(me), email: me.email }, projects: projects.map((p) => ({ id: p.id, name: p.name, identifier: p.identifier })) });
    } catch (e) {
      return fail(c, e);
    }
  });

  app.post("/api/settings/plane/save", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    try {
      const baseUrl = cleanUrl(b.baseUrl), ws = String(b.workspace ?? "").trim();
      const token = planeToken(baseUrl, ws, b.token);
      const client = planeFrom(baseUrl, ws, token);
      const me = await client.me();
      const projects: string[] = Array.isArray(b.projects) ? b.projects.map(String) : [];
      store.update((s) => {
        s.connections.task = {
          provider: "plane",
          plane: { baseUrl, workspace: ws, projects, tokenEnc: encrypt(token), tokenLast4: last4(token), account: { name: memberName(me), email: me.email }, checkedAt: Date.now(), ok: true },
        };
      });
      afterSwitch();
      return c.json({ ok: true });
    } catch (e) {
      return fail(c, e);
    }
  });

  app.post("/api/settings/projects", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    const ids: string[] = Array.isArray(b.projects) ? b.projects.map(String) : [];
    const t = store.get().connections.task;
    if (t.provider === "builtin") return c.json({ ok: true }); // built-in follows all its projects
    if (!t.plane && envPlaneSet()) {
      // First change from the env setup: keep using the env server/token, now stored here.
      store.update((s) => {
        s.connections.task = { provider: "plane", plane: { baseUrl: config.plane.baseUrl, workspace: config.plane.workspace, projects: ids, tokenEnc: encrypt(config.plane.apiKey), tokenLast4: last4(config.plane.apiKey) } };
      });
    } else if (t.plane) {
      store.update((s) => (s.connections.task.plane!.projects = ids));
    }
    afterSwitch();
    return c.json({ ok: true });
  });

  app.post("/api/settings/plane/check", async (c) => {
    const g = guard(c); if (g) return g;
    try {
      const me = await provider().me();
      store.update((s) => { if (s.connections.task.plane) Object.assign(s.connections.task.plane, { checkedAt: Date.now(), ok: true, account: { name: memberName(me), email: me.email } }); });
      return c.json({ ok: true, account: memberName(me) });
    } catch (e) {
      store.update((s) => { if (s.connections.task.plane) Object.assign(s.connections.task.plane, { checkedAt: Date.now(), ok: false }); });
      return fail(c, e, 502);
    }
  });

  app.post("/api/settings/provider", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    const want = b.provider;
    if (want === "builtin") {
      if (!sql) return fail(c, new Error("ระบบในตัวต้องมีฐานข้อมูล PostgreSQL"));
      await migrateBuiltin();
      store.update((s) => (s.connections.task = { provider: "builtin" }));
    } else if (want === "none") {
      store.update((s) => (s.connections.task = { provider: "none" }));
    } else return fail(c, new Error("provider ไม่ถูกต้อง"));
    afterSwitch();
    return c.json({ ok: true });
  });

  app.post("/api/settings/builtin/project", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    try {
      const p = await createBuiltinProject(String(b.name ?? "").trim() || "โปรเจกต์ใหม่", String(b.identifier ?? ""));
      afterSwitch();
      return c.json({ ok: true, project: p });
    } catch (e) {
      return fail(c, e);
    }
  });

  // ---------------- Claude
  app.post("/api/settings/claude", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    const key = String(b.key ?? "").trim() || decrypt(store.get().connections.claude?.keyEnc) || process.env.ANTHROPIC_API_KEY || "";
    try {
      if (!key) throw new Error("ใส่ API key");
      await new Anthropic({ apiKey: key }).models.retrieve(config.claude.model);
      if (b.key) store.update((s) => (s.connections.claude = { keyEnc: encrypt(key), last4: last4(key), savedAt: Date.now() }));
      return c.json({ ok: true, model: config.claude.model });
    } catch (e) {
      return fail(c, new Error(`ใช้ key นี้ไม่ได้: ${e instanceof Error ? e.message.slice(0, 160) : e}`));
    }
  });
  app.post("/api/settings/claude/remove", (c) => guard(c) ?? (store.update((s) => delete s.connections.claude), c.json({ ok: true })));

  // ---------------- Notion
  app.post("/api/settings/notion", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    const token = String(b.token ?? "").trim();
    try {
      if (!token) throw new Error("ใส่ Internal integration token");
      const name = await notionWhoami(token);
      store.update((s) => (s.connections.notion = { tokenEnc: encrypt(token), last4: last4(token), botName: name, savedAt: Date.now() }));
      return c.json({ ok: true, name });
    } catch (e) {
      return fail(c, e);
    }
  });
  app.post("/api/settings/notion/remove", (c) => guard(c) ?? (store.update((s) => delete s.connections.notion), c.json({ ok: true })));

  // ---------------- Google
  app.post("/api/settings/google", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    try {
      const clientId = String(b.clientId ?? "").trim();
      const secret = String(b.clientSecret ?? "").trim();
      const cur = store.get().connections.google;
      if (!clientId) throw new Error("ใส่ Client ID");
      if (!secret && !(cur?.clientSecretEnc && cur.clientId === clientId)) throw new Error("ใส่ Client secret");
      store.update((s) => {
        s.connections.google = clientId === cur?.clientId ? { ...cur!, ...(secret ? { clientSecretEnc: encrypt(secret) } : {}) } : { clientId, clientSecretEnc: encrypt(secret) };
      });
      return c.json({ ok: true, url: authUrl() });
    } catch (e) {
      return fail(c, e);
    }
  });
  app.get("/api/settings/google/auth", (c) => {
    const g = guard(c); if (g) return g;
    try { return c.json({ url: authUrl() }); } catch (e) { return fail(c, e); }
  });
  app.post("/api/settings/google/remove", (c) => {
    const g = guard(c); if (g) return g;
    disconnectGoogle();
    return c.json({ ok: true });
  });
  app.get("/oauth/google/callback", async (c) => {
    const back = `${config.publicUrl}/settings?t=${encodeURIComponent(config.dashboardToken)}`;
    const page = (msg: string, ok: boolean) => c.html(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Google</title>
<body style="font-family:system-ui,sans-serif;padding:32px;max-width:520px;margin:auto;color:#18202B"><h2 style="color:${ok ? "#1B6B34" : "#A2231B"}">${ok ? "เชื่อม Google แล้ว" : "เชื่อมไม่สำเร็จ"}</h2><p>${msg.replace(/</g, "&lt;")}</p><p><a href="${back}">กลับไปหน้าตั้งค่า</a></p></body>`, ok ? 200 : 400, { "Referrer-Policy": "no-referrer" });
    if (c.req.query("error")) return page(String(c.req.query("error")), false);
    try {
      const email = await handleCallback(String(c.req.query("code") ?? ""), String(c.req.query("state") ?? ""));
      return page(`เชื่อมในชื่อ ${email}`, true);
    } catch (e) {
      return page(e instanceof Error ? e.message : String(e), false);
    }
  });

  // ---------------- preferences
  app.post("/api/settings/prefs", async (c) => {
    const g = guard(c); if (g) return g;
    try {
      const patch = cleanPrefs(await body(c));
      store.update((s) => (s.prefs = { ...s.prefs, ...patch }));
      return c.json({ ok: true, prefs: store.get().prefs });
    } catch (e) {
      return fail(c, e);
    }
  });

  // ---------------- data
  app.get("/api/settings/export", async (c) => {
    const g = guard(c); if (g) return g;
    const ws = c.req.query("ws") || workspace();
    const data = await exportWorkspace(ws);
    const name = `pm-assistant-${ws.replace(/[^a-z0-9]+/gi, "-")}-${new Date().toISOString().slice(0, 10)}`;
    const table = c.req.query("table") as keyof typeof data | undefined;
    if (table && table in data) {
      return c.body(csv(data[table] as Record<string, unknown>[]), 200, { "Content-Type": "text/csv; charset=utf-8", "Content-Disposition": `attachment; filename="${name}-${table}.csv"` });
    }
    return c.body(JSON.stringify({ workspace: ws, exportedAt: new Date().toISOString(), ...data }, null, 2), 200, { "Content-Type": "application/json", "Content-Disposition": `attachment; filename="${name}.json"` });
  });

  app.post("/api/settings/delete", async (c) => {
    const g = guard(c); if (g) return g;
    const b = await body(c);
    try {
      if (b.scope === "all") {
        if (b.confirm !== "ลบทั้งหมด") throw new Error('พิมพ์ "ลบทั้งหมด" เพื่อยืนยัน');
        await deleteWorkspace(null);
        store.wipe();
        await migrateBuiltin();
        resetProvider();
        return c.json({ ok: true });
      }
      const ws = String(b.ws ?? "");
      if (!ws) throw new Error("ไม่ได้ระบุ workspace");
      await deleteWorkspace(ws);
      return c.json({ ok: true });
    } catch (e) {
      return fail(c, e);
    }
  });
}
