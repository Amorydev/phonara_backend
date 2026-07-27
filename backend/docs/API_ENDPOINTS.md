# Phonara API — Tài liệu Endpoint

> **Backend API cho ứng dụng luyện phát âm tiếng Anh Phonara.**
> Tài liệu này liệt kê toàn bộ endpoint đã hiện thực trong `internal/server/server.go`, kèm mô tả chi tiết tác dụng, tham số, và mã trả về.

| | |
|---|---|
| **Title** | Phonara API |
| **Version** | 1.0 |
| **Host (local)** | `http://localhost:8080` |
| **Base path** | `/v1` (trừ probes `/health`, `/ready` và Swagger UI `/swagger/`) |
| **Swagger UI** | `http://localhost:8080/swagger/` |

## Quy ước chung

### Envelope response
Mọi response dùng chung envelope JSON:

```jsonc
// Thành công
{ "data": { /* payload */ } }
// Lỗi
{ "error": "thông điệp lỗi" }
```

Một số list endpoint có phân trang dùng thêm `meta` (`page`, `per_page`, `total_items`, `total_pages`).

### Xác thực
- Header: `Authorization: Bearer <access_token>`.
- `access_token` sống ~15 phút, `refresh_token` ~7 ngày.
- Các endpoint có cột **🔒 Auth = Có** bắt buộc Bearer token (middleware JWT). Endpoint public không cần.

### Mã trạng thái dùng chung
| Code | Ý nghĩa |
|---|---|
| `200` | OK |
| `201` | Created |
| `202` | Accepted (xử lý async — đã nhận, sẽ xử lý sau) |
| `400` | Bad Request (tham số sai định dạng) |
| `401` | Unauthorized (thiếu/hết hạn token) |
| `402` | Payment Required (vượt quota freemium / cần gói trả phí) |
| `404` | Not Found |
| `409` | Conflict (vd: email đã tồn tại) |
| `422` | Unprocessable Entity (validation thất bại / recording fail) |
| `429` | Too Many Requests (rate limit) |
| `503` | Service Unavailable (circuit breaker chi phí) |

---

## 0. Probes & Hạ tầng (không thuộc `/v1`)

| Method | Path | 🔒 Auth | Tác dụng |
|---|---|:---:|---|
| `GET` | `/health` | Không | Liveness probe. Trả `200` ngay nếu process còn sống. Dùng cho load balancer / k8s. |
| `GET` | `/ready` | Không | Readiness probe. Kiểm tra kết nối **Postgres** và **Redis**; trả `200` nếu cả hai OK, `503 degraded` nếu một dịch vụ phụ thuộc hỏng. |
| `GET` | `/swagger/*` | Không | Swagger UI tương tác (chỉ nên bật ở môi trường dev). |

---

## 0b. Home — Màn hình chính (aggregate) `/v1/home`

