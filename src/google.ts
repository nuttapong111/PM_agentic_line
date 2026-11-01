// Google Workspace: OAuth with the owner's company account, Calendar (events, free/busy,
// Meet links), Gmail send. Client ID/secret are entered on the Settings page.
import { config } from "./config.js";
import { store } from "./store.js";
import { encrypt, decrypt, randomId } from "./crypto.js";
import { dateStr } from "./metrics.js";
import { localToDate, localIso } from "./time.js";

const SCOPES = [
  "openid",
  "email",
  "https://www.googleapis.com/auth/calendar.readonly",
  "https://www.googleapis.com/auth/calendar.events",
  "https://www.googleapis.com/auth/gmail.send",
];

export const redirectUri = () => `${config.publicUrl}/oauth/google/callback`;

export class GoogleNotConnected extends Error {
  constructor() { super("ยังไม่ได้เชื่อม Google Workspace ไปที่หน้าตั้งค่า > การเชื่อมต่อ ก่อนนะครับ"); }
}

export function googleConnected(): boolean {
  const g = store.get().connections.google;
  return !!(g?.refreshEnc && g.clientId);
}

/** Step 1: URL the owner opens to sign in with the company account. */
export function authUrl(): string {
  const g = store.get().connections.google;
  if (!g?.clientId) throw new Error("ใส่ Client ID และ Client secret ก่อน");
  const state = randomId(16);
  store.update((s) => (s.oauth = { state, at: Date.now() }));
  const q = new URLSearchParams({
    client_id: g.clientId, redirect_uri: redirectUri(), response_type: "code", scope: SCOPES.join(" "),
    access_type: "offline", prompt: "consent select_account", include_granted_scopes: "true", state,
  });
  return `https://accounts.google.com/o/oauth2/v2/auth?${q}`;
}

/** Step 2: Google redirects back with a code; exchange it for a refresh token. */
export async function handleCallback(code: string, state: string): Promise<string> {
  const s = store.get();
  if (!s.oauth || s.oauth.state !== state || Date.now() - s.oauth.at > 15 * 60_000) throw new Error("ลิงก์เชื่อมหมดอายุ ลองกดเชื่อมใหม่จากหน้าตั้งค่า");
  const g = s.connections.google;
  const secret = decrypt(g?.clientSecretEnc);
  if (!g || !secret) throw new Error("ไม่พบ Client secret");
  const r = await fetch("https://oauth2.googleapis.com/token", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ code, client_id: g.clientId, client_secret: secret, redirect_uri: redirectUri(), grant_type: "authorization_code" }),
  });
  const j: any = await r.json();
  if (!r.ok) throw new Error(`Google ปฏิเสธ: ${j.error_description ?? j.error ?? r.status}`);
  if (!j.refresh_token) throw new Error("Google ไม่ส่ง refresh token กลับมา ลองยกเลิกสิทธิ์แอปใน myaccount.google.com แล้วเชื่อมใหม่");
  access = { token: j.access_token, exp: Date.now() + (j.expires_in - 60) * 1000 };
  const email = j.id_token ? JSON.parse(Buffer.from(j.id_token.split(".")[1], "base64url").toString()).email : undefined;
  store.update((st) => {
    st.oauth = null;
    st.connections.google = { ...st.connections.google!, refreshEnc: encrypt(j.refresh_token), email, connectedAt: Date.now() };
  });
  return email ?? "";
}

export function disconnectGoogle() {
  const g = store.get().connections.google;
  const refresh = decrypt(g?.refreshEnc);
  if (refresh) void fetch(`https://oauth2.googleapis.com/revoke?token=${encodeURIComponent(refresh)}`, { method: "POST" }).catch(() => undefined);
  access = null;
  store.update((s) => { if (s.connections.google) { delete s.connections.google.refreshEnc; delete s.connections.google.email; delete s.connections.google.connectedAt; } });
}

let access: { token: string; exp: number } | null = null;

