# 12 — Backend Architecture (Full P1+P2+P3, trừ Conversation Live)

> Trạng thái: Draft v0.1 · Liên quan: `04-functional-requirements.md`, `06-business-rules.md`, `07-nfr-architecture.md`, `08-data-model.md`
>
> **Phạm vi:** Toàn bộ sản phẩm (P1 + P2 + P3) **TRỪ Conversation Live** (FR-CONV-01..05, màn S29/S30/S31).
> Bao gồm Exam Mode (Speechace). Conversation để dành hook tích hợp sau.

---

## 0. Nguyên tắc thiết kế

1. **Hybrid scoring theo mode** (xem §3b): Azure SDK chạy **client** cho Word/Sentence/MP/Shadowing (latency là yêu cầu cứng NFR-PERF-01); **Exam (Speechace) và phần dính tiền/gating chấm/verify server-side**. Backend KHÔNG proxy audio realtime cho mode client.
2. **Token broker bắt buộc** (§3b.1): client KHÔNG bao giờ nhúng key Azure/Speechace — chỉ nhận short-lived token từ backend.
3. **Trust boundary** (§3b.2): điểm client gửi về là **untrusted input** — backend sanity-check trước khi đổ vào Error Profile; quyết định dính tiền (Exam band, freemium quota, streak) **luôn ở server**.
4. **Speech Engine Abstraction** (NFR-MAINT-01): mọi kết quả chấm điểm đi qua 1 ingestion contract chung. Hôm nay Azure (Word/Sentence/MP/Shadowing), Exam = Speechace — đều "đổ" về cùng format nội bộ. Ingestion generic giúp đổi vendor mà không sửa core.
5. **Error Profile là tài sản lõi** → tách thành domain riêng, ingestion event-driven, dễ mở rộng cho Conversation sau.
6. **Content versioned, tách khỏi code** (NFR-MAINT-02): ContentItem, L1ErrorTag, FixGuide do linguist quản lý.
7. **Cache mạnh** (Redis + object storage): TTS audio (~$0), freemium counter, streak, rate-limit.
8. **Modular monolith** trước (1 deploy, nhiều module rõ ranh giới) → tách microservice sau khi có tải. Tránh over-engineer ở giai đoạn này.

---

## 1. Sơ đồ kiến trúc tổng (high-level)

```
                         ┌──────────────────────────────────────┐
                         │         Mobile App (RN/Flutter)        │
                         │  UI · Recording · Azure Speech SDK     │
                         │  (Word/Sentence/MP/Shadowing PA + TTS) │
                         │  Speechace SDK/client (Exam)           │
                         └───────────┬───────────────┬───────────┘
                                     │ HTTPS (REST/JSON)
                       ┌─────────────▼───────────────▼──────────────┐
                       │            API Gateway / Edge               │
                       │  TLS · Auth (JWT) · Rate-limit · Routing    │
                       └─────────────┬──────────────────────────────┘
                                     │
         ┌───────────────────────────┼───────────────────────────────────┐
          │            Backend (Go · Echo · Modular Monolith)               │
         │                                                                 │
         │  ┌────────────┐ ┌────────────┐ ┌───────────────┐ ┌───────────┐ │
         │  │  Identity  │ │  Speech    │ │ Error Profile  │ │  Content  │ │
         │  │  & Profile │ │ Gateway/   │ │  Engine (LÕI)  │ │  Service  │ │
         │  │  & Sync    │ │ Token      │ │  + Coach       │ │           │ │
         │  └────────────┘ │ Broker +   │ └───────┬───────┘ └─────┬─────┘ │
         │  ┌────────────┐ │ Ingestion  │         │                 │      │
         │  │ Practice   │ └─────┬──────┘         │                 │      │
         │  │ Session    │       │                │                 │      │
         │  │ Service    │◀──────┘ (kết quả PA)    │                 │      │
         │  └─────┬──────┘                         │                 │      │
         │  ┌─────▼──────┐ ┌────────────┐ ┌────────▼──────┐ ┌────────▼────┐│
         │  │ Shadowing  │ │ Streak &   │ │  Progress &   │ │ TTS Service ││
         │  │ Progress   │ │ Gamific.   │ │  Reports      │ │ (gen+cache) ││
         │  └────────────┘ └────────────┘ └───────────────┘ └─────────────┘│
         │  ┌────────────┐ ┌────────────┐ ┌───────────────┐ ┌─────────────┐│
         │  │ Subscription│ │ Daily      │ │ Notification  │ │ Exam        ││
         │  │ & Freemium │ │ Challenge  │ │ Service       │ │ Service     ││
         │  │ + IAP      │ │ (P2)       │ │ (push, P2)    │ │ (Speechace) ││
         │  └────────────┘ └────────────┘ └───────────────┘ └─────────────┘│
         └────────┬───────────────────┬──────────────────┬────────────────┘
                  │                   │                  │
         ┌────────▼──────┐   ┌────────▼──────┐   ┌────────▼────────┐
         │  PostgreSQL   │   │     Redis     │   │  Object Storage  │
         │  (lõi dữ liệu)│   │ cache/counter │   │ (audio TTS + ghi)│
         └───────────────┘   └───────────────┘   └─────────────────┘

         External: Azure Speech (PA+TTS) · Speechace (Exam) ·
                   Apple/Google IAP · APNs/FCM (push)
         [Hook để dành: Conversation pipeline STT→LLM→TTS — chưa build]
```