> **Yêu cầu Bearer token.** Một call khi vào Home, gộp nhiều phần.

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/home` | **Gộp toàn bộ dữ liệu Home trong MỘT request.** |

`GET /home` compose 4 phần từ các service feature. **Degrade độc lập**: phần nào lỗi trả `null`, không làm hỏng cả Home (luôn `200`).

```jsonc
{ "data": {
  "header": {                 // lời chào + streak
    "display_name": "Trung", "avatar_url": "https://…",
    "current_streak": 5, "longest_streak": 12, "last_active_date": "2026-06-26"
  },
  "daily_mission": { /* DailyMission — xem mục Daily */ },
  "challenge":     { /* DailyChallengeSummary — xem mục Daily */ },
  "practice_modes": [         // 6 card điều hướng (DB-driven, bảng practice_modes)
    { "key":"word", "title":"Phát âm từ", "subtitle":"…", "icon":"ic_word",
      "route":"/practice/word", "is_premium":false, "order":1 }
    // sentence, minimal_pair, shadowing, flashcard, profile …
  ]
}}
```

- **header** ← `users.display_name` + `users.avatar_url` + `streak_records`.
- **daily_mission** ← `GET /daily/mission` (cùng dữ liệu).
- **challenge** ← `GET /daily/today` (summary).
- **practice_modes** ← bảng `practice_modes` (`is_active` ORDER BY `order_index`); card tĩnh cho mọi user, sửa được không cần deploy.

---

## 1. Auth — Xác thực `/v1/auth`

| Method | Path | 🔒 Auth | Tác dụng |
|---|---|:---:|---|
| `POST` | `/auth/register` | Không | **Đăng ký tài khoản** qua email/Google/Apple. Tự động khởi tạo `error_profile`, `subscription` (gói `free`) và `streak_record` cho user mới. Trả cặp `access_token` + `refresh_token`. |
| `POST` | `/auth/login` | Không | **Đăng nhập**. Xác thực qua email/Google/Apple, trả `access_token` (15m) và `refresh_token` (7d). |
| `POST` | `/auth/refresh` | Không | **Refresh token**. Revoke refresh token cũ và cấp cặp token mới. Client gọi khi `access_token` hết hạn. |
| `POST` | `/auth/guest` | Không | **Tạo guest user** — tài khoản khách tạm thời (BR-FREE-04). Bị giới hạn tính năng, có thể nâng cấp lên tài khoản thật sau. |

**Chi tiết:**

- **`POST /auth/register`** — Body: `registerRequest`. → `201` `TokenData` · `400` thiếu field · `409` email đã đăng ký · `422` validation.
- **`POST /auth/login`** — Body: `loginRequest`. → `200` `TokenData` · `401` sai thông tin · `422` validation.
- **`POST /auth/refresh`** — Body: `refreshRequest`. → `200` `TokenData` · `401` refresh token sai/hết hạn.
- **`POST /auth/guest`** — Không body. → `201` `TokenData` · `500` lỗi nội bộ.

---

## 2. Me — Hồ sơ & cài đặt người dùng `/v1/me`

> Tất cả đều **yêu cầu Bearer token**.

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/me` | **Lấy profile**: goal, level, accent, scoring_level, timezone, consent flags. |
| `PATCH` | `/me` | **Cập nhật profile**: goal, level, accent, `daily_goal_items`, `daily_goal_minutes` (mục tiêu phút/ngày, 1–120), default_scoring_level, `display_name`, `avatar_url`, timezone. Chỉ gửi field cần đổi (partial update). |
| `DELETE` | `/me` | **Xóa tài khoản** (soft-delete) + enqueue job async xóa toàn bộ audio trên S3 (BR-PRIV-02). |
| `POST` | `/me/sync` | **Đồng bộ đa thiết bị** — trả payload sync để các thiết bị thống nhất trạng thái (NFR-REL-04). |
| `GET` | `/me/notifications` | **Đọc** cài đặt nhắc luyện tập & nhắc streak. |
| `PATCH` | `/me/notifications` | **Cập nhật** notification: bật/tắt `practice_reminder`, `streak_reminder`, đặt giờ nhắc. |
| `POST` | `/me/devices` | **Đăng ký push token** APNs (iOS) hoặc FCM (Android) cho thiết bị hiện tại. |
| `GET` | `/me/privacy` | **Đọc consent flags**: lưu bản ghi & dùng cải thiện sản phẩm (BR-PRIV-01/03). |
| `PATCH` | `/me/privacy` | **Cập nhật consent**: `allow_recording_for_improvement`, `save_recording_history`. Chi phối việc lưu `audio_ref`. |
| `POST` | `/me/export` | **Xuất dữ liệu cá nhân (GDPR)** — enqueue job gom toàn bộ dữ liệu user thành file tải về. Trả `202`. |
| `DELETE` | `/me/history` | **Xóa lịch sử luyện tập** (sessions/results/audio) nhưng giữ nguyên tài khoản. |

---

## 3. Assessment — Pre-Assessment onboarding `/v1/assessments`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/assessments/pre-assessment` | **Lấy bộ câu hỏi Pre-Assessment** dùng trong onboarding bằng **một request duy nhất**. |

**Chi tiết `GET /assessments/pre-assessment`:**

Trả về metadata bộ đề + toàn bộ danh sách câu hỏi đã sắp xếp theo `order` để client chỉ gọi 1 lần khi bắt đầu bài đánh giá. Thiết kế mở rộng cho nhiều bộ đề / nhiều cấp độ CEFR.

**Query params (tùy chọn):**
| Param | Kiểu | Mô tả |
|---|---|---|
| `code` | string | Chọn một bộ đề cụ thể theo slug (vd: `pre_assessment_default`). Mặc định: bộ active version cao nhất. |
| `level` | string | Lọc theo cấp độ CEFR. Enum: `A1`, `A2`, `B1`, `B2`, `C1`, `C2`. |

