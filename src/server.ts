// HTTP server: LINE webhook, Plane webhook, dashboard + settings pages and APIs, scheduler.
import { Hono } from "hono";
import { serve } from "@hono/node-server";
import cron from "node-cron";
import crypto from "node:crypto";
import { messagingApi, validateSignature, webhook } from "@line/bot-sdk";
import type { Context } from "hono";
import { config } from "./config.js";
import { handleText, handlePostback, handleImage } from "./agent.js";
import { buildDigest } from "./digest.js";
import { plane, dashboardUrl, type Preview } from "./tools.js";
import { projectMetrics } from "./metrics.js";
import { projectPicker, textMsg } from "./flex.js";
import { store, initStore } from "./store.js";
import { snapshotMetrics, history, sql } from "./db.js";
import { ensureRichMenu } from "./richmenu.js";
import { dashboardHtml, settingsHtml } from "./assets.js";
import { registerSettings } from "./settings.js";
import { startScheduler } from "./scheduler.js";
import { migrateBuiltin } from "./providers/builtin.js";
import { provider } from "./providers/index.js";
import { listRecords } from "./records.js";
import { milestonesFor, budgetStatus } from "./insights.js";
import { settingsUrl } from "./features.js";

const line = new messagingApi.MessagingApiClient({ channelAccessToken: config.line.accessToken });
const blob = new messagingApi.MessagingApiBlobClient({ channelAccessToken: config.line.accessToken });
const app = new Hono();

app.get("/", (c) => c.text("LINE PM Assistant is running"));
app.get("/healthz", (c) => c.json({ ok: true }));

// ------------------------------------------------------------- LINE webhook

app.post("/webhook", async (c) => {
  const body = await c.req.text();
  const sig = c.req.header("x-line-signature") ?? "";
  if (!validateSignature(body, config.line.channelSecret, sig)) return c.text("bad signature", 401);
  const payload = JSON.parse(body) as webhook.CallbackRequest;
  // Answer LINE right away; do the work in the background.
  for (const ev of payload.events) void handleEvent(ev).catch((e) => console.error("event failed", e));
  return c.text("ok");
});

let queue: Promise<unknown> = Promise.resolve();
/** One event at a time, so the conversation state never interleaves. */
function serial<T>(fn: () => Promise<T>): Promise<T> {
  const next = queue.then(fn, fn);
  queue = next.catch(() => undefined);
  return next;
}

