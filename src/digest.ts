// Morning digest: computed in code from task data + today's meetings (no AI call, no cost).
import { plane, dashboardUrl } from "./tools.js";
import { projectMetrics, dateStr, thaiDate } from "./metrics.js";
import { digestCard } from "./flex.js";
import { snapshotMetrics } from "./db.js";
import { googleConnected, todaysMeetings } from "./google.js";
import { hm } from "./time.js";

const TH_DAY_FULL = ["วันอาทิตย์", "วันจันทร์", "วันอังคาร", "วันพุธ", "วันพฤหัสบดี", "วันศุกร์", "วันเสาร์"];

export async function buildDigest() {
  const projects = await plane.projects();
  const metrics = [];
  for (const p of projects) {
    try {
      const m = await projectMetrics(plane, p);
      metrics.push(m);
      await snapshotMetrics(m).catch(() => undefined);
    } catch (e) {
      console.error(`digest: ${p.identifier} failed`, e);
    }
  }
  const today = dateStr();
  const dow = new Date(today + "T00:00:00Z").getUTCDay();
  const label = `${TH_DAY_FULL[dow]} ${thaiDate(today, false)}`;
  let meetings: string[] = [];
  if (googleConnected()) {
    try {
      meetings = (await todaysMeetings()).slice(0, 4).map((e) => `${hm(new Date(e.start))} ${e.summary}${e.meetUrl ? " · Meet" : ""}`);
    } catch (e) {
      console.error("digest: calendar failed", e instanceof Error ? e.message : e);
    }
  }
  return digestCard(metrics, label, dashboardUrl(), meetings);
}