**Response `data` (`AssessmentSet`):**
```jsonc
{
  "id": "uuid",
  "code": "pre_assessment_default",
  "type": "pre_assessment",
  "title": "Pre-Assessment",
  "description": "Đánh giá trình độ phát âm ban đầu trong onboarding.",
  "cefr_level": null,
  "locale": "en-US",
  "question_count": 7,
  "questions": [
    {
      "id": "uuid",
      "order": 2,
      "text": "I think this is great.",
      "phonetic": "/aɪ θɪŋk ðɪs ɪz ɡreɪt/",
      "sample_audio_url": "https://cdn.phonara.app/assessment/pre/q2.mp3",
      "expected_duration": 4,   // optional, giây
      "difficulty": 2           // optional, 1..5
    }
  ]
}
```

→ `200` `AssessmentSet` · `400` `level` không hợp lệ · `401` Unauthorized · `404` chưa có bộ đề active.

> Việc **chấm điểm** (ghi âm gửi lên) thuộc endpoint khác (Sessions / Speech) — không nằm trong endpoint này.

---

## 4. Speech Gateway — Cấp token nhà cung cấp `/v1/speech`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `POST` | `/speech/token` | **Cấp speech token** short-lived (30–60s) cho Azure Speech hoặc Speechace (§3c). Áp dụng 7 lớp phòng thủ chi phí: rate-limit theo user/IP, quota freemium, cost circuit breaker. |

→ `200` `SpeechTokenResult` · `401` · `402` vượt quota · `422` validation · `429` rate limit · `503` circuit breaker chi phí kích hoạt.

---

## 5. Sessions — Phiên luyện tập & chấm điểm `/v1/sessions`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `POST` | `/sessions` | **Tạo session** luyện tập mới. Server kiểm tra gating (quota freemium). Nếu không gửi `scoring_level` thì dùng `default_scoring_level` của user. |
| `GET` | `/sessions/history` | **Lịch sử** 50 session gần nhất; mỗi session có `summary_score` và signed URL để replay audio (gated theo consent). |
| `GET` | `/sessions/in-progress` | **Session đang dang dở** (chưa `ended_at`) để client hiển thị "Tiếp tục 2/8". |
| `GET` | `/sessions/{id}` | **Lấy thông tin** một session. |
| `POST` | `/sessions/{id}/end` | **Kết thúc session**: tính `summary_score` = trung bình accuracy của toàn bộ results. |
| `POST` | `/sessions/{id}/results` | **Nộp kết quả PA (ingestion)** — nhận PA thô từ Azure Speech SDK. Server áp Level scoring (§3b.0a), sanity-check, lưu DB, enqueue recompute Error Profile. **Idempotent** theo `idempotency_key` (BR-QA-03). Recording fail (không có tiếng/nhiễu) → `422`, không tạo result, không trừ điểm. |
| `POST` | `/sessions/{id}/results:batch` | **Batch ingest** nhiều PA result cùng lúc khi mạng khôi phục (offline sync, NFR-REL-03). Tối đa 50 items. |

**Lưu ý kiến trúc:** Client **không tự chấm điểm** — chỉ gửi PA thô, server mới là nơi áp scoring (trust boundary §3b).

---

## 6. Content — Kho nội dung luyện tập `/v1/content`

> **Yêu cầu Bearer token.**

| Method | Path | Query params | Tác dụng |
|---|---|---|---|
| `GET` | `/content/words` | `topic`, `goal`, `phoneme` | **Danh sách từ vựng** active, lọc theo chủ đề / mục tiêu / phoneme trọng tâm (FR-WORD-07). |
| `GET` | `/content/sentences` | `topic`, `goal` | **Danh sách câu** luyện active. |
| `GET` | `/content/minimal-pairs` | `l1_tag`, `category` | **Danh sách minimal pairs** (cặp âm tối thiểu), lọc theo L1 error tag hoặc category chip (S15). |
| `GET` | `/content/passages` | `source`, `difficulty` | **Danh sách shadowing passages** (`curated`/`daily`/`news`/`dialogue`). |
| `GET` | `/content/passages/{id}/sentences` | path `id` | **Các câu trong một passage**, sắp theo thứ tự. |
| `GET` | `/content/fix-guide` | `phoneme`, `l1_tag` | **Fix guide** hướng dẫn sửa lỗi phát âm (tiếng Việt). Fallback về simplified guide nếu chưa có rich content (BR-L1-02). |