async function token(): Promise<string> {
  if (access && access.exp > Date.now()) return access.token;
  const g = store.get().connections.google;
  const refresh = decrypt(g?.refreshEnc);
  const secret = decrypt(g?.clientSecretEnc);
  if (!g || !refresh || !secret) throw new GoogleNotConnected();
  const r = await fetch("https://oauth2.googleapis.com/token", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ client_id: g.clientId, client_secret: secret, refresh_token: refresh, grant_type: "refresh_token" }),
  });
  const j: any = await r.json();
  if (!r.ok) throw new Error(`Google sign-in expired (${j.error ?? r.status}): เชื่อม Google ใหม่ที่หน้าตั้งค่า`);
  access = { token: j.access_token, exp: Date.now() + (j.expires_in - 60) * 1000 };
  return access.token;
}

async function api<T>(url: string, init: RequestInit = {}): Promise<T> {
  const r = await fetch(url, { ...init, headers: { Authorization: `Bearer ${await token()}`, "Content-Type": "application/json", ...(init.headers ?? {}) } });
  const text = await r.text();
  if (!r.ok) throw new Error(`Google API ${r.status}: ${text.slice(0, 200)}`);
  return (text ? JSON.parse(text) : {}) as T;
}

// ------------------------------------------------------------------ Calendar

export interface CalEvent {
  id: string; summary: string; start: string; end: string; allDay: boolean;
  meetUrl?: string; htmlLink?: string; attendees: string[]; description?: string;
}

function toEvent(e: any): CalEvent {
  return {
    id: e.id, summary: e.summary ?? "(ไม่มีชื่อ)",
    start: e.start?.dateTime ?? e.start?.date, end: e.end?.dateTime ?? e.end?.date, allDay: !e.start?.dateTime,
    meetUrl: e.hangoutLink ?? e.conferenceData?.entryPoints?.find((p: any) => p.entryPointType === "video")?.uri,
    htmlLink: e.htmlLink, attendees: (e.attendees ?? []).filter((a: any) => !a.resource).map((a: any) => a.email), description: e.description,
  };
}

export async function listEvents(from: Date, to: Date, calendarId = "primary", q?: string): Promise<CalEvent[]> {
  const qs = new URLSearchParams({ timeMin: from.toISOString(), timeMax: to.toISOString(), singleEvents: "true", orderBy: "startTime", maxResults: "50", ...(q ? { q } : {}) });
  const j = await api<{ items?: any[] }>(`https://www.googleapis.com/calendar/v3/calendars/${encodeURIComponent(calendarId)}/events?${qs}`);
  return (j.items ?? []).filter((e) => e.status !== "cancelled").map(toEvent);
}

export async function todaysMeetings(): Promise<CalEvent[]> {
  const today = dateStr();
  return (await listEvents(localToDate(today, "00:00"), localToDate(today, "23:59"))).filter((e) => !e.allDay);
}

/** Busy intervals per attendee. Attendees whose calendar we can't see come back in `unknown`. */
export async function freeBusy(emails: string[], from: Date, to: Date) {
  const j = await api<{ calendars: Record<string, { busy?: { start: string; end: string }[]; errors?: unknown[] }> }>(
    "https://www.googleapis.com/calendar/v3/freeBusy",
    { method: "POST", body: JSON.stringify({ timeMin: from.toISOString(), timeMax: to.toISOString(), timeZone: config.timezone, items: emails.map((id) => ({ id })) }) },
  );
  const busy: Record<string, { start: number; end: number }[]> = {};
  const unknown: string[] = [];
  for (const e of emails) {
    const c = j.calendars?.[e];
    if (!c || c.errors?.length) { unknown.push(e); continue; }
    busy[e] = (c.busy ?? []).map((b) => ({ start: Date.parse(b.start), end: Date.parse(b.end) }));
  }
  return { busy, unknown };
}

export interface Slot { date: string; start: string; end: string; startIso: string; endIso: string; free: number; total: number; missing: string[] }