async function handleEvent(ev: webhook.Event) {
  const userId = ev.source?.type === "user" ? ev.source.userId : undefined;
  const replyToken = "replyToken" in ev ? (ev.replyToken as string | undefined) : undefined;
  if (!userId) return; // groups come later

  if (!config.line.ownerUserId) {
    if (replyToken) await line.replyMessage({ replyToken, messages: [textMsg(`ตั้งค่า OWNER_LINE_USER_ID เป็น\n${userId}\nแล้ว redeploy เพื่อเริ่มใช้งาน`)] });
    return;
  }
  if (userId !== config.line.ownerUserId) {
    if (replyToken) await line.replyMessage({ replyToken, messages: [textMsg("บอทนี้ใช้งานส่วนตัวครับ")] });
    return;
  }

  const started = Date.now();
  let messages: messagingApi.Message[] | null = null;

  if (ev.type === "follow") {
    messages = [textMsg(`สวัสดีครับ ผมคือ PM Assistant ถามสถานะงาน สั่งสร้างหรือแก้งานได้ด้วยข้อความ ส่งรูปไวท์บอร์ดให้แปลงเป็นงานก็ได้ หรือใช้เมนูด้านล่าง\n\nเชื่อมเครื่องมือต่าง ๆ ได้ที่หน้าตั้งค่า พิมพ์ "ตั้งค่า" เพื่อรับลิงก์`)];
    const projects = await plane.projects().catch(() => []);
    if (projects.length > 1 && !store.get().currentProjectId) messages.push(projectPicker("เริ่มจากเลือกโปรเจกต์ปัจจุบันก่อนครับ", projects));
  } else if (ev.type === "message" && ev.message.type === "text") {
    const text = ev.message.text;
    await line.showLoadingAnimation({ chatId: userId, loadingSeconds: 30 }).catch(() => undefined);
    messages = (await serial(() => handleText(text))).messages;
  } else if (ev.type === "postback") {
    const data = ev.postback.data;
    await line.showLoadingAnimation({ chatId: userId, loadingSeconds: 20 }).catch(() => undefined);
    messages = await serial(() => handleMenu(data));
  } else if (ev.type === "message" && ev.message.type === "image") {
    const id = ev.message.id;
    await line.showLoadingAnimation({ chatId: userId, loadingSeconds: 45 }).catch(() => undefined);
    messages = await serial(async () => {
      const img = await readImage(id);
      if (!img) return [textMsg("เปิดรูปนี้ไม่ได้ครับ ลองส่งเป็นรูปภาพ (JPEG/PNG) อีกครั้ง")];
      return (await handleImage(img.data, img.type)).messages;
    });
  } else if (ev.type === "message") {
    messages = [textMsg("ตอนนี้รับข้อความตัวอักษรและรูปภาพครับ")];
  }
  if (!messages?.length) return;

  // Reply tokens expire after about a minute; fall back to push (counts toward the monthly quota).
  if (replyToken && Date.now() - started < 50_000) {
    try {
      await line.replyMessage({ replyToken, messages });
      return;
    } catch (e) {
      console.warn("reply failed, falling back to push", e);
    }
  }
  await line.pushMessage({ to: userId, messages });
}

/** Download an image the owner sent. Claude accepts up to ~5 MB; LINE photos are usually well under. */
async function readImage(messageId: string): Promise<{ data: string; type: "image/jpeg" | "image/png" } | null> {
  try {
    const stream = await blob.getMessageContent(messageId);
    const chunks: Buffer[] = [];
    for await (const c of stream as AsyncIterable<Buffer>) chunks.push(Buffer.from(c));
    const buf = Buffer.concat(chunks);
    if (buf.length > 4.5 * 1024 * 1024) return null;
    const type = buf[0] === 0x89 && buf[1] === 0x50 ? "image/png" : "image/jpeg";
    return { data: buf.toString("base64"), type };
  } catch (e) {
    console.error("image download failed", e);
    return null;
  }
}

async function handleMenu(data: string): Promise<messagingApi.Message[]> {
  if (data === "cmd:digest") return [await buildDigest()];
  if (data === "cmd:switch") return [projectPicker("เลือกโปรเจกต์ปัจจุบันครับ", await plane.projects())];
  if (data === "cmd:settings") return [textMsg(`หน้าตั้งค่า\n${settingsUrl()}`)];
  if (data === "cmd:create") return [textMsg("พิมพ์งานที่จะสร้างได้เลยครับ เช่น\nสร้างงานหน้า login ด้วย OTP แตกเป็น FE/BE/QA ให้ต้นทำ BE ส่งศุกร์หน้า")];
  return (await handlePostback(data)).messages;
}

// ------------------------------------------------------------- scheduled pushes (digest, alerts, reports)

async function pushOwner(messages: messagingApi.Message[]) {
  if (!config.line.ownerUserId || !messages.length) return;
  await line.pushMessage({ to: config.line.ownerUserId, messages: messages.slice(0, 5) });
}
startScheduler(pushOwner, (expr, fn) => void cron.schedule(expr, fn, { timezone: config.timezone }));

// ------------------------------------------------------------- daily history snapshot (Postgres)

async function snapshotAll() {
  for (const p of await plane.projects()) {
    try {
      await snapshotMetrics(await projectMetrics(plane, p));
    } catch (e) {
      console.error(`snapshot ${p.identifier} failed`, e instanceof Error ? e.message : e);
    }
  }
}
cron.schedule("50 23 * * *", () => void snapshotAll().catch((e) => console.error("snapshot failed", e)), { timezone: config.timezone });