---

## 7. Coach — Error Profile Engine `/v1/coach`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/coach/profile` | **Error Profile — tổng quan phát âm**: `overall_score`, `top_errors`, phoneme/skill mastery. Dùng cho onboarding (S07) & coach overview (S19). |
| `GET` | `/coach/recommendation` | **Bài luyện đề xuất**. Rank = severity × frequency × L1_importance × goal_fit (BR-COACH-04). Dùng cho hero section (S09). |
| `GET` | `/coach/report` | **Báo cáo tiến độ tuần/tháng**. Đọc `mastery_snapshots` để tính trend ↑↓ và sparkline. Query `period` = `week`\|`month` (default `week`). |

---

## 8. Shadowing — Luyện nói theo `/v1/shadowing`

> **Yêu cầu Bearer token.** `passage_id` là path param.

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/shadowing/{passage_id}/progress` | **Tiến độ passage**: `current_sentence_index`, `sentence_status` (passed/current/skipped), `avg_score`. |
| `POST` | `/shadowing/{passage_id}/sentence-result` | **Nộp kết quả một câu**. Server áp Level scoring, check ngưỡng 80% (BR-SHAD-02). Trả `next_action`: `advance` \| `retry` \| `skip_available`. |
| `POST` | `/shadowing/{passage_id}/complete` | **Hoàn thành passage** — mark completed, trả `avg_score` tổng kết (S26). |

---

## 9. Minimal Pairs — Listen drill `/v1/minimal-pairs`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `POST` | `/minimal-pairs/listen/start` | **Bắt đầu listen drill** — tạo phiên nghe phân biệt âm với N cặp ngẫu nhiên, `hearts=3` (FR-MP-02, S16). |
| `POST` | `/minimal-pairs/listen/{drill_id}/answer` | **Nộp đáp án** — chấm đúng/sai, trừ heart nếu sai, cập nhật `pair_mastery.listen`. |
| `GET` | `/minimal-pairs/listen/{drill_id}` | **Trạng thái drill**: `total_items`, `correct_count`, `hearts_left`, `status`. |

---

## 10. Progress, Badges & Streak `/v1`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/progress/overview` | **Tổng quan tiến độ**: streak hiện tại, longest_streak, tổng số session (S20). |
| `GET` | `/progress/charts` | **Biểu đồ điểm theo thời gian** (sparkline) từ `mastery_snapshots`. Query `period` = `week`\|`month`. |
| `GET` | `/badges` | **Danh sách badges**: `earned[]` và `locked[]` kèm criteria và progress tới badge kế tiếp (FR-STREAK-03). |
| `POST` | `/streak/check-in` | **Check-in hàng ngày** theo timezone user. Tăng `current_streak` nếu liên tiếp, reset về 1 nếu gián đoạn, cập nhật `longest_streak` (BR-STREAK-01). |

---

## 11. Subscription & IAP `/v1`

| Method | Path | 🔒 Auth | Tác dụng |
|---|---|:---:|---|
| `GET` | `/subscription` | Có | **Trạng thái subscription**: plan (`free`/`premium`/`exam_pack`), status, renews_at, store. |
| `GET` | `/subscription/plans` | Không | **Danh sách gói** với giá VND, product_id cho App Store/Google Play (BR-PAY-03, S21). |
| `POST` | `/subscription/verify` | Có | **Verify IAP receipt** từ App Store/Google Play, nâng cấp plan nếu hợp lệ (BR-PAY-02). |
| `POST` | `/subscription/restore` | Có | **Restore Purchase** — khôi phục entitlement bằng cách re-verify với store (bắt buộc theo policy store). |
| `POST` | `/iap/webhook/apple` | Không | **Webhook Apple** — nhận server notifications (renewed/canceled/refunded). |
| `POST` | `/iap/webhook/google` | Không | **Webhook Google Play** — nhận Real-Time Developer Notifications (RTDN). |
| `GET` | `/freemium/quota` | Có | **Quota freemium hôm nay**: `items_used`, `daily_limit`, `remaining`. Premium → `is_premium=true`, `limit=-1` (BR-FREE-01). |

---

