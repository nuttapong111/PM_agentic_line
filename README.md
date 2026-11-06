# LINE PM Assistant

ผู้ช่วย PM ใน LINE ที่ต่อกับ Plane (self-hosted) หรือระบบงานในตัว, Google Workspace และ Notion ใช้ Claude Haiku คุยภาษาไทย ทุกการเขียนหรือส่งออกนอกระบบต้องกดยืนยันก่อน มีหน้า Dashboard และหน้าตั้งค่าบนเว็บ

## สิ่งที่ทำได้ (รอบ 1)
- ถาม-ตอบสถานะโปรเจกต์ งานเลยกำหนด งานของฉัน งาน Blocked
- สร้างงาน (แตกงานย่อยได้) แก้สถานะ/ผู้รับผิดชอบ/วันส่ง คอมเมนต์ ทุกอย่างต้องกด "ยืนยัน" ก่อน
- โปรเจกต์ปัจจุบัน: ไม่ต้องพิมพ์ชื่อโปรเจกต์ทุกครั้ง (รหัสงานในข้อความ > ชื่อโปรเจกต์ในข้อความ > โปรเจกต์ปัจจุบัน > ถามด้วยปุ่ม)
- สรุปเช้า จ-ศ 08:30 (คำนวณจาก Plane ไม่เรียก AI ไม่เสียเงิน)
- Dashboard: burndown, งานเพิ่มกลาง sprint, งานค้างนิ่งเกิน 5 วัน, module เทียบกำหนด, workload, bug ตาม priority
- ประวัติย้อนหลัง (ต้องมี PostgreSQL): กราฟแนวโน้มงานเปิด/เลยกำหนด/Blocked/ค้างนาน 30 วัน–6 เดือน, log ทุกอย่างที่บอทแก้ใน Plane, ยอดใช้ Claude รายเดือน
- Rich menu 6 ปุ่ม ติดตั้งให้อัตโนมัติตอนเริ่มระบบ

## สิ่งที่เพิ่มในรอบ 2
- **หน้าตั้งค่า** `/settings?t=DASHBOARD_TOKEN` (พิมพ์ "ตั้งค่า" ในแชตเพื่อรับลิงก์): เชื่อม/เปลี่ยนบัญชี Plane, ใช้ระบบงานในตัวบอท, Claude API key, Google, Notion, โปรเจกต์ที่ติดตาม, ชื่อเล่น, เวลาแจ้งเตือน, ส่งออกและลบข้อมูล ค่าลับเก็บแบบเข้ารหัส (AES-256-GCM) ไม่ต้องแก้ env
  - เปลี่ยน Server ของ Plane ต้องใส่ token ใหม่เสมอ token เดิมจะไม่ถูกส่งไป server ใหม่
  - ประวัติ, risk log, CR, งวดงาน แยกตาม workspace ย้ายบริษัทแล้วไม่ปนกัน
- **Risk log / การตัดสินใจ / Change request** (ประเมินผลต่องวดงาน, สร้างเอกสาร CR ใน Notion ได้)
- **งวดงานและ TOR**: ความคืบหน้า module ในงวด รายการส่งมอบที่ยังไม่มีงาน เตือนล่วงหน้า 14/7/3 วัน
- **เตือนกำหนดส่งตามสัญญา**: module ที่ใกล้กำหนดแต่ความคืบหน้าตามหลัง
- **เตือนความจำ** ("พรุ่งนี้ 10 โมงเตือนตาม FDA-98") เลื่อนได้ 1 วัน
- **ทวงอัปเดต** งานนิ่งนาน ทุกสัปดาห์ (กดยืนยันก่อนคอมเมนต์)
- **Man-day เทียบงบ** จาก effort ที่บันทึกผ่านแชต และ **กำลังคน** (หักวันลาจาก Google Calendar)
- **รูปเป็นงาน**: ส่งรูปไวท์บอร์ด/สมุด บอทอ่านแล้วส่งการ์ดสร้างงาน
- **รายงานประจำสัปดาห์** ร่างอัตโนมัติ ดูฉบับเต็มบนเว็บ กดส่งทาง Gmail
- **Retro** เมื่อ sprint จบ บันทึกลง Notion ได้
- **Google Workspace**: หาเวลาว่าง นัด Google Meet ส่ง invite, ประชุมวันนี้ในสรุปเช้า, สรุปก่อนประชุม 15 นาที, ส่งอีเมล
- **Notion**: ค้นข้อตกลง/จดประชุม, หลังประชุมเสนอร่าง minute ส่งอีเมลถึงผู้เข้าประชุม แล้วสร้าง action items เป็นงาน

## ตั้งค่า
1. **LINE OA** → LINE Developers → Provider → Messaging API channel
   - คัดลอก *Channel secret* และออก *Channel access token (long-lived)*
   - Webhook URL: `https://<โดเมน>/webhook` เปิด *Use webhook* ปิด *Auto-reply* และ *Greeting* ใน LINE OA Manager
