// HTTP server: LINE webhook, Plane webhook, dashboard API + page, cron digest.
import { Hono } from "hono";
import { serve } from "@hono/node-server";
import cron from "node-cron";
import crypto from "node:crypto";
import { messagingApi, validateSignature, webhook } from "@line/bot-sdk";
import { config } from "./config.js";
import { handleText, handlePostback } from "./agent.js";
import { buildDigest } from "./digest.js";
import { plane, dashboardUrl } from "./tools.js";
import { projectMetrics } from "./metrics.js";
import { projectPicker, textMsg } from "./flex.js";
import { store } from "./store.js";
import { ensureRichMenu } from "./richmenu.js";
import { dashboardHtml } from "./assets.js";

const line = new messagingApi.MessagingApiClient({ channelAccessToken: config.line.accessToken });
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
    messages = [textMsg("สวัสดีครับ ผมคือ PM Assistant ถามสถานะงาน สั่งสร้างหรือแก้งานใน Plane ได้ด้วยข้อความ หรือใช้เมนูด้านล่าง")];
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
  } else if (ev.type === "message") {
    messages = [textMsg("ตอนนี้รับเฉพาะข้อความตัวอักษรครับ")];
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

async function handleMenu(data: string): Promise<messagingApi.Message[]> {
  if (data === "cmd:digest") return [await buildDigest()];
  if (data === "cmd:switch") return [projectPicker("เลือกโปรเจกต์ปัจจุบันครับ", await plane.projects())];
  if (data === "cmd:create") return [textMsg("พิมพ์งานที่จะสร้างได้เลยครับ เช่น\nสร้างงานหน้า login ด้วย OTP แตกเป็น FE/BE/QA ให้ต้นทำ BE ส่งศุกร์หน้า")];
  return (await handlePostback(data)).messages;
}

// ------------------------------------------------------------- morning digest

cron.schedule(
  config.digestCron,
  async () => {
    if (!config.line.ownerUserId) return;
    try {
      await line.pushMessage({ to: config.line.ownerUserId, messages: [await buildDigest()] });
    } catch (e) {
      console.error("digest failed", e);
    }
  },
  { timezone: config.timezone },
);

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
    return c.json({ projects: projects.map((p) => ({ id: p.id, name: p.name })), selected: selected.id, metrics, updatedAt: new Date().toISOString() });
  } catch (e) {
    console.error("dashboard api failed", e);
    return c.json({ error: e instanceof Error ? e.message : String(e) }, 502);
  }
});

app.get("/api/usage", (c) => {
  if (!safeEqual(c.req.query("t") ?? "", config.dashboardToken)) return c.json({ error: "unauthorized" }, 401);
  return c.json(store.get().usage);
});

function started(port: number) {
  console.log(`LINE PM Assistant listening on :${port}`);
  console.log(`Dashboard: ${config.publicUrl}/dashboard?t=<DASHBOARD_TOKEN>`);
  if (process.env.RICHMENU_AUTO !== "false") {
    ensureRichMenu().then((id) => console.log("rich menu ready", id), (e) => console.error("rich menu setup failed", e?.message ?? e));
  }
}

// Railway Functions run on Bun; elsewhere use Node's HTTP server.
const bun = (globalThis as { Bun?: { serve(o: { port: number; fetch: typeof app.fetch }): unknown } }).Bun;
if (bun) {
  bun.serve({ port: config.port, fetch: app.fetch });
  started(config.port);
} else {
  serve({ fetch: app.fetch, port: config.port }, (info) => started(info.port));
}