---

## 2. Danh mục module (bounded contexts)

| # | Module | Phase | Trách nhiệm chính | FR/BR liên quan |
|---|---|---|---|---|
| M1 | **Identity & Profile & Sync** | P1 | Auth (email/Google/Apple), guest, JWT, profile (goal/level/accent/daily_goal/default_scoring_level), đa thiết bị, xóa tài khoản | FR-ACC-01..05, FR-ONB-01..05, NFR-REL-04, NFR-SEC-03 |
| M2 | **Speech Gateway** | P1 | Token broker (Azure/Speechace short-lived token), **Result Ingestion** (nhận PA result từ client), abstraction layer | FR-ENG-02, NFR-MAINT-01 |
| M3 | **Practice Session Service** | P1 | Tạo/quản lý PracticeSession + PracticeItemResult + PhonemeScore, áp Level (Dễ/Vừa/Khó), gating số bài/ngày | FR-ENG-05, FR-LEVEL-01..08, FR-WORD/SENT/MP |
| M4 | **Error Profile Engine + Coach** | P1 | EWMA mastery (phoneme/pair/skill), top_errors, map L1ErrorTag, đề xuất bài, link Minimal Pairs | FR-COACH-01..06, BR-COACH, BR-L1 |
| M5 | **Content Service** | P1 | ContentItem, MinimalPair, L1ErrorTag, FixGuide, ShadowingPassage/PassageSentence; versioned; đề xuất theo goal/topic | FR-WORD-01, FR-MP-01, FR-SHAD, BR-CONT, NFR-MAINT-02 |
| M6 | **TTS Service** | P1 | Pre-generate Azure Neural TTS (SSML) US/UK, cache vào object storage, gắn audio_url vào content | FR-ENG-04, BR-CONT-01, NFR-PERF-03 |
| M7 | **Subscription & Freemium + IAP** | P1 | Gating N bài/ngày, verify IAP receipt (App/Google), plan/status/renews_at, Exam Pack add-on | FR-PAY-01..06, BR-FREE, BR-PAY |
| M8 | **Streak & Gamification** | P1/P2 | StreakRecord theo timezone, DailyProgress, badge/XP | FR-DAILY-02..03, BR-STREAK |
| M9 | **Shadowing Progress** | P2 | Tiến độ passage theo câu, ngưỡng 80%, skip sau N lần, tổng kết đoạn | FR-SHAD-01..10, BR-SHAD |
| M10 | **Daily Challenge** | P2 | Sinh nội dung ngày (tin tức/hội thoại) + kiểm duyệt, gắn Shadowing | FR-DAILY-01, BR-CONT-04 |
| M11 | **Progress & Reports** | P1/P2 | Tổng quan tiến độ, lịch sử, biểu đồ cải thiện theo tuần/tháng | FR-PROG-01..03, FR-COACH-05 |
| M12 | **Notification Service** | P2 | Push nhắc luyện điểm yếu / giữ streak (APNs/FCM) | FR-COACH-06, FR-DAILY-04 |
| M13 | **Exam Service** | P3 | IELTS/TOEIC format, tích hợp Speechace, band score, báo cáo tiêu chí | FR-EXAM-01..03 |