## 12. Daily Challenge `/v1/daily`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/daily/today` | **Summary nhẹ cho card ở Home.** Metadata + `item_count` + `user_status`, KHÔNG kèm nội dung item. |
| `GET` | `/daily/challenges/{id}` | **Full nội dung challenge** cho màn challenge. Resolve sẵn toàn bộ item trong một call. |
| `GET` | `/daily/history` | **Lịch sử 30 ngày**: challenge_id, date, title, category, status (completed/missed), score. |
| `GET` | `/daily/mission` | **Nhiệm vụ hàng ngày (mục tiêu phút)** — trạng thái cho widget "Xuất sắc! 15/15 phút". |
| `POST` | `/daily/mission/heartbeat` | **Cộng dồn thời gian luyện** — client gửi delta giây active, server tích lũy theo ngày. |

### Nhiệm vụ hàng ngày (mục tiêu phút)

Mục tiêu thời lượng/ngày (mặc định 15 phút, `users.daily_goal_minutes`). Thời gian active do client gửi lên, tích lũy theo **local date** vào `daily_progress.seconds_practiced`.

- **`POST /daily/mission/heartbeat`** — body `{ "seconds": 1..3600 }` (validate). Gửi **DELTA** kể từ lần trước (additive), không gửi tổng. Trả về `DailyMission` mới nhất → client cập nhật widget ngay, khỏi gọi GET.
- **`GET /daily/mission`** — đọc trạng thái (read-only).

**Response (`DailyMission`):**
```jsonc
{
  "data": {
    "date": "2026-06-27",
    "goal_minutes": 15,
    "minutes_done": 15,    // cap ở goal cho "15/15"
    "seconds_done": 1140,  // số thật, có thể vượt goal
    "percent": 100,        // 0..100 (cap)
    "completed": true,
    "status": "not_started | in_progress | completed",
    "xp_earned": 50,       // XP nhận trong ngày từ nhiệm vụ
    "just_completed": true // chỉ true ở heartbeat vừa hoàn thành (để client mừng + toast XP)
  }
}
```
> "Xuất sắc! 15/15 phút" = `completed: true`. Backend trả `status` enum, client render chữ Việt theo locale. `minutes_done` cap ở goal khi đã hoàn thành, `seconds_done` giữ số thật.
>
> **Tích hợp:** lần ĐẦU đạt goal trong ngày, server tự **cộng 50 XP** (`daily_progress.xp_earned`) và **streak check-in** (tăng `current_streak`). Cả hai idempotent — heartbeat sau đó không cộng trùng (`just_completed` chỉ xuất hiện đúng một lần). Goal mặc định 15 phút, user đổi qua `PATCH /me` (`daily_goal_minutes`).

Một daily challenge là **một bộ nhiều mục** (`daily_challenge_items`) — vd: vài từ/câu + một passage — có thứ tự (`order`). "Hôm nay" tính theo **timezone của user** (`users.timezone`), đổi ngày lúc nửa đêm giờ địa phương (nhất quán với streak), không phải UTC.

**Luồng client (tách summary / detail):**
1. **Home** gọi `GET /daily/today` → nhận card nhẹ (không tải items).
2. User bấm vào → navigate **chỉ mang theo `challenge_id`**.
3. **Màn challenge** gọi `GET /daily/challenges/{id}` **một lần** → full nội dung.
4. History cũng có `challenge_id` để deep-link vào cùng endpoint detail.

**`GET /daily/today` — response (`DailyChallengeSummary`):**
```jsonc
{
  "data": {
    "challenge_id": "uuid",
    "date": "2026-06-27",
    "title": "Thử thách phát âm hằng ngày",
    "description": "…",
    "category": "pronunciation",
    "banner_url": "https://…",
    "moderated": true,
    "user_status": "completed | in_progress | missed | null",
    "available": true,
    "item_count": 4
  }
}
```
> Khi chưa có challenge cho ngày đó: `available: false` + `message`.

