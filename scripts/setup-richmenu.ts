import { ensureRichMenu } from "../src/richmenu.js";
ensureRichMenu(true).then((id) => console.log("rich menu set:", id), (e) => { console.error(e); process.exit(1); });