> **KHÔNG build:** Conversation Service (M? — pipeline STT→LLM→TTS). Để hook ingestion generic ở M2/M4 để sau gắn vào.

---

## 3. Ranh giới Client ↔ Backend (quan trọng)

| Việc | Chạy ở đâu | Lý do |
|---|---|---|
| Ghi âm + chấm PA (Azure) | **Client** (SDK) | Latency thấp (NFR-PERF-01), không proxy audio |
| Cấp token Azure/Speechace | **Backend** (M2) | Không nhúng API key vào app (NFR-SEC-03) |
| Nhận & lưu kết quả PA | **Backend** (M2→M3) | Nguồn cập nhật Error Profile |
| Tính Error Profile / đề xuất | **Backend** (M4) | Tài sản lõi, đa thiết bị |
| Chấm Exam (Speechace) | **Backend chấm/verify server-side** | Band score nhạy cảm, dính tiền (Exam Pack) → không tin client (§3b.2) |
| TTS audio | **Backend** pre-gen + **client** phát từ URL cache | Chi phí ~$0 (BR-CONT-01) |
| Gating freemium | **Backend** (M7) — bắt buộc | Không tin client cho quota |
| Streak counter | **Backend** (M8) theo timezone | Tránh gian lận |

---

## 3b. Hybrid scoring & Trust boundary (bổ sung — quan trọng)

> Quyết định "chạy SDK ở client" là **hợp lý cho latency nhưng có đánh đổi**. Không áp dụng đồng nhất cho mọi mode — chọn theo đặc tính từng mode và mức nhạy cảm tiền bạc.

### 3b.0a. Hai việc tách biệt: "chấm PA" vs "tính đạt/không đạt" (đọc kỹ — tránh nhầm)

> **"Client gọi Azure" KHÔNG phải "client tự chấm điểm".** Đây là 2 công đoạn nối tiếp khác nhau:
> - **CHẤM PA (Azure làm):** Azure trả **số thô** — từng âm chính xác %, NBest "nói âm gì", từ nào thiếu. Azure KHÔNG trả "đạt/không đạt".
> - **TÍNH ĐẠT/KHÔNG (Server làm):** áp Level lên số thô → ra điểm câu + quyết pass/retry. Cùng 1 dữ liệu Azure, Level khác → kết quả khác.

```
[Client] thu âm ──audio (nặng, realtime)──▶ [Azure Speech]
                                                   │  PA THÔ (JSON nhẹ):
                                                   │  "walked" → /w/95 /ɔː/90 /k/88 /t/40
                                              ◀────┘
[Client] ──PA thô + scoring_level (vài KB)──▶ [Server]
                                                   │  ÁP LEVEL:
                                                   │   • Dễ : bỏ đuôi -ed → /t/ yếu KHÔNG tính → ĐẠT
                                                   │   • Khó: tính /t/ cuối → /t/40 → KHÔNG ĐẠT
                                                   │  → điểm câu, quyết pass/retry (80%), lưu DB
                                              ◀────┘  → cập nhật Error Profile
```

| Bước | Ai làm | Vì sao |
|---|---|---|
| Thu âm | Client | mic |
| **Chấm PA (ra số thô)** | **Azure** (client gọi trực tiếp) | Audio nặng + realtime → đi thẳng client→Azure (NFR-PERF-01); server KHÔNG đụng audio |
| Gửi PA thô về | Client → Server | JSON nhẹ vài KB |
| **Áp Level → đạt/không đạt** | **Server** | Chống gian lận tiến độ + đổi rule không update app (§3b.2) |
| Lưu + Error Profile | Server | DB |

> **Hệ quả:** vì client vẫn **gọi Azure trực tiếp** (cầm token) → rủi ro token lộ vẫn còn → **§3c (phòng thủ chi phí) vẫn đúng & bắt buộc**. Việc server tính điểm không thay đổi điều này.

### 3b.0. Hybrid theo mode

