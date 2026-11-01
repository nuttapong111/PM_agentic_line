// Everything the bot sends on its own, checked once a minute against the times in Settings:
// morning digest, reminders, milestone / contract-deadline alerts, meeting prep, minutes nudge,
// weekly nudges for stale work, weekly report draft, retro when a sprint ends.
// Each alert has a dedupe key in state.sent so a restart never sends it twice.
import type { messagingApi } from "@line/bot-sdk";
import { config } from "./config.js";
import { store } from "./store.js";
import { plane } from "./resolve.js";
import { projectMetrics, dateStr, thaiDate } from "./metrics.js";
import { buildDigest } from "./digest.js";
import { dueReminders, updateRecord } from "./records.js";
import { milestonesFor, retro, meetingPrep } from "./insights.js";
import { milestoneCard, prepCard, nudgePreview, addProactive, weeklyReportProactive, retroProactive } from "./features.js";
import { googleConnected, listEvents } from "./google.js";
import { notionConnected, searchPages } from "./notion.js";
import { confirmCard, infoCard, quickText } from "./flex.js";
import { hm, weekday, inQuietHours } from "./time.js";
import { snapshotMetrics, logActivity } from "./db.js";

type Push = (messages: messagingApi.Message[]) => Promise<void>;

function once(key: string): boolean {
  if (store.get().sent[key]) return false;
  store.update((s) => (s.sent[key] = Date.now()));
  return true;
}

const workday = () => { const d = weekday(); return d >= 1 && d <= 5; };

export function startScheduler(push: Push, schedule: (expr: string, fn: () => void) => void) {
  let running = false;
  schedule("* * * * *", () => {
    if (running || !config.line.ownerUserId) return;
    running = true;
    tick(push).catch((e) => console.error("scheduler tick failed", e)).finally(() => (running = false));
  });
}

async function tick(push: Push) {
  const p = store.get().prefs;
  const now = hm();
  const today = dateStr();
  const quiet = inQuietHours(p.quietFrom, p.quietTo, now);
  const safe = async (name: string, fn: () => Promise<void>) => {
    try { await fn(); } catch (e) { console.error(`job ${name} failed`, e instanceof Error ? e.message : e); }
  };

  // Reminders the owner set: always delivered, even in quiet hours.
  await safe("reminders", () => fireReminders(push));

  if (now === p.digestTime && (!p.workdaysOnly || workday()) && once(`digest:${today}`)) {
    await safe("digest", async () => push([await buildDigest()]));
  }

  if (quiet) return;

  // 08:31 on workdays: milestone + contract deadline alerts (after the digest).
  if (now === "08:31" && workday()) await safe("deadlines", () => deadlineAlerts(push, today));

  if (p.meetingPrepMinutes > 0 && googleConnected()) await safe("meeting prep", () => prepAlerts(push, p.meetingPrepMinutes));

  if (googleConnected() && notionConnected() && Number(now.slice(3)) % 15 === 0) await safe("minutes", () => minutesNudge(push));

  if (p.nudge.enabled && weekday() === p.nudge.day && now === p.nudge.time && once(`nudge:${today}`)) await safe("nudge", () => nudges(push));

  if (p.weekly.enabled && weekday() === p.weekly.day && now === p.weekly.time && once(`weekly:${today}`)) {
    await safe("weekly", async () => push([(await weeklyReportProactive()).msg]));
  }

  if (now === "15:00") await safe("retro", () => retroAlerts(push, today));
}

async function fireReminders(push: Push) {
  for (const r of await dueReminders()) {
    await updateRecord(r.id, { status: "sent" });
    const rows: { label: string; value: string; color?: "red" | "amber" }[] = [];
    let url: string | undefined;
    if (r.data.itemKey) {
      try {
        const it = await plane.workItemByKey(r.data.itemKey);
        const states = await plane.states(it.projectId);
        const members = await plane.members(it.projectId);
        const st = states.find((s) => s.id === it.stateId)?.name ?? "?";
        const who = it.assigneeIds.map((id) => members.find((m) => m.id === id)?.display_name).filter(Boolean).join(", ") || "ไม่มีเจ้าของ";
        const days = Math.floor((Date.now() - Date.parse(it.updatedAt)) / 86_400_000);
        rows.push({ label: "งาน", value: `${it.key} ${it.name}` }, { label: "ตอนนี้", value: `${st}${days ? ` มา ${days} วัน` : ""} · ${who}`, color: /block/i.test(st) ? "red" : undefined });
        url = plane.itemUrl(it);
      } catch { rows.push({ label: "งาน", value: r.data.itemKey }); }
    }
    await push([infoCard({
      kicker: `เตือนความจำ · ${thaiDate(dateStr())} ${hm()}`, title: r.data.text, tone: "warn", rows,
      buttons: [
        ...(url ? [{ label: "ดูงาน", url, primary: true }] : []),
        ...(r.data.itemKey ? [{ label: "คอมเมนต์", data: `cmd:คอมเมนต์ใน ${r.data.itemKey} ว่า ขออัปเดตความคืบหน้าหน่อยครับ` }] : []),
        { label: "เลื่อน 1 วัน", data: `snooze:${r.id}` },
        { label: "เสร็จแล้ว", data: `rdone:${r.id}` },
      ],
    })]);
  }
}

