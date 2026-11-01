// Morning digest: computed in code from Plane data (no AI call, no cost).
import { plane, dashboardUrl } from "./tools.js";
import { projectMetrics, dateStr, thaiDate } from "./metrics.js";
import { digestCard } from "./flex.js";
import { snapshotMetrics } from "./db.js";

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
  return digestCard(metrics, label, dashboardUrl());
}