| Mode | Vị trí chấm | Lý do |
|---|---|---|
| **Word / Sentence / Minimal Pairs** | **Client** (token broker) → ingest kết quả về backend | Latency cứng (NFR-PERF-01); gian lận điểm ít hệ quả tiền bạc |
| **Shadowing** | **Client** | Chấm theo câu, realtime (BR-SHAD) |
| **Exam (Speechace)** | **Server-side / verify lại** | Band score nhạy cảm, dính Exam Pack (BR-PAY-06) |
| **Conversation (chưa build)** | Server pipeline | Bản chất server-side |

### 3b.1. Token broker (bắt buộc — bảo mật key)

- App **KHÔNG nhúng** subscription key Azure/Speechace (decompile lấy được → đốt tiền).
- `POST /v1/speech/token` cấp **short-lived token TTL 30–60 giây** (KHÔNG 10 phút), gắn **1 session_id cụ thể**, scope tối thiểu, không tái sử dụng cho session khác.
- Token broker là nơi áp **quota/rate-limit tập trung** (NFR-SCALE-02): từ chối cấp token khi vượt freemium (BR-FREE-01) hoặc bật/tắt prosody chọn lọc (BR-SCORE-08).
- **Đây là chốt chặn chi phí chính** — chi tiết phòng thủ 7 lớp ở §3c.

### 3b.2. Trust boundary (chống bịa điểm — bảo vệ moat)

Điểm client gửi qua `POST /v1/sessions/{id}/results` là **untrusted input**. Backend phải:

1. **Sanity-check** trước khi đổ vào Error Profile:
   - điểm trong [0..100]; phoneme khớp `content_item_id` đã phát; số phoneme hợp lệ.
   - reject/flag kết quả bất thường (vd toàn 100 liên tục, timing phi lý).
2. **Quyết định dính tiền/gating LUÔN ở server**, không để client tự quyết:
   - Exam band score → server chấm/verify (3b.0).
   - freemium quota (M7), streak check-in (M8) → server-side, theo timezone.
3. **Audit định kỳ:** `audio_ref` được lưu (BR-PRIV) → có thể **re-score server-side mẫu** để phát hiện gian lận / kiểm chứng chất lượng engine.
4. **Anti-abuse:** rate-limit ingestion; phát hiện pattern gửi result không kèm phiên token hợp lệ.

### 3b.3. Đánh đổi đã chấp nhận

| Đánh đổi | Giảm thiểu |
|---|---|
| Vendor lock-in client (NFR-MAINT-01) — đổi engine phải update app | Giữ **ingestion contract generic**; đổi vendor chỉ ảnh hưởng lớp lấy token + SDK client, core M4 không đổi |
| **Khó kiểm soát chi phí khi client gọi thẳng** — token lộ + spam = hóa đơn Azure khổng lồ | **Phòng thủ chi phí 7 lớp — §3c (BẮT BUỘC)** |
| Client có thể bịa điểm | Trust boundary (3b.2): untrusted input + server quyết phần tiền |

---

## 3c. Phòng thủ chi phí — Defense in Depth (BẮT BUỘC)

> **Rủi ro nặng nhất của kiến trúc client-side:** token bị trích từ traffic → attacker spam Azure → hóa đơn hàng nghìn USD trước khi phát hiện. Token broker một mình KHÔNG đủ. Áp dụng đủ 7 lớp dưới đây. Phương án chốt: **Hybrid client-side + 7 lớp phòng thủ.**

### Lớp 1 — Token scope hẹp + TTL cực ngắn
- TTL **30–60 giây**, gắn **1 session_id**, chỉ chấm được đúng nội dung của session đó.
- Hết hạn ngay sau 1 lần chấm → cửa sổ tấn công tối thiểu.

### Lớp 2 — Rate-limit tầng cấp token (chốt chặn chính)
> Attacker buộc phải qua **API của bạn** để xin token ⇒ đây là điểm kiểm soát mạnh nhất.
- Giới hạn **token/user/phút** (vd 1 token / 10s).
- Giới hạn **token/IP/phút** (chống 1 attacker tạo nhiều account).
- **Free tier: số token cấp = số bài còn lại** (BR-FREE-01). Hết quota → KHÔNG cấp token → **không thể gọi Azure**. Đây là cơ chế chặn chi phí cốt lõi.

