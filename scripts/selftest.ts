// Offline self-test: mocks the Plane API and checks the metrics + flex cards. No network, no AI.
process.env.LINE_CHANNEL_SECRET ||= "x";
process.env.LINE_CHANNEL_ACCESS_TOKEN ||= "x";
process.env.PLANE_BASE_URL ||= "https://plane.test";
process.env.PLANE_API_KEY ||= "x";
process.env.PLANE_WORKSPACE_SLUG ||= "ws";
process.env.DASHBOARD_TOKEN ||= "t";
process.env.DATA_DIR = "./data-test";

const { PlaneClient } = await import("../src/plane.js");
const { projectMetrics, dateStr } = await import("../src/metrics.js");
const { statusCard, digestCard } = await import("../src/flex.js");

const DAY = 86400_000;
const d = (n: number) => dateStr(new Date(Date.now() + n * DAY));
const iso = (n: number) => new Date(Date.now() + n * DAY).toISOString();
const page = (results: unknown[]) => ({ results, next_cursor: null, next_page_results: false });

const data: Record<string, unknown> = {
  "/projects/": page([{ id: "p1", name: "FDA Big Data", identifier: "FDA" }]),
  "/projects/p1/states/": page([
    { id: "s-todo", name: "Todo", group: "unstarted" },
    { id: "s-prog", name: "In Progress", group: "started" },
    { id: "s-block", name: "Blocked", group: "started" },
    { id: "s-done", name: "Done", group: "completed" },
  ]),
  "/projects/p1/project-members/": [{ member: { id: "u1", display_name: "ton" } }, { id: "u2", display_name: "may" }],
  "/projects/p1/labels/": page([{ id: "l-bug", name: "Bug" }]),
  "/projects/p1/cycles/": page([{ id: "c1", name: "Sprint 1", start_date: d(-7), end_date: d(7) }]),
  "/projects/p1/modules/": page([{ id: "m1", name: "Login", start_date: d(-10), target_date: d(2) }]),
  "/projects/p1/cycles/c1/cycle-issues/": page([
    { id: "x1", issue: "i1", created_at: iso(-7) },
    { id: "x2", issue: "i2", created_at: iso(-7) },
    { id: "x3", issue: "i3", created_at: iso(-2) },
    { id: "x4", issue: "i4", created_at: iso(-7) },
  ]),
  "/projects/p1/modules/m1/module-issues/": page([{ id: "y1", issue: "i1" }, { id: "y2", issue: "i2" }]),
  "/projects/p1/work-items/": page([
    { id: "i1", name: "API login", sequence_id: 1, state: "s-prog", assignees: ["u1"], target_date: d(-3), created_at: iso(-10), updated_at: iso(-8), priority: "high" },
    { id: "i2", name: "OTP", sequence_id: 2, state: "s-block", assignees: ["u2"], target_date: d(0), created_at: iso(-10), updated_at: iso(-1), labels: ["l-bug"], priority: "urgent" },
    { id: "i3", name: "UI", sequence_id: 3, state: "s-todo", assignees: [], target_date: d(3), created_at: iso(-2) },
    { id: "i4", name: "Docs", sequence_id: 4, state: "s-done", completed_at: iso(0), created_at: iso(-10) },
  ]),
};

const mockFetch = (async (url: string | URL) => {
  const u = new URL(String(url));
  const key = u.pathname.replace("/api/v1/workspaces/ws", "");
  const body = data[key];
  if (body === undefined) return new Response("not found " + key, { status: 404 });
  return new Response(JSON.stringify(key.endsWith("work-items/") && u.searchParams.get("per_page") === "1" ? page([]) : body), { status: 200 });
}) as typeof fetch;

const plane = new PlaneClient("https://plane.test", "x", "ws", mockFetch);
const [project] = await plane.projects();
const m = await projectMetrics(plane, project);

let failed = 0;
const check = (name: string, ok: boolean, got?: unknown) => {
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${ok ? "" : ` (got ${JSON.stringify(got)})`}`);
  if (!ok) failed++;
};
check("open count", m.counts.open === 3, m.counts.open);
check("overdue = FDA-1", m.overdue.map((v) => v.key).join() === "FDA-1", m.overdue);
check("due today = FDA-2", m.dueToday.map((v) => v.key).join() === "FDA-2", m.dueToday);
check("blocked = FDA-2", m.blocked.map((v) => v.key).join() === "FDA-2", m.blocked);
check("stale = FDA-1", m.stale.map((v) => v.key).join() === "FDA-1", m.stale);
check("unassigned = 1", m.counts.unassigned === 1, m.counts.unassigned);
check("done this week = 1", m.counts.doneThisWeek === 1, m.counts.doneThisWeek);
check("urgent bugs = 1", m.counts.urgentBugs === 1, m.counts.urgentBugs);
check("cycle total 4, done 1", m.cycle?.total === 4 && m.cycle?.done === 1, m.cycle);
check("cycle added midway = 1", m.cycle?.addedMidway === 1, m.cycle?.addedMidway);
check("module listed", m.modules.length === 1, m.modules);
check("member wrapped in `member` is resolved", m.workload.some((w) => w.name.includes("ton")), m.workload);

const cards = [statusCard(m, "https://x/dashboard"), digestCard([m], "วันจันทร์ 1 พ.ย.", "https://x/dashboard")];
for (const c of cards) check(`card ${c.type} serialises < 30KB`, JSON.stringify(c).length < 30_000);

console.log(failed ? `\n${failed} failed` : "\nall passed");
process.exit(failed ? 1 : 0);