// ------------------------------------------------------------- Plane webhook (optional)
// Plane → Workspace settings → Webhooks → URL https://<host>/plane/webhook, copy the secret into PLANE_WEBHOOK_SECRET.

app.post("/plane/webhook", async (c) => {
  const body = await c.req.text();
  if (!config.plane.webhookSecret) return c.text("disabled", 404);
  const sig = c.req.header("x-plane-signature") ?? "";
  const expected = crypto.createHmac("sha256", config.plane.webhookSecret).update(body).digest("hex");
  if (!safeEqual(sig, expected)) return c.text("bad signature", 401);
  const ev = JSON.parse(body);
  try {
    const act = ev.activity ?? {};
    const isIssue = ev.event === "issue" || ev.event === "work_item";
    if (isIssue && ev.action === "updated" && act.field === "state" && /block/i.test(String(act.new_value ?? "")) && config.line.ownerUserId) {
      const d = ev.data ?? {};
      const project = (await plane.projects()).find((p) => p.id === (d.project ?? d.project_id));
      if (project) {
        const key = `${project.identifier}-${d.sequence_id}`;
        await line.pushMessage({ to: config.line.ownerUserId, messages: [textMsg(`แจ้งเตือนจาก Plane\n${key} ${d.name ?? ""}\nถูกเปลี่ยนเป็น ${act.new_value}`)] });
      }
    }
  } catch (e) {
    console.error("plane webhook handling failed", e);
  }
  return c.text("ok");
});

// ------------------------------------------------------------- dashboard

function safeEqual(a: string, b: string) {
  const x = Buffer.from(a), y = Buffer.from(b);
  return x.length === y.length && crypto.timingSafeEqual(x, y);
}

/** Dashboard token from ?t= (pages) or the x-token header (API calls from the pages). */
function authed(c: Context) {
  return safeEqual(c.req.query("t") ?? c.req.header("x-token") ?? "", config.dashboardToken);
}

registerSettings(app as any, authed, settingsHtml);