### Lớp 3 — Hard budget cap ở Azure (lưới an toàn cuối)
- **Azure Cost Management budget + auto-action**: đạt ngưỡng $X/ngày → tự động disable resource / trigger webhook khóa cấp token.
- **Tách key theo tier**: key free-tier có spending limit thấp, tách hẳn key paid.
- Nguyên tắc: dù mọi lớp trên thất bại, **thiệt hại vẫn có trần cứng**.

### Lớp 4 — App attestation (chống token-stealer / script)
- **App Attest (iOS)** + **Play Integrity (Android)**: token broker verify request đến từ app thật, chưa bị tamper.
- Fail attestation → từ chối cấp token ⇒ script/emulator/curl không lấy được token.

### Lớp 5 — Anomaly detection + circuit breaker
- Monitor real-time: token/phút, phút-audio/user/giờ.
- 1 user xin token bất thường → **auto-suspend** + alert.
- **Global circuit breaker**: tổng chi phí/giờ vượt ngưỡng → tạm dừng cấp token toàn hệ thống + báo on-call.

### Lớp 6 — Server-side proxy cho high-risk (tùy chọn nâng cấp)
- Phần nhạy cảm KHÔNG cấp token cho client mà **proxy audio qua backend** → kiểm soát chi phí tuyệt đối (đếm từng request).
- Mặc định áp cho **Exam** (đã server-side). Có thể bật cho Free tier nếu lạm dụng tăng.

### Lớp 7 — Billing alert đa tầng
- Alert ở $50 / $100 / $500/ngày qua **nhiều kênh** (email + Slack + SMS).
- Daily cost report tự động + dashboard chi phí/engine (NFR-MAINT-03).

### Bảng tóm tắt 7 lớp

| Lớp | Cơ chế | Chặn được gì |
|---|---|---|
| 1 | Token TTL 30–60s + gắn session | Token lộ chỉ dùng được trong giây lát |
| 2 | Rate-limit cấp token + quota | Spam xin token / vượt free tier |
| 3 | Azure budget cap + auto-disable | Trần cứng thiệt hại tối đa |
| 4 | App Attest / Play Integrity | Script/emulator không lấy được token |
| 5 | Anomaly detect + circuit breaker | Phát hiện & cắt tấn công đang diễn ra |
| 6 | Server proxy cho high-risk | Kiểm soát tuyệt đối phần nhạy cảm |
| 7 | Billing alert đa kênh | Phát hiện sớm để can thiệp thủ công |

> **Ưu tiên triển khai:** Lớp 1, 2, 3, 7 là **MVP bắt buộc** (chặn được phần lớn rủi ro với chi phí thấp). Lớp 4, 5 thêm khi có user thật. Lớp 6 là phương án dự phòng khi lạm dụng tăng.

---

## 4. API Contract chính (REST, JSON)

> Tiền tố `/v1`. Auth qua `Authorization: Bearer <JWT>`. Tất cả endpoint TLS.

### 4.1. Identity & Profile (M1)
```
POST   /v1/auth/register            { provider, email?, id_token? }
POST   /v1/auth/login               -> { access_token, refresh_token }
POST   /v1/auth/refresh
POST   /v1/auth/guest               -> tạo guest, giới hạn (BR-FREE-04)
GET    /v1/me                       -> profile
PATCH  /v1/me                       { goal, level, target_accent, daily_goal, default_scoring_level }
DELETE /v1/me                       -> xóa tài khoản + data + audio (BR-PRIV-02)
POST   /v1/me/sync                  -> đồng bộ tiến độ đa thiết bị (NFR-REL-04)

# Notification / reminder preferences (S08, S22) — đồng bộ đa thiết bị + phục vụ M12
GET    /v1/me/notifications         -> { practice_reminder:{enabled,time}, streak_reminder:{enabled} }
PATCH  /v1/me/notifications         { practice_reminder?, streak_reminder? }
POST   /v1/me/devices               { push_token, platform } -> đăng ký APNs/FCM token (M12)

# Privacy consent toggles (S23, BR-PRIV-01/03) — chi phối vòng đời audio_ref
GET    /v1/me/privacy               -> { allow_recording_for_improvement, save_recording_history }
PATCH  /v1/me/privacy               { allow_recording_for_improvement?, save_recording_history? }
```