async function deadlineAlerts(push: Push, today: string) {
  const alertDays = store.get().prefs.milestoneAlertDays;
  const out: messagingApi.Message[] = [];
  for (const project of await plane.projects()) {
    // payment milestones
    for (const m of await milestonesFor(project).catch(() => [])) {
      if ((alertDays.includes(m.daysLeft) || m.daysLeft === 1) && once(`ms:${m.id}:${m.daysLeft}`)) out.push(milestoneCard(project.name, m));
    }
    // modules whose contract date is close and progress is behind
    const metrics = await projectMetrics(plane, project).catch(() => null);
    if (!metrics) continue;
    await snapshotMetrics(metrics).catch(() => undefined);
    for (const mod of metrics.modules) {
      if (!mod.target || mod.status === "done" || mod.status === "on_track") continue;
      const left = Math.round((Date.parse(mod.target) - Date.parse(today)) / 86_400_000);
      if (left < 0 || left > 7) continue;
      if (!once(`mod:${project.id}:${mod.name}:${mod.target}:${left <= 3 ? 3 : 7}`)) continue;
      out.push(infoCard({
        kicker: "เตือนกำหนดส่งตามสัญญา", title: "Module เสี่ยงไม่ทัน", tone: "danger",
        rows: [
          { label: "Module", value: `${mod.name} (${project.name})` },
          { label: "กำหนดส่ง", value: `${thaiDate(mod.target)} (อีก ${left} วัน)`, color: "red" },
        ],
        bars: [{ label: "ความคืบหน้า", percent: mod.percent, tone: "red" }],
        note: `เหลือ ${mod.total - mod.done} งาน${metrics.counts.blocked ? ` · Blocked ในโปรเจกต์ ${metrics.counts.blocked}` : ""}`,
        buttons: [
          { label: "ดูงานที่เหลือ", data: `cmd:งานที่ยังไม่เสร็จใน module ${mod.name} ของ ${project.name}`.slice(0, 300), primary: true },
          { label: "ร่างเมลแจ้งลูกค้า", data: `cmd:ร่างอีเมลแจ้งลูกค้าว่า module ${mod.name} อาจส่งไม่ทัน ${thaiDate(mod.target)}`.slice(0, 300) },
        ],
      }));
    }
  }
  for (let i = 0; i < out.length; i += 5) await push(out.slice(i, i + 5));
}

async function prepAlerts(push: Push, minutes: number) {
  const from = new Date(Date.now() + (minutes - 1) * 60_000);
  const to = new Date(Date.now() + (minutes + 1) * 60_000);
  const evs = (await listEvents(from, new Date(to.getTime() + 60 * 60_000))).filter((e) => !e.allDay && e.attendees.length > 1);
  for (const e of evs) {
    const start = Date.parse(e.start);
    if (start < from.getTime() || start >= to.getTime()) continue;
    if (!once(`prep:${e.id}:${e.start}`)) continue;
    await push([prepCard(await meetingPrep(e))]);
  }
}

/** A meeting with attendees ended 30–90 minutes ago and Notion has a page about it → offer to send the minute. */
async function minutesNudge(push: Push) {
  const evs = await listEvents(new Date(Date.now() - 3 * 3600_000), new Date());
  for (const e of evs) {
    if (e.allDay || e.attendees.length < 2) continue;
    const ended = Date.now() - Date.parse(e.end);
    if (ended < 30 * 60_000 || ended > 90 * 60_000) continue;
    if (store.get().sent[`minutes:${e.id}`]) continue;
    const pages = (await searchPages(e.summary, 3)).filter((p) => Date.parse(p.editedAt) >= Date.parse(e.start) - 3600_000);
    if (!pages.length) continue;
    once(`minutes:${e.id}`);
    await push([quickText(`ประชุม "${e.summary}" จบแล้ว และพบจดประชุมใน Notion "${pages[0].title}"\nให้ร่างอีเมล minute ถึงผู้เข้าประชุม ${e.attendees.length} คนไหมครับ`, [
      { label: "ร่าง minute", data: `cmd:ร่างอีเมล minute ของประชุม "${e.summary}" วันนี้ จากหน้า Notion id ${pages[0].id} ส่งถึงผู้เข้าประชุมทุกคน`.slice(0, 300), displayText: "ร่าง minute" },
      { label: "ไม่ต้อง", data: "cmd:ไม่ต้องส่ง minute ครับ", displayText: "ไม่ต้อง" },
    ])]);
  }
}

async function nudges(push: Push) {
  for (const project of await plane.projects()) {
    try {
      const pv = await nudgePreview(project.id);
      const id = addProactive("nudge_stale", pv);
      await push([confirmCard(pv, id, true)]);
    } catch { /* nothing stale */ }
  }
}

async function retroAlerts(push: Push, today: string) {
  for (const project of await plane.projects()) {
    const cycles = await plane.cycles(project.id).catch(() => []);
    for (const c of cycles) {
      if (c.end_date?.slice(0, 10) !== today || !once(`retro:${c.id}`)) continue;
      const r = await retro(project, c);
      if (r) {
        await push([await retroProactive(r)]);
        logActivity("retro", `ร่าง retro ${c.name}`, project.id);
      }
    }
  }
}