**`GET /daily/challenges/{id}` — response (`DailyChallenge`):** gồm các field summary ở trên **cộng** mảng `items` đã resolve. Path param `id` là UUID; không tồn tại → `404`.
```jsonc
{
  "data": {
    "challenge_id": "uuid", "date": "2026-06-27", "title": "…",
    "user_status": null, "available": true, "item_count": 4,
    "items": [
      {
        "order": 1,
        "kind": "word",          // word | sentence | passage
        "content_item": {
          "id": "uuid", "type": "word", "text": "comfortable",
          "ipa": "/ˈkʌmftəbəl/", "audio_url_us": "https://…",
          "difficulty": 3, "focus_phonemes": ["ə"]
        }
      },
      {
        "order": 4,
        "kind": "passage",
        "passage": {
          "id": "uuid", "title": "Morning Routine", "source": "curated",
          "difficulty": 2, "sentence_count": 3,
          "sentences": [
            { "id": "uuid", "order_index": 1, "text": "Every morning I wake up at six.",
              "ipa": "/…/", "native_audio_url": "https://…" }
          ]
        }
      }
    ]
  }
}
```

> Mỗi item có **đúng một** trong `content_item` hoặc `passage`, xác định bởi `kind`. Việc ghi âm & chấm điểm đi qua `/sessions` với `source: "daily"` (không nằm trong endpoint này).

---

## 13. Exam — IELTS/TOEIC Speaking `/v1/exam`

> **Yêu cầu Bearer token.**

| Method | Path | Tác dụng |
|---|---|---|
| `GET` | `/exam/prompts` | **Danh sách đề thi** IELTS/TOEIC. Query `type` (`ielts`/`toeic`), `part` (`part1`, `part2`…). |
| `POST` | `/exam/sessions` | **Tạo exam session** — mở phiên thi, kiểm tra gating Exam Pack (BR-PAY-06). `402` nếu thiếu gói. |
| `POST` | `/exam/sessions/{id}/submit` | **Nộp bài** (upload `audio_ref`). Server enqueue chấm async qua Speechace. Client **không** tự chấm band. Trả `202`. |
| `GET` | `/exam/sessions/{id}/report` | **Báo cáo kết quả**: `band_score` + `cefr_level` + `criteria` sau khi Speechace chấm xong (S33). |
| `GET` | `/exam/sessions` | **Lịch sử** 20 exam session gần nhất với band & CEFR (save/share). |

---

## 14. System — Cấu hình, Legal, Feedback, Analytics `/v1`

| Method | Path | 🔒 Auth | Tác dụng |
|---|---|:---:|---|
| `POST` | `/feedback` | Có | **Gửi feedback / báo lỗi** kèm context (app version, device, screen). Loại: feedback/bug/support. |
| `GET` | `/app-config` | Không | **App configuration flags**: `min_version` (force-update) và feature flags "Sắp có". |
| `GET` | `/legal/{doc_type}` | Không | **Legal documents** — phiên bản mới nhất của Terms hoặc Privacy (markdown). `doc_type` = `terms`\|`privacy`. |
| `POST` | `/events` | Có | **Ingest analytics event** vào `analytics_events` (vd: `practice_item_scored`, `fix_guide_viewed`, `recording_failed`…). Trả `202`. |

---

## Phụ lục — Bảng tổng hợp toàn bộ endpoint