### 4.2. Speech Gateway (M2)
```
POST   /v1/speech/token             { engine: azure|speechace, session_id }
       -> token TTL 30-60s gắn session (§3c L1); kèm App Attest/Play Integrity (§3c L4);
          từ chối nếu hết quota free tier hoặc vượt rate-limit (§3c L2)
POST   /v1/sessions/{id}/results    <- INGESTION: client gửi PA THÔ (Azure), server tính
       body: {
         content_item_id, scoring_level,            -- server áp Level, KHÔNG tin điểm client
         pa_raw: {
           word_scores: [{ word, accuracy, error_type, word_index }],
           phonemes:    [{ expected, said(NBest), accuracy, word_index, phoneme_index }],
           fluency, completeness, prosody            -- trục cấp câu (Azure cung cấp)
         },
         audio_ref?
       }
       -> SERVER áp Level (bỏ đuôi -s/-es/-ed, BR-LEVEL-02..04) → tính accuracy/completeness câu
       -> lưu PracticeItemResult + PhonemeScore (đầy đủ, BR-LEVEL-05) → trust sanity-check (§3b.2)
       -> trigger Error Profile recompute (async event)
```

### 4.3. Practice Session (M3)
```
POST   /v1/sessions                 { mode, source, scoring_level } -> session_id (gating check)
GET    /v1/sessions/{id}
POST   /v1/sessions/{id}/end        -> summary_score
GET    /v1/sessions/history         -> FR-PROG-02
```

### 4.4. Content (M5)
```
GET    /v1/content/words?topic=&goal=&phoneme=     (FR-WORD-07)
GET    /v1/content/sentences?topic=&goal=
GET    /v1/content/minimal-pairs?l1_tag=
GET    /v1/content/passages?source=&difficulty=    (Shadowing)
GET    /v1/content/passages/{id}/sentences
GET    /v1/content/fix-guide?phoneme=&l1_tag=       (FR-WORD-05)
```

### 4.5. Error Profile & Coach (M4)
```
GET    /v1/coach/profile            -> top_errors + phoneme/pair/skill mastery (S19)
GET    /v1/coach/recommendation     -> bài đề xuất ưu tiên (S09 hero, FR-COACH-03)
GET    /v1/coach/report?period=week|month   -> FR-COACH-05 (S28)
```

### 4.6. Shadowing (M9)
```
GET    /v1/shadowing/{passage_id}/progress
POST   /v1/shadowing/{passage_id}/sentence-result
       { order_index, scoring_level, pa_raw: { word_scores[], phonemes[] }, audio_ref? }
       -> SERVER áp Level (bỏ đuôi -s/-es/-ed theo BR-LEVEL-02..04) → tính điểm câu
          → quyết next/retry (ngưỡng 80%, BR-SHAD-02) + cho skip sau N lần (BR-SHAD-04)
          → lưu phoneme đầy đủ bất kể Level (BR-LEVEL-05)
POST   /v1/shadowing/{passage_id}/complete         -> tổng kết passage
```
> **Lưu ý Level (§3b.2 trust boundary):** client KHÔNG tự tính điểm câu mà gửi **PA thô** (word/phoneme từ Azure) + `scoring_level`. **Server** áp Level và quyết ngưỡng pass — chống gian lận tiến độ + logic bỏ đuôi tập trung 1 chỗ (đổi rule không phải update app).
>
> **Chuyển toggle Dễ/Vừa/Khó trên UI KHÔNG gọi backend** — chỉ đổi state client + caption rule. Level chỉ đi kèm request khi chấm câu.

### 4.6b. Minimal Pairs Listen drill (M6, FR-MP-02, S16) — bổ sung
```
POST   /v1/minimal-pairs/listen/start  { count } -> drill (hearts=3, progress)
POST   /v1/minimal-pairs/listen/{drill_id}/answer  { pair_id, chosen_word }
       -> chấm đúng/sai, trừ heart, cập nhật pair_mastery.listen
GET    /v1/minimal-pairs/listen/{drill_id}         -> trạng thái drill
```