/** Candidate slots in working hours (09:00–18:00, Mon–Fri), best first. */
export async function findSlots(emails: string[], fromDate: string, days: number, minutes: number): Promise<{ slots: Slot[]; unknown: string[] }> {
  const start = localToDate(fromDate, "00:00");
  const end = new Date(start.getTime() + days * 86_400_000);
  const { busy, unknown } = await freeBusy(emails, start, end);
  const known = Object.keys(busy);
  const slots: Slot[] = [];
  for (let d = 0; d < days; d++) {
    const date = dateStr(new Date(start.getTime() + d * 86_400_000 + 12 * 3600_000));
    const dow = new Date(date + "T00:00:00Z").getUTCDay();
    if (dow === 0 || dow === 6) continue;
    for (let m = 9 * 60; m + minutes <= 18 * 60; m += 30) {
      if (m < 13 * 60 && m + minutes > 12 * 60) continue; // skip lunch
      const s = localToDate(date, `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`);
      const e = new Date(s.getTime() + minutes * 60_000);
      if (s.getTime() < Date.now() + 30 * 60_000) continue;
      const missing = known.filter((who) => busy[who].some((b) => b.start < e.getTime() && b.end > s.getTime()));
      slots.push({
        date, start: hhmm(m), end: hhmm(m + minutes), startIso: localIso(s), endIso: localIso(e),
        free: known.length - missing.length, total: known.length, missing,
      });
    }
  }
  slots.sort((a, b) => a.missing.length - b.missing.length || a.date.localeCompare(b.date) || a.start.localeCompare(b.start));
  return { slots, unknown };
}

const hhmm = (m: number) => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;

export async function createMeeting(o: { summary: string; description?: string; startIso: string; endIso: string; attendees: string[] }): Promise<CalEvent> {
  const body = {
    summary: o.summary, description: o.description,
    start: { dateTime: o.startIso, timeZone: config.timezone }, end: { dateTime: o.endIso, timeZone: config.timezone },
    attendees: o.attendees.map((email) => ({ email })),
    conferenceData: { createRequest: { requestId: randomId(8), conferenceSolutionKey: { type: "hangoutsMeet" } } },
  };
  const e = await api<any>("https://www.googleapis.com/calendar/v3/calendars/primary/events?conferenceDataVersion=1&sendUpdates=all", { method: "POST", body: JSON.stringify(body) });
  return toEvent(e);
}

/** Leave days per person from a shared leave calendar: events whose title or attendees mention them. */
export async function leaveDays(calendarId: string, from: string, to: string, people: { name: string; aliases: string[] }[]) {
  const events = await listEvents(localToDate(from, "00:00"), localToDate(to, "23:59"), calendarId);
  const out: Record<string, string[]> = {};
  for (const p of people) {
    const keys = p.aliases.map((a) => a.toLowerCase()).filter(Boolean);
    for (const e of events) {
      const hay = `${e.summary} ${e.attendees.join(" ")}`.toLowerCase();
      if (!keys.some((k) => hay.includes(k))) continue;
      const s = e.start.slice(0, 10), en = e.allDay ? dateStr(new Date(Date.parse(e.end) - 86_400_000)) : e.end.slice(0, 10);
      for (let d = s; d <= en && d <= to; d = dateStr(new Date(Date.parse(d) + 36 * 3600_000))) {
        if (d >= from) (out[p.name] ??= []).includes(d) || out[p.name].push(d);
      }
    }
  }
  return out;
}

// ------------------------------------------------------------------ Gmail

const b64 = (s: string) => Buffer.from(s, "utf8").toString("base64");

export async function sendEmail(o: { to: string[]; cc?: string[]; subject: string; html: string }): Promise<{ id: string }> {
  const lines = [
    `To: ${o.to.join(", ")}`,
    ...(o.cc?.length ? [`Cc: ${o.cc.join(", ")}`] : []),
    `Subject: =?UTF-8?B?${b64(o.subject)}?=`,
    "MIME-Version: 1.0",
    "Content-Type: text/html; charset=UTF-8",
    "Content-Transfer-Encoding: base64",
    "",
    b64(o.html).replace(/.{76}/g, "$&\r\n"),
  ];
  const raw = Buffer.from(lines.join("\r\n"), "utf8").toString("base64url");
  return api<{ id: string }>("https://gmail.googleapis.com/gmail/v1/users/me/messages/send", { method: "POST", body: JSON.stringify({ raw }) });
}

export async function googleWhoami(): Promise<string | undefined> {
  const j = await api<{ email?: string }>("https://openidconnect.googleapis.com/v1/userinfo");
  return j.email;
}