2. **Plane** → Profile settings → API tokens → สร้าง token
3. **Claude** → console.anthropic.com → API keys
4. ใส่ค่าตาม `.env.example` (บน Railway ใส่ที่ Variables) ค่า Plane และ Claude ใส่ทีหลังในหน้าตั้งค่าได้
   - ตั้ง `ENCRYPTION_KEY` เป็นค่าสุ่มยาว ๆ และอย่าเปลี่ยน (ถ้าเปลี่ยน ต้องใส่ token ในหน้าตั้งค่าใหม่)
   - Google: หน้าตั้งค่ามีขั้นตอนสร้าง OAuth client พร้อม redirect URI ให้คัดลอก
   - Notion: สร้าง Internal integration แล้วเพิ่ม integration ในหน้าที่ให้บอทอ่าน
5. ทักบอทใน LINE ครั้งแรก บอทจะตอบ user ID กลับมา → ใส่ `OWNER_LINE_USER_ID` แล้ว redeploy
6. (ทางเลือก) Plane → Workspace settings → Webhooks → `https://<โดเมน>/plane/webhook` แล้วใส่ secret ที่ `PLANE_WEBHOOK_SECRET`

## โครงสร้างโค้ด (Go, clean architecture)
```
cmd/server        จุดเริ่มโปรแกรม: ประกอบทุกส่วนเข้าด้วยกัน, HTTP server, scheduler ทุกนาที
cmd/richmenu      ติดตั้ง rich menu ใหม่ทันที
internal/domain   กติกาธุรกิจล้วน ๆ ไม่มี I/O: งาน, metrics, sprint, risk/CR/งวดงาน, วันที่ไทย, state
internal/port     interface ที่ use case ต้องใช้ (TaskProvider, RecordRepo, LLM, Messenger, GoogleAPI ...)
internal/usecase  การทำงานของบอท: agent กับ Claude, tools, การ์ดยืนยัน, สรุปเช้า, แจ้งเตือน, หน้าตั้งค่า
internal/adapter  ตัวเชื่อมภายนอก: plane, builtin, postgres, filestore, google, notion, anthropic, line, crypto
internal/delivery/httpapi  route ของ LINE webhook, Plane webhook, Dashboard, หน้าตั้งค่า, OAuth
web/              dashboard.html, settings.html, richmenu.png (ฝังใน binary)
```
ทิศทางการพึ่งพา: `delivery → usecase → port ← adapter` และทุกชั้นใช้ `domain` ได้ use case ไม่ import adapter ตรง ๆ

## คำสั่ง
ต้องมี Go 1.24 ขึ้นไป
```
go run ./cmd/server          # รันในเครื่อง (อ่าน .env ถ้ามี)
go test ./...                # test แบบ offline (mock Plane / LINE / Claude)
TEST_DATABASE_URL=postgres://... go test ./...   # ทดสอบส่วน PostgreSQL ด้วย
go vet ./...
go run ./cmd/richmenu        # ติดตั้ง rich menu ใหม่ทันที
docker build -t pm-assistant .   # Railway build จาก Dockerfile นี้
```
ข้อมูลเดิมใช้ต่อได้ทันที: env ชื่อเดิม ตาราง PostgreSQL เดิม และรูปแบบการเข้ารหัสเดิม (ไม่ต้องใส่ token ใหม่)

## ความปลอดภัย
- ใช้ได้เฉพาะ `OWNER_LINE_USER_ID` คนอื่นจะได้ข้อความ "ใช้งานส่วนตัว"
- Dashboard ต้องมี `?t=DASHBOARD_TOKEN` อย่าแชร์ลิงก์ ถ้าหลุดให้เปลี่ยนค่าแล้ว redeploy (rich menu จะอัปเดตลิงก์ให้เอง)
- งานที่แก้ข้อมูล คอมเมนต์ ส่งอีเมล นัดประชุม บันทึกลง Notion ต้องกดยืนยันทุกครั้ง (ยกเว้นเตือนความจำ ซึ่งอยู่ในบอทเอง)
- หน้าตั้งค่าใช้ token เดียวกับ Dashboard และไม่เคยส่ง token/API key กลับไปที่เบราว์เซอร์ (แสดงแค่ 4 ตัวท้าย)
- LINE token/secret และ `OWNER_LINE_USER_ID` อยู่ใน env เท่านั้น

## ข้อจำกัดที่ควรรู้
- ข้อความที่บอทส่งเอง (สรุปเช้า แจ้งเตือน รายงาน) นับโควตา push ของ LINE OA แผนฟรีมีจำกัดต่อเดือน
- Man-day คิดจากเวลาที่บันทึกผ่านบอท ยังไม่ได้ดึง worklog จาก Plane
- กำลังคนนับงานละ 1 วัน (ยังไม่ใช้ estimate)

## รอบถัดไป
บอทในกลุ่ม LINE, Jira / ClickUp / Linear (เพิ่ม adapter ที่ implement `port.TaskProvider` ใน `internal/adapter/`)
