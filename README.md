# LINE PM Assistant

ผู้ช่วย PM ใน LINE ที่ต่อกับ Plane (self-hosted) ใช้ Claude Haiku คุยภาษาไทย ถามสถานะ สร้าง/แก้งานแบบมีการ์ดยืนยัน สรุปเช้า 08:30 และมีหน้า Dashboard (เว็บ + LIFF)

## สิ่งที่ทำได้ (รอบ 1)
- ถาม-ตอบสถานะโปรเจกต์ งานเลยกำหนด งานของฉัน งาน Blocked
- สร้างงาน (แตกงานย่อยได้) แก้สถานะ/ผู้รับผิดชอบ/วันส่ง คอมเมนต์ ทุกอย่างต้องกด "ยืนยัน" ก่อน
- โปรเจกต์ปัจจุบัน: ไม่ต้องพิมพ์ชื่อโปรเจกต์ทุกครั้ง (รหัสงานในข้อความ > ชื่อโปรเจกต์ในข้อความ > โปรเจกต์ปัจจุบัน > ถามด้วยปุ่ม)
- สรุปเช้า จ-ศ 08:30 (คำนวณจาก Plane ไม่เรียก AI ไม่เสียเงิน)
- Dashboard: burndown, งานเพิ่มกลาง sprint, งานค้างนิ่งเกิน 5 วัน, module เทียบกำหนด, workload, bug ตาม priority
- ประวัติย้อนหลัง (ต้องมี PostgreSQL): กราฟแนวโน้มงานเปิด/เลยกำหนด/Blocked/ค้างนาน 30 วัน–6 เดือน, log ทุกอย่างที่บอทแก้ใน Plane, ยอดใช้ Claude รายเดือน
- Rich menu 6 ปุ่ม ติดตั้งให้อัตโนมัติตอนเริ่มระบบ

## ตั้งค่า
1. **LINE OA** → LINE Developers → Provider → Messaging API channel
   - คัดลอก *Channel secret* และออก *Channel access token (long-lived)*
   - Webhook URL: `https://<โดเมน>/webhook` เปิด *Use webhook* ปิด *Auto-reply* และ *Greeting* ใน LINE OA Manager
2. **Plane** → Profile settings → API tokens → สร้าง token
3. **Claude** → console.anthropic.com → API keys
4. ใส่ค่าตาม `.env.example` (บน Railway ใส่ที่ Variables)
5. ทักบอทใน LINE ครั้งแรก บอทจะตอบ user ID กลับมา → ใส่ `OWNER_LINE_USER_ID` แล้ว redeploy
6. (ทางเลือก) Plane → Workspace settings → Webhooks → `https://<โดเมน>/plane/webhook` แล้วใส่ secret ที่ `PLANE_WEBHOOK_SECRET`

## คำสั่ง
```
npm install
npm run dev          # รันในเครื่อง
npm test             # self-test แบบ offline (mock Plane)
TEST_DATABASE_URL=postgres://... npm test   # ทดสอบส่วน PostgreSQL ด้วย
npm run typecheck
npm run richmenu     # ติดตั้ง rich menu ใหม่ทันที
```

## ความปลอดภัย
- ใช้ได้เฉพาะ `OWNER_LINE_USER_ID` คนอื่นจะได้ข้อความ "ใช้งานส่วนตัว"
- Dashboard ต้องมี `?t=DASHBOARD_TOKEN` อย่าแชร์ลิงก์ ถ้าหลุดให้เปลี่ยนค่าแล้ว redeploy (rich menu จะอัปเดตลิงก์ให้เอง)
- งานที่แก้ข้อมูลใน Plane ต้องกดยืนยันทุกครั้ง

## รอบถัดไป
Google Workspace: นัดประชุม Google Meet, ส่ง minutes จาก Notion ทางอีเมล แล้วสร้าง action items เป็นงาน, หน้า Settings (LIFF), บอทในกลุ่ม LINE