### 4.7. Streak / Progress (M8/M11)
```
GET    /v1/progress/overview        -> S20 (streak, longest_streak, điểm, bài đã luyện)
GET    /v1/progress/charts?period=  -> dùng mastery_snapshots (score trend)
GET    /v1/sessions/history         -> lịch sử + signed URL replay audio (consent-gated)
GET    /v1/badges                   -> earned/locked + progress (S20)
POST   /v1/streak/check-in          (server-side, theo timezone)
```

### 4.8. Subscription & IAP (M7)
```
GET    /v1/subscription             -> plan/status/renews_at
GET    /v1/subscription/plans       -> pricing VND + features (paywall S21, BR-PAY-03)
POST   /v1/subscription/verify      { store, receipt } -> verify IAP (BR-PAY-02)
POST   /v1/subscription/restore     -> RESTORE PURCHASE (store bắt buộc, FR-PAY-02)
POST   /v1/iap/webhook/apple        (server notifications)
POST   /v1/iap/webhook/google       (RTDN)
GET    /v1/freemium/quota           -> bài còn lại hôm nay (BR-FREE-01)
```

### 4.9. Daily Challenge (M10)
```
GET    /v1/daily/today              -> nội dung ngày (FR-DAILY-01)
GET    /v1/daily/history            -> "Thử thách gần đây" + completion (S27)
```

### 4.9b. Account / Privacy / Support / Config (M11b) — bổ sung
```
POST   /v1/me/export               -> export toàn bộ dữ liệu (GDPR, BR-PRIV-02)
DELETE /v1/me/history              -> xóa lịch sử luyện (giữ account)
POST   /v1/feedback                { type, message, context } -> feedback/bug (S22/S24)
GET    /v1/app-config              -> min_version + feature flags ("Sắp có")
GET    /v1/legal/{terms|privacy}   -> legal versioned (S02/S23)
GET    /v1/sessions/in-progress    -> resume "Tiếp tục 2/8" (S09b)
GET    /v1/events                  (POST) -> analytics ingestion (doc 10)
```

### 4.10. Exam (M13) — server-side scoring (§3b.2)
```
GET    /v1/exam/prompts?type=ielts|toeic&part=
POST   /v1/exam/sessions            -> exam session (gating Exam Pack, BR-PAY-06)
POST   /v1/exam/sessions/{id}/submit  { audio_ref }  -> backend gọi Speechace, KHÔNG nhận band từ client
GET    /v1/exam/sessions/{id}/report  -> band score + CEFR + tiêu chí (S33)
GET    /v1/exam/sessions              -> lịch sử exam report (save/share, S33)
```
> Khác mode thường: client upload `audio_ref`, **backend tự chấm qua Speechace** rồi trả band — tránh client bịa band score.

---

## 5. Error Profile Engine — thiết kế chi tiết (LÕI)

Đây là phần đầu tư nhiều nhất. Event-driven để dễ gắn Conversation sau.

```
PracticeItemResult + PhonemeScore  (ingest từ M2)
        │  (emit event: PracticeResultRecorded)
        ▼
┌─────────────────────────────────────────────┐
│ Error Profile Engine (M4)                    │
│                                              │
│ 1. Cập nhật PhonemeMastery (EWMA, BR-COACH-02)│
│ 2. Map NBest said≠expected → L1ErrorTag      │
│    (BR-L1-03): nếu (said,expected) ∈ cặp L1  │
│    → cập nhật PairMastery + gợi ý MinimalPair │
│ 3. Omission ở phụ âm cuối → final_consonant  │
│    deletion (BR-L1-04)                        │
│ 4. Cập nhật SkillMastery (prosody/fluency...) │
│ 5. Re-tính top_errors (denormalize, S07/S19) │
│ 6. status: weak<60 / improving / good≥80      │
│    (BR-COACH-03)                              │
│ 7. Recommendation engine (BR-COACH-04):       │
│    rank = severity × frequency × L1_importance│
│           × goal_fit; anti-fatigue (BR-COACH-07)│
└─────────────────────────────────────────────┘
        │
        ▼
   Cache top_errors + recommendation vào Redis (đọc nhanh cho S09 hero)
```

**Tính đồng bộ:** ingestion ghi DB ngay (không mất dữ liệu BR-QA-03), recompute mastery chạy async (queue) để không chặn response client. Recommendation cache TTL ngắn, invalidate khi có result mới.

