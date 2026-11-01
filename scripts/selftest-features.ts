// Offline test of round-2 features against a mocked Plane API and a real Postgres.
// TEST_DATABASE_URL=postgres://... npx tsx scripts/selftest-features.ts
process.env.LINE_CHANNEL_SECRET ||= "x";
process.env.LINE_CHANNEL_ACCESS_TOKEN ||= "x";
process.env.PLANE_BASE_URL = "https://plane.test";
process.env.PLANE_API_KEY = "envtoken1234";
process.env.PLANE_WORKSPACE_SLUG = "ws";
process.env.DASHBOARD_TOKEN ||= "t";
process.env.DATA_DIR = "./data-test";
if (!process.env.TEST_DATABASE_URL) { console.log("skip: set TEST_DATABASE_URL"); process.exit(0); }
process.env.DATABASE_URL = process.env.TEST_DATABASE_URL;

const DAY = 86400_000;
const { dateStr } = await import("../src/metrics.js");
const d = (n: number) => dateStr(new Date(Date.now() + n * DAY));
const iso = (n: number) => new Date(Date.now() + n * DAY).toISOString();
const page = (results: unknown[]) => ({ results, next_cursor: null, next_page_results: false });

const data: Record<string, unknown> = {
  "/users/me/": { id: "u1", display_name: "ton", email: "ton@corp.test" },
  "/projects/": page([{ id: "p1", name: "FDA Big Data", identifier: "FDA" }]),
  "/projects/p1/states/": page([
    { id: "s-todo", name: "Todo", group: "unstarted" }, { id: "s-prog", name: "In Progress", group: "started" },
    { id: "s-block", name: "Blocked", group: "started" }, { id: "s-done", name: "Done", group: "completed" },
  ]),
  "/projects/p1/project-members/": [{ member: { id: "u1", display_name: "ton", email: "ton@corp.test" } }, { id: "u2", display_name: "may" }],
  "/projects/p1/labels/": page([]),
  "/projects/p1/cycles/": page([{ id: "c0", name: "Sprint 0", start_date: d(-21), end_date: d(-8) }, { id: "c1", name: "Sprint 1", start_date: d(-7), end_date: d(7) }]),
  "/projects/p1/modules/": page([{ id: "m1", name: "Login", start_date: d(-10), target_date: d(2) }]),
  "/projects/p1/cycles/c1/cycle-issues/": page([{ id: "x1", issue: "i1", created_at: iso(-7) }, { id: "x3", issue: "i3", created_at: iso(-2) }]),
  "/projects/p1/cycles/c0/cycle-issues/": page([{ id: "x2", issue: "i2", created_at: iso(-21) }, { id: "x4", issue: "i4", created_at: iso(-21) }]),
  "/projects/p1/modules/m1/module-issues/": page([{ id: "y1", issue: "i1" }, { id: "y2", issue: "i2" }]),
  "/projects/p1/work-items/": page([
    { id: "i1", name: "API login", sequence_id: 1, state: "s-prog", assignees: ["u1"], target_date: d(-3), created_at: iso(-10), updated_at: iso(-8), priority: "high" },
    { id: "i2", name: "OTP", sequence_id: 2, state: "s-block", assignees: ["u2"], target_date: d(-9), created_at: iso(-20), updated_at: iso(-9) },
    { id: "i3", name: "UI", sequence_id: 3, state: "s-todo", assignees: ["u1"], target_date: d(9), created_at: iso(-2) },
    { id: "i4", name: "Docs", sequence_id: 4, state: "s-done", completed_at: iso(-1), created_at: iso(-10) },
  ]),
};
const comments: string[] = [];
const realFetch = globalThis.fetch;
globalThis.fetch = (async (url: string | URL, init?: RequestInit) => {
  const u = new URL(String(url));
  if (u.host !== "plane.test") return realFetch(url, init);
  if (init?.headers && (init.headers as Record<string, string>)["X-API-Key"] === "bad") return new Response("{}", { status: 401 });
  const key = u.pathname.replace("/api/v1/workspaces/ws", "").replace("/api/v1", "");
  if (init?.method === "POST") {
    if (key.endsWith("/comments/")) comments.push(key);
    return new Response("{}", { status: 201 });
  }
  const body = data[key];
  if (body === undefined) return new Response("not found " + key, { status: 404 });
  return new Response(JSON.stringify(key.endsWith("work-items/") && u.searchParams.get("per_page") === "1" ? page([]) : body), { status: 200 });
}) as typeof fetch;

