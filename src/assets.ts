// Static assets. Read from disk when run from the repo; the single-file Railway Function build
// replaces this module with inlined copies (see scripts/build-function.ts).
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");

export const dashboardHtml = () => fs.readFileSync(path.join(root, "public", "dashboard.html"), "utf8");
export const settingsHtml = () => fs.readFileSync(path.join(root, "public", "settings.html"), "utf8");
export const richMenuImage = () => fs.readFileSync(path.join(root, "assets", "richmenu.png"));