| # | Method | Path | 🔒 Auth | Nhóm |
|---|---|---|:---:|---|
| 1 | GET | `/health` | – | Probe |
| 2 | GET | `/ready` | – | Probe |
| 3 | GET | `/swagger/*` | – | Docs |
| 4 | POST | `/v1/auth/register` | – | Auth |
| 5 | POST | `/v1/auth/login` | – | Auth |
| 6 | POST | `/v1/auth/refresh` | – | Auth |
| 7 | POST | `/v1/auth/guest` | – | Auth |
| 8 | GET | `/v1/me` | ✓ | Me |
| 9 | PATCH | `/v1/me` | ✓ | Me |
| 10 | DELETE | `/v1/me` | ✓ | Me |
| 11 | POST | `/v1/me/sync` | ✓ | Me |
| 12 | GET | `/v1/me/notifications` | ✓ | Me |
| 13 | PATCH | `/v1/me/notifications` | ✓ | Me |
| 14 | POST | `/v1/me/devices` | ✓ | Me |
| 15 | GET | `/v1/me/privacy` | ✓ | Me |
| 16 | PATCH | `/v1/me/privacy` | ✓ | Me |
| 17 | POST | `/v1/me/export` | ✓ | Me |
| 18 | DELETE | `/v1/me/history` | ✓ | Me |
| 19 | GET | `/v1/assessments/pre-assessment` | ✓ | Assessment |
| 20 | POST | `/v1/speech/token` | ✓ | Speech |
| 21 | POST | `/v1/sessions` | ✓ | Sessions |
| 22 | GET | `/v1/sessions/history` | ✓ | Sessions |
| 23 | GET | `/v1/sessions/in-progress` | ✓ | Sessions |
| 24 | GET | `/v1/sessions/{id}` | ✓ | Sessions |
| 25 | POST | `/v1/sessions/{id}/end` | ✓ | Sessions |
| 26 | POST | `/v1/sessions/{id}/results` | ✓ | Sessions |
| 27 | POST | `/v1/sessions/{id}/results:batch` | ✓ | Sessions |
| 28 | GET | `/v1/content/words` | ✓ | Content |
| 29 | GET | `/v1/content/sentences` | ✓ | Content |
| 30 | GET | `/v1/content/minimal-pairs` | ✓ | Content |
| 31 | GET | `/v1/content/passages` | ✓ | Content |
| 32 | GET | `/v1/content/passages/{id}/sentences` | ✓ | Content |
| 33 | GET | `/v1/content/fix-guide` | ✓ | Content |
| 34 | GET | `/v1/coach/profile` | ✓ | Coach |
| 35 | GET | `/v1/coach/recommendation` | ✓ | Coach |
| 36 | GET | `/v1/coach/report` | ✓ | Coach |
| 37 | GET | `/v1/shadowing/{passage_id}/progress` | ✓ | Shadowing |
| 38 | POST | `/v1/shadowing/{passage_id}/sentence-result` | ✓ | Shadowing |
| 39 | POST | `/v1/shadowing/{passage_id}/complete` | ✓ | Shadowing |
| 40 | POST | `/v1/minimal-pairs/listen/start` | ✓ | MinimalPairs |
| 41 | POST | `/v1/minimal-pairs/listen/{drill_id}/answer` | ✓ | MinimalPairs |
| 42 | GET | `/v1/minimal-pairs/listen/{drill_id}` | ✓ | MinimalPairs |
| 43 | GET | `/v1/progress/overview` | ✓ | Progress |
| 44 | GET | `/v1/progress/charts` | ✓ | Progress |
| 45 | GET | `/v1/badges` | ✓ | Progress |
| 46 | POST | `/v1/streak/check-in` | ✓ | Progress |
| 47 | GET | `/v1/subscription` | ✓ | Subscription |
| 48 | GET | `/v1/subscription/plans` | – | Subscription |
| 49 | POST | `/v1/subscription/verify` | ✓ | Subscription |
| 50 | POST | `/v1/subscription/restore` | ✓ | Subscription |
| 51 | POST | `/v1/iap/webhook/apple` | – | Subscription |
| 52 | POST | `/v1/iap/webhook/google` | – | Subscription |
| 53 | GET | `/v1/freemium/quota` | ✓ | Subscription |
| 54 | GET | `/v1/home` | ✓ | Home |
| 55 | GET | `/v1/daily/today` | ✓ | Daily |
| 56 | GET | `/v1/daily/challenges/{id}` | ✓ | Daily |
| 57 | GET | `/v1/daily/history` | ✓ | Daily |
| 58 | GET | `/v1/daily/mission` | ✓ | Daily |
| 59 | POST | `/v1/daily/mission/heartbeat` | ✓ | Daily |
| 60 | GET | `/v1/exam/prompts` | ✓ | Exam |
| 61 | POST | `/v1/exam/sessions` | ✓ | Exam |
| 62 | POST | `/v1/exam/sessions/{id}/submit` | ✓ | Exam |
| 63 | GET | `/v1/exam/sessions/{id}/report` | ✓ | Exam |
| 64 | GET | `/v1/exam/sessions` | ✓ | Exam |
| 65 | POST | `/v1/feedback` | ✓ | System |
| 66 | GET | `/v1/app-config` | – | System |
| 67 | GET | `/v1/legal/{doc_type}` | – | System |
| 68 | POST | `/v1/events` | ✓ | System |

> **Tổng cộng:** 65 endpoint API (`/v1`) + 3 endpoint hạ tầng (probes & Swagger).

---

_Tài liệu sinh từ các route trong `internal/server/server.go` và annotation Swagger trong `internal/handler/*.go`. Khi thêm/sửa endpoint, cập nhật file này song song và chạy `make swag-gen` để đồng bộ Swagger UI._