/** Full view of an email / report draft waiting for confirmation. */
app.get("/preview/:id", (c) => {
  if (!authed(c)) return c.text("ลิงก์ไม่ถูกต้อง", 401);
  const id = c.req.param("id");
  const s = store.get();
  const pv = (s.proactive[id]?.preview ?? (s.pending?.id === id ? s.pending.writes.find((w) => (w.preview as Preview).fullHtml)?.preview : undefined)) as Preview | undefined;
  if (!pv?.fullHtml) return c.text("ร่างนี้ส่งไปแล้วหรือหมดอายุแล้ว", 404);
  const plan = pv.plan as any;
  const head = plan.kind === "email" ? `<p style="color:#5A6573;font-size:13px">ถึง: ${(plan.to ?? []).join(", ") || "(ยังไม่ได้ตั้ง)"}${plan.cc?.length ? `<br>สำเนา: ${plan.cc.join(", ")}` : ""}<br>เรื่อง: ${String(plan.subject ?? "").replace(/</g, "&lt;")}</p><hr>` : "";
  return c.html(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ร่าง</title><body style="max-width:720px;margin:auto;padding:20px;background:#fff">${head}${pv.fullHtml}<p style="color:#5A6573;font-size:12px;margin-top:24px">กดส่งจากการ์ดใน LINE</p></body>`, 200, { "Cache-Control": "no-store", "Referrer-Policy": "no-referrer" });
});

/** Risk log, CR, milestones and man-day budget for the dashboard. */
app.get("/api/records", async (c) => {
  if (!authed(c)) return c.json({ error: "unauthorized" }, 401);
  if (!sql) return c.json({ enabled: false });
  try {
    const projects = await plane.projects();
    const p = projects.find((x) => x.id === c.req.query("project")) ?? projects[0];
    if (!p) return c.json({ enabled: true, risks: [], decisions: [], crs: [], milestones: [], budget: null });
    const [risks, decisions, crs, milestones, budget] = await Promise.all([
      listRecords("risk", { projectId: p.id }),
      listRecords("decision", { projectId: p.id, limit: 20 }),
      listRecords("cr", { projectId: p.id }),
      milestonesFor(p, true).catch(() => []),
      budgetStatus(p).catch(() => null),
    ]);
    return c.json({ enabled: true, risks, decisions, crs, milestones, budget });
  } catch (e) {
    console.error("records api failed", e);
    return c.json({ error: e instanceof Error ? e.message : String(e) }, 502);
  }
});

app.get("/dashboard", (c) => {
  if (!safeEqual(c.req.query("t") ?? "", config.dashboardToken)) return c.text("ลิงก์ไม่ถูกต้อง ขอลิงก์ใหม่จากบอทใน LINE", 401);
  const html = dashboardHtml();
  return c.html(html, 200, { "Cache-Control": "no-store", "Referrer-Policy": "no-referrer" });
});

app.get("/api/dashboard", async (c) => {
  if (!safeEqual(c.req.query("t") ?? "", config.dashboardToken)) return c.json({ error: "unauthorized" }, 401);
  try {
    const projects = await plane.projects();
    const want = c.req.query("project");
    const selected = projects.find((p) => p.id === want) ?? projects.find((p) => p.id === store.get().currentProjectId) ?? projects[0];
    if (!selected) return c.json({ projects: [], metrics: null });
    const metrics = await projectMetrics(plane, selected);
    void snapshotMetrics(metrics).catch((e) => console.error("snapshot failed", e?.message ?? e)); // today's point stays fresh
    return c.json({ projects: projects.map((p) => ({ id: p.id, name: p.name })), selected: selected.id, metrics, updatedAt: new Date().toISOString() });
  } catch (e) {
    console.error("dashboard api failed", e);
    return c.json({ error: e instanceof Error ? e.message : String(e) }, 502);
  }
});

app.get("/api/history", async (c) => {
  if (!safeEqual(c.req.query("t") ?? "", config.dashboardToken)) return c.json({ error: "unauthorized" }, 401);
  const projectId = c.req.query("project") ?? "";
  const days = Math.min(365, Math.max(7, Number(c.req.query("days")) || 90));
  try {
    return c.json(await history(projectId, days));
  } catch (e) {
    console.error("history api failed", e);
    return c.json({ error: "history unavailable" }, 502);
  }
});

app.get("/api/usage", (c) => {
  if (!safeEqual(c.req.query("t") ?? "", config.dashboardToken)) return c.json({ error: "unauthorized" }, 401);
  return c.json(store.get().usage);
});

function started(port: number) {
  console.log(`LINE PM Assistant listening on :${port}`);
  console.log(`Dashboard: ${config.publicUrl}/dashboard?t=<DASHBOARD_TOKEN>  Settings: ${config.publicUrl}/settings?t=<DASHBOARD_TOKEN>`);
  const p = provider();
  if (p.kind === "none") console.log("Task tool: not connected yet (open Settings)");
  else plane.me().then(
    (me) => console.log(`Task tool OK (${p.kind}): connected as ${me.display_name}`),
    (e) => console.error(`Task tool check failed (${p.kind}):`, e instanceof Error ? e.message : e),
  );
  if (process.env.RICHMENU_AUTO !== "false") {
    ensureRichMenu().then((id) => console.log("rich menu ready", id), (e) => console.error("rich menu setup failed", e?.message ?? e));
  }
}

// Railway Functions run on Bun; elsewhere use Node's HTTP server.
await initStore();
await migrateBuiltin().catch((e) => console.error("built-in tables failed", e?.message ?? e));
provider(); // pick the task tool now so every log row is tagged with its workspace
const bun = (globalThis as { Bun?: { serve(o: { port: number; fetch: typeof app.fetch }): unknown } }).Bun;
if (bun) {
  bun.serve({ port: config.port, fetch: app.fetch });
  started(config.port);
} else {
  serve({ fetch: app.fetch, port: config.port }, (info) => started(info.port));
}