---

## 6. Hạ tầng dữ liệu

| Tầng | Dùng cho | Ghi chú |
|---|---|---|
| **PostgreSQL** | User, ErrorProfile, PhonemeMastery/PairMastery/SkillMastery, PracticeSession/ItemResult/PhonemeScore, Content*, MinimalPair, L1ErrorTag, FixGuide, ShadowingPassage/Sentence/Progress, Subscription, StreakRecord/DailyProgress | Schema theo `08-data-model.md` |
| **Redis** | freemium counter/ngày, streak cache, rate-limit, top_errors + recommendation cache, audio meta | TTL theo từng loại |
| **Object Storage (S3-like)** | audio TTS cached (US/UK) + bản ghi user (audio_ref) | audio_ref tuân thủ BR-PRIV, xóa khi xóa account |
| **Queue (Redis/SQS)** | recompute Error Profile async, TTS pre-gen batch, IAP webhook xử lý | |

---

## 7. Bảo mật & vận hành (NFR)

| Hạng mục | Giải pháp | NFR |
|---|---|---|
| Truyền/lưu | TLS + at-rest encryption | NFR-SEC-01 |
| Auth | JWT access + refresh, OAuth social, token broker cho engine | NFR-SEC-03 |
| **Token broker** | Short-lived token (~10ph), không nhúng key vào app (§3b.1) | NFR-SEC-03 |
| **Trust boundary** | Điểm client = untrusted: sanity-check + server quyết phần tiền + audit re-score (§3b.2) | NFR-SEC-03, BR-STREAK-04 |
| Chống lạm dụng | Rate-limit per-user (Redis) ở gateway + ingestion | NFR-SEC-03 |
| Quyền riêng tư | audio_ref có vòng đời, xóa theo yêu cầu | NFR-SEC-02, BR-PRIV |
| Uptime | ≥ 99.5%, retry + local fallback ở client | NFR-REL-02/03 |
| Observability | Logging/monitoring/alerting **chi phí engine** (Azure/Speechace) + lỗi | NFR-MAINT-03 |
| Cost control | TTS cache, prosody bật chọn lọc (BR-SCORE-08), freemium gating | NFR-SCALE-02 |
| **Chống đốt chi phí token** | Phòng thủ 7 lớp (§3c): TTL ngắn, rate-limit cấp token, Azure budget cap, attestation, anomaly + circuit breaker, proxy high-risk, billing alert | NFR-SCALE-02, NFR-SEC-03 |

---

## 8. Thứ tự build (3 tầng) — gom vào 1 đợt nhưng có lớp

**Tầng 1 — Nền tảng (chặn các phần khác):**
M1 Identity/Profile/Sync · M2 Speech Gateway (token + ingestion) · Storage · M3 Practice Session + Level Engine · **M4 Error Profile Engine** · M5 Content Service · M6 TTS cache.

**Tầng 2 — Tính năng người dùng:**
5 mode end-to-end · M4 Coach đề xuất · M8 Streak · M7 Freemium + IAP · M9 Shadowing · M11 Progress/Reports · M10 Daily Challenge · M12 Notifications.

**Tầng 3 — Cao cấp:**
M13 Exam (Speechace abstraction) · Exam Pack billing (M7).

**Hook để dành Conversation (không build):** giữ `POST /v1/sessions/{id}/results` generic để pipeline hội thoại sau chỉ cần đổ kết quả PA vào — không sửa core M4.

---

## 9. Khác biệt so với build tuần tự theo phase

| | Build Full (trừ Conv Live) | Build theo phase |
|---|---|---|
| Loại bỏ | Conversation pipeline (đắt + khó nhất) | — |
| Vẫn phải build | Toàn bộ còn lại gồm Exam + Speechace | Theo từng đợt |
| Rủi ro | Scope lớn, time-to-market lâu, validate chậm | Validate sớm từng phase |
| Lợi thế | Sản phẩm gần đầy đủ, mạnh phân khúc luyện thi | An toàn MVP |

> Khuyến nghị: dù gom 1 đợt, **vẫn release nội bộ theo tầng** để kiểm chứng Error Profile (moat) sớm trước khi đổ công vào Exam/Daily.
