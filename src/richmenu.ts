// Installs the 6-button rich menu as the OA default. Runs on startup (idempotent) and via `npm run richmenu`.
import { messagingApi } from "@line/bot-sdk";
import { config } from "./config.js";
import { dashboardUrl } from "./tools.js";
import { richMenuImage } from "./assets.js";

const MENU_NAME = "pm-assistant-v1";

export async function ensureRichMenu(force = false) {
  const client = new messagingApi.MessagingApiClient({ channelAccessToken: config.line.accessToken });
  const blob = new messagingApi.MessagingApiBlobClient({ channelAccessToken: config.line.accessToken });
  const url = dashboardUrl();

  const existing = (await client.getRichMenuList()).richmenus;
  // The Dashboard button carries the URL, so a new PUBLIC_URL or token means a new menu.
  const current = existing.find((m) => m.name === MENU_NAME && m.areas.some((a) => (a.action as { uri?: string }).uri === url));
  if (current && !force) {
    await client.setDefaultRichMenu(current.richMenuId);
    return current.richMenuId;
  }

  const W = 2500 / 3, H = 1686 / 2;
  const cell = (i: number) => ({ x: Math.round((i % 3) * W), y: Math.round(Math.floor(i / 3) * H), width: Math.round(W), height: Math.round(H) });
  const pb = (data: string, displayText: string) => ({ type: "postback" as const, data, displayText });
  const actions = [
    { type: "uri" as const, uri: url, label: "Dashboard" },
    pb("cmd:status", "สถานะโปรเจกต์"),
    pb("cmd:mine", "งานของฉัน"),
    pb("cmd:create", "สร้างงาน"),
    pb("cmd:digest", "สรุปวันนี้"),
    pb("cmd:switch", "เปลี่ยนโปรเจกต์"),
  ];
  const { richMenuId } = await client.createRichMenu({
    size: { width: 2500, height: 1686 },
    selected: true,
    name: MENU_NAME,
    chatBarText: "เมนู PM",
    areas: actions.map((action, i) => ({ bounds: cell(i), action })),
  });
  await blob.setRichMenuImage(richMenuId, new Blob([richMenuImage()], { type: "image/png" }));
  await client.setDefaultRichMenu(richMenuId);
  // Remove our older menus so the list doesn't grow on each URL change.
  for (const m of existing) if (m.name === MENU_NAME) await client.deleteRichMenu(m.richMenuId).catch(() => undefined);
  return richMenuId;
}