let failed = 0;
const check = (name: string, ok: boolean, got?: unknown) => {
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${ok ? "" : ` (got ${JSON.stringify(got)?.slice(0, 400)})`}`);
  if (!ok) failed++;
};

const db = await import("../src/db.js");
await db.sql!`drop table if exists pm_records, app_state, activity_log, usage_log, metrics_daily, bt_comments, bt_items, bt_projects cascade`;
const { initStore, store } = await import("../src/store.js");
await initStore();
const { migrateBuiltin } = await import("../src/providers/builtin.js");
await migrateBuiltin();
const { provider, resetProvider, tasks } = await import("../src/providers/index.js");
const { encrypt, decrypt } = await import("../src/crypto.js");
const { prepareWrite, executeWrite } = await import("../src/tools.js");
const { takeCards } = await import("../src/cards.js");
const recs = await import("../src/records.js");
const ins = await import("../src/insights.js");
const feat = await import("../src/features.js");
const time = await import("../src/time.js");
const notion = await import("../src/notion.js");

// ---- crypto, time
check("encrypt/decrypt round trip", decrypt(encrypt("secret-ä-ข")) === "secret-ä-ข");
check("decrypt garbage = null", decrypt("v1.aaa.bbb.ccc") === null);
check("Bangkok 10:00 = 03:00Z", time.localToDate("2026-11-12", "10:00").toISOString() === "2026-11-12T03:00:00.000Z", time.localToDate("2026-11-12", "10:00"));
check("localIso has +07:00", time.localIso(new Date("2026-11-12T03:00:00Z")) === "2026-11-12T10:00:00+07:00", time.localIso(new Date("2026-11-12T03:00:00Z")));
check("quiet hours across midnight", time.inQuietHours("20:00", "08:00", "23:10") && !time.inQuietHours("20:00", "08:00", "09:00"));

// ---- provider from env
check("env provider is plane", provider().kind === "plane");
check("workspace key", db.workspace() === "plane:plane.test/ws", db.workspace());
store.update((s) => (s.currentProjectId = "p1"));

// ---- risk log
let pv = await prepareWrite("log_risk", { type: "risk", title: "API หน่วยงานล่าช้า", impact: "high", likelihood: "medium", mitigation: "ใช้ CSV" });
check("risk preview has impact line", pv.lines.some((l) => l.sub.includes("สูง")), pv.lines);
let r: any = await executeWrite(pv.plan);
check("risk saved R-01", r.record === "R-01", r);
pv = await prepareWrite("log_risk", { type: "decision", title: "เลื่อน dashboard ไป sprint 16" });
r = await executeWrite(pv.plan);
check("decision saved D-01", r.record === "D-01", r);
pv = await prepareWrite("update_record", { code: "R-01", status: "closed" });
await executeWrite(pv.plan);
check("risk closed", (await recs.getRecord("R-01"))?.status === "closed");

// ---- milestones
pv = await prepareWrite("set_milestone", { name: "งวดที่ 2", due: d(12), value_percent: 30, modules: ["Login"], deliverables: [{ tor: "4.2", name: "API login" }, { tor: "6.1", name: "คู่มือผู้ดูแลระบบ" }, { tor: "4.5", name: "Docs" }] });
r = await executeWrite(pv.plan);
check("milestone saved M-01", r.record === "M-01", r);
const [project] = await tasks.projects();
const ms = await ins.milestonesFor(project);
check("deliverable statuses", ms[0]?.deliverables.map((x) => x.status).join() === "open,missing,done", ms[0]?.deliverables);
check("module percent (Login 0/2)", ms[0]?.modulePercent === 0, ms[0]);
check("milestone card builds", JSON.stringify(feat.milestoneCard(project.name, ms[0])).includes("สร้างงานที่ยังขาด 1 งาน"));

// ---- change request against the milestone
pv = await prepareWrite("log_change_request", { title: "Export Excel", requester: "คุณวิภา", tasks: 5, man_days: 20, create_document: false });
check("CR preview shows schedule impact", pv.lines.some((l) => l.text === "ผลต่อกำหนดส่ง"), pv.lines);
r = await executeWrite(pv.plan);
check("CR saved pending", (await recs.getRecord("CR-01"))?.status === "pending", r);

// ---- budget + effort
await executeWrite((await prepareWrite("set_budget", { total_man_days: 10, modules: { Login: 4 } })).plan);
await executeWrite((await prepareWrite("log_effort", { entries: [{ item_key: "FDA-1", hours: 16, who: "ton" }, { item_key: "FDA-3", hours: 8 }] })).plan);
const b = await ins.budgetStatus(project);
check("budget used 3 md", b.usedManDays === 3, b);
check("budget by module Login 2", b.modules.find((x) => x.name === "Login")?.used === 2, b.modules);
check("progress 25%", b.progressPercent === 25, b.progressPercent);

// ---- capacity (no Google)
const cap = await ins.capacity(project, (await import("../src/metrics.js")).startOfWeek(d(0)));
check("capacity has ton", cap.people.some((p) => p.name === "ton"), cap.people);

// ---- nudges
pv = await prepareWrite("nudge_stale", {});
check("nudge lists stale FDA-1 + FDA-2", pv.lines.filter((l) => /FDA-\d/.test(l.text)).length === 2, pv.lines);
r = await executeWrite(pv.plan);
check("nudge posted 2 comments", comments.length === 2, comments);

// ---- reminders
await feat.runFeatureRead("set_reminder", { at: `${d(1)}T10:00`, text: "ตาม FDA-1", item_key: "FDA-1" });
check("reminder card attached", takeCards().length === 1);
const rem = (await recs.listRecords<any>("reminder", { status: "open" }))[0];
await recs.updateRecord(rem.id, { dueAt: new Date(Date.now() - 1000) });
check("due reminder found", (await recs.dueReminders()).length === 1);
let threw = false;
try { await feat.runFeatureRead("set_reminder", { at: `${d(-1)}T10:00`, text: "x" }); } catch { threw = true; }
check("past reminder rejected", threw);

// ---- weekly report, retro
const wr = await ins.weeklyReport();
check("weekly report mentions project + done item", wr.html.includes("FDA Big Data") && wr.done.some((x) => x.key === "FDA-4"), wr);
const rt = await ins.retro(project);
check("retro of Sprint 0: 1/2 done, 1 blocked missed", rt?.cycle === "Sprint 0" && rt.done === 1 && rt.improve.some((x) => x.includes("Blocked")), rt);
const wp = await feat.weeklyReportProactive(wr);
check("weekly proactive saved", !!store.get().proactive[wp.id]);

// ---- helpers
check("textToHtml bullets", feat.textToHtml("สรุป\n\n- a\n- b").includes("<li>a</li>"));
check("notion blocks", notion.toBlocks("# H\n- a\n[ ] todo\ntext").map((x: any) => x.type).join() === "heading_2,bulleted_list_item,to_do,paragraph");
check("notion id from url", notion.normalizeId("https://www.notion.so/Retro-1a2b3c4d5e6f708192a3b4c5d6e7f809?pvs=4") === "1a2b3c4d5e6f708192a3b4c5d6e7f809");

// ---- settings API
const { Hono } = await import("hono");
const { registerSettings } = await import("../src/settings.js");
const app = new Hono();
registerSettings(app as any, () => true, () => "<html></html>");
const post = (path: string, body: unknown) => app.request(path, { method: "POST", body: JSON.stringify(body), headers: { "Content-Type": "application/json" } });
let res = await app.request("/api/settings");
const snap: any = await res.json();
check("settings snapshot ok", res.status === 200 && snap.task.plane?.host === "plane.test" && snap.task.plane.tokenLast4 === "1234", snap.task);
check("snapshot never contains the token", !JSON.stringify(snap).includes("envtoken1234"));
res = await post("/api/settings/plane/test", { baseUrl: "https://other.test", workspace: "ws" });
check("new server without token refused", res.status === 400 && (await res.json()).error.includes("token ใหม่"));
res = await post("/api/settings/plane/test", { baseUrl: "plane.test", workspace: "ws" });
check("same server reuses env token", res.status === 200, await res.clone().json());
res = await post("/api/settings/plane/save", { baseUrl: "https://plane.test", workspace: "ws", token: "newtoken9999", projects: ["FDA"] });
check("plane saved from settings", res.status === 200 && store.get().connections.task.provider === "plane" && store.get().connections.task.plane?.tokenLast4 === "9999");
check("saved token encrypted", !JSON.stringify(store.get()).includes("newtoken9999"));
res = await post("/api/settings/prefs", { digestTime: "25:00" });
check("bad time rejected", res.status === 400);
res = await post("/api/settings/prefs", { digestTime: "08:15", weekly: { enabled: true, day: 5, time: "16:00", to: "a@x.co, bad, b@y.co" } });
check("prefs saved", store.get().prefs.digestTime === "08:15" && store.get().prefs.weekly.to.join() === "a@x.co,b@y.co", store.get().prefs);

// ---- built-in provider
res = await post("/api/settings/provider", { provider: "builtin" });
check("switch to builtin", res.status === 200 && provider().kind === "builtin" && db.workspace() === "builtin");
res = await post("/api/settings/builtin/project", { name: "ERP Rollout", identifier: "erp" });
const bp = (await tasks.projects())[0];
check("builtin project created", bp?.identifier === "ERP", bp);
store.update((s) => (s.currentProjectId = bp.id));
pv = await prepareWrite("create_work_items", { items: [{ name: "Kickoff", target_date: d(2) }, { name: "Setup", state: "In Progress" }] });
r = await executeWrite(pv.plan);
check("builtin items created ERP-1, ERP-2", r.created?.map((c: any) => c.key).join() === "ERP-1,ERP-2", r);
pv = await prepareWrite("update_work_item", { key: "ERP-1", state: "Done" });
await executeWrite(pv.plan);
const it = await tasks.workItemByKey("ERP-1");
check("builtin update to done sets completedAt", it.stateId === "done" && !!it.completedAt, it);
check("risk log is per workspace (builtin empty)", (await recs.listRecords("risk")).length === 0);

// ---- data summary / delete
const sum = await db.dataSummary();
check("data summary lists plane workspace", sum.some((w) => w.ws === "plane:plane.test/ws" && w.records >= 3), sum);
res = await post("/api/settings/delete", { scope: "workspace", ws: "plane:plane.test/ws" });
check("workspace data deleted", (await db.dataSummary()).every((w) => w.ws !== "plane:plane.test/ws"));
res = await post("/api/settings/delete", { scope: "all", confirm: "ไม่" });
check("wipe needs typed confirm", res.status === 400);
res = await post("/api/settings/delete", { scope: "all", confirm: "ลบทั้งหมด" });
check("wipe all", res.status === 200 && store.get().connections.task.provider === "none" && provider().kind === "none");

await new Promise((r) => setTimeout(r, 300));
await db.sql!.end();
resetProvider();
console.log(failed ? `\n${failed} failed` : "\nall passed");
process.exit(failed ? 1 : 0);
