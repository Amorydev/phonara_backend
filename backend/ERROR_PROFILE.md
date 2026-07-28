# Hồ sơ lỗi — cách mastery được tính

`internal/service/errorprofile.go`. Đây là thứ Coach, Fix Guide và gợi ý nội dung bám vào.

Trước 2026-07-28, ba bảng `phoneme_mastery`, `skill_mastery`, `mastery_snapshots` **không
có một đường ghi nào** trong toàn bộ code — `error_profiles` chỉ được chèn một dòng rỗng
lúc đăng ký. Coach trả mảng rỗng vĩnh viễn mà không báo lỗi.

## Dữ liệu vào

```
phoneme_scores → practice_item_results → practice_sessions → user
```

Loại bỏ: `trust_flag = 'rejected'`, `expected_phoneme = ''`, `diagnosis = 'insertion'`.

## Quy một lần chấm về điểm

Bám sát engine, để điểm trong Coach không lệch điểm người dùng vừa thấy sau khi đọc:

| `diagnosis` | Điểm | Vì sao |
|---|---|---|
| `correct` · `substitution` | `accuracy` | |
| `omission` | **0** | `accuracy` là NULL. Nuốt mất /t/ chính là câu trả lời cho "phát âm /t/ tốt đến đâu" |
| `insertion` | *bỏ* | âm thừa không thuộc về âm vị chuẩn nào |
| `uncertain` | `accuracy` | bất định nằm ở **nhãn**, không ở **điểm** — GOP vẫn tính bình thường |
| `NULL` (bản ghi cũ) | theo `is_omission` | dữ liệu trước migration 000007 |

⚠️ Quy tắc `omission → 0` **không** mâu thuẫn với migration 000007, nơi omission bị loại
khỏi `accuracy` mức câu. Ở đó `completeness` đã phạt rồi nên tính thêm là phạt kép. Ở đây
câu hỏi khác hẳn — không phải "câu này đọc tốt không" mà "âm này phát âm tốt không".

## Công thức

Trung bình có trọng số giảm dần theo thời gian, **chuẩn hoá theo tổng trọng số**:

```
mastery = Σ wᵢ·xᵢ / Σ wᵢ ,   wᵢ = (1−α)^(tuổi của quan sát i)
```

**Không** dùng dạng đệ quy quen thuộc `acc = α·v + (1−α)·acc` khởi tạo bằng quan sát đầu
tiên. Dạng đó có lỗi thật khi số mẫu còn ít: quan sát **cũ nhất** giữ trọng số `(1−α)^(n−1)`,
nên với 4 lần đọc thì lần đầu vẫn nắm 34%. Đo được:

| Chuỗi | Dạng đệ quy (sai) | Dạng chuẩn hoá |
|---|---|---|
| `[20,20,20,90]` tiến bộ | **41,0** | 41,97 |
| `[90,20,20,20]` sa sút | **44,0** | 33,49 |

Dạng đệ quy chấm người **đang tệ đi cao hơn** người đang khá lên. `TestEWMAWeightsRecentHigher`
giữ cho lỗi đó không quay lại.

### α = 0,15

Cửa sổ hiệu dụng ≈ `1/α` ≈ **7 lần đọc gần nhất**; quan sát mới nhất nắm đúng 15,0% trọng số.

Chọn 0,15 chứ không phải 0,3 vì một ràng buộc sản phẩm: **một lần đọc hỏng không được làm
tụt quá một bậc trạng thái.**

| α | 5 lần 90 điểm rồi 1 lần 0 điểm | Trạng thái |
|---|---|---|
| 0,30 | 59,4 | `good` → **`weak`** — nhảy hai bậc |
| **0,15** | **68,3** | `good` → `improving` |

Micro trục trặc hay một tiếng ho không được xoá thành quả nhiều buổi luyện.

### Cửa sổ 50 quan sát mỗi âm

Chặn chi phí, và **không** làm sai kết quả: quan sát thứ 50 có trọng số `0,85⁴⁹ ≈ 3,5e-4`
trên tổng trọng số `6,667` — đóng góp **0,0052%**. Cửa sổ là hệ quả của chính công thức,
không phải một đánh đổi. `TestEWMATruncationWindowIsNumericallyIrrelevant` giữ lý lẽ đó
đúng nếu ai đó chỉnh α.

## Trạng thái

| | |
|---|---|
| `weak` | < 60 |
| `improving` | 60 – 79 |
| `good` | ≥ 80 |

## Kỹ năng

`accuracy` và `completeness` lấy từ `practice_item_results`.

`final_consonant` tách riêng vì đây là lỗi theo **vị trí**, không theo âm: người phát âm
/t/ đầu từ rất tốt vẫn có thể nuốt sạch /t/ cuối từ. Gộp chung hai vị trí sẽ làm trung bình
che mất đúng lỗi cần chỉ ra. Xác định bằng `phoneme_index` lớn nhất trong mỗi
`(item_result, word_index)`, lọc qua tập 24 phụ âm tiếng Anh.

Liệt kê **phụ âm** thay vì nguyên âm là cố ý: tập phụ âm đóng và ổn định, còn tập nguyên âm
espeak rộng và nhiều biến thể giảm (`ə`, `ɐ`, `ᵻ`, `ɚ`…). Ký hiệu lạ khi đó mặc định **không**
bị tính là phụ âm cuối — hướng sai an toàn hơn là bịa ra chỉ số nuốt-âm từ ký hiệu chưa từng thấy.

**`prosody` và `fluency` cố ý KHÔNG được ghi.** Engine v1 trả null cho cả hai (xem
`CAPABILITIES` trong `assess.py`). Ghi 0 sẽ hiện lên UI thành "kỹ năng rất yếu" trong khi
thực tế là "chưa đo".

## `top_errors`

Âm `weak` có **≥ 3 lần thử**, sắp xếp mastery tăng dần, lấy 5.

Ngưỡng 3 lần chặn việc một lần micro trục trặc đẩy một âm vào Fix Guide. Thứ tự phụ theo
tên âm để kết quả ổn định — map trong Go duyệt ngẫu nhiên, thiếu cái đó thì hai lần
recompute trên cùng dữ liệu sinh ra thứ tự khác nhau và UI trông như dữ liệu đang nhảy.

`l1_tag` để trống: nội dung chọn theo lỗi **đo được**, không theo tiếng mẹ đẻ khai báo.
Xem §6.1.0 của `PRONUNCIATION_ENGINE_PLAN.md`.

## Tính lại toàn bộ, không cộng dồn

Task được đẩy fire-and-forget và asynq có retry, nên một task chạy hai lần là chuyện bình
thường. Cộng dồn EWMA hai lần sẽ **đếm trùng** và làm lệch mastery vĩnh viễn mà không có
cách nào phát hiện. Tính lại thì chạy bao nhiêu lần cũng ra một kết quả.

Chi phí đã bị chặn sẵn bởi cửa sổ quan sát; đo thực tế trên dữ liệu nhỏ: **17 ms**.

## Kích hoạt

`SessionService.IngestResult` → `enqueueErrorProfileRecompute` (fire-and-forget, lỗi chỉ log
— người học vừa đọc xong cần thấy điểm ngay).

`asynq.Unique` + `ProcessIn(15s)` gom cả một phiên luyện thành **một** lần tính. Một phiên
20 câu đẩy 20 task giống hệt nhau; thiếu dedup thì 19 lần tính đầu bị lần cuối ghi đè, tốn
CPU và I/O cho kết quả bị vứt đi.

`asynq.ErrDuplicateTask` **không** được log như lỗi — trùng task là kết quả mong muốn.

## Chạy tay

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/backend && go test ./internal/service/ -run TestEWMA -v
```

## Điểm tổng

Trung bình mastery trên **toàn bộ** âm vị đã luyện. Chưa luyện gì → `null`, **không phải 0**
(vẽ đường chạm đáy cho người mới là cách nhanh nhất khiến họ bỏ app).

Định nghĩa nằm ở một chỗ duy nhất (`overallScore`) vì nó xuất hiện ở hai nơi người dùng
nhìn thấy **cùng lúc**: con số trong Coach và đường biểu đồ tiến bộ.

⚠️ Trước 2026-07-28, `CoachService.GetErrorProfile` tính điểm tổng bằng cách lấy trung bình
trên danh sách hiển thị `ORDER BY mastery ASC LIMIT 20` — tức 20 âm **yếu nhất**. Tiếng Anh
có ~44 âm vị, nên người luyện rộng bị chặn trên bởi chính cái đuôi yếu của mình và phần đã
làm tốt không bao giờ được tính. Đã sửa thành truy vấn `AVG` riêng trên toàn bộ.

## Ảnh chụp theo ngày — `mastery_snapshots`

Nguồn của `GET /v1/progress/charts` và `GET /v1/coach/report`.

Ghi trong **cùng transaction** với mastery (`snapshotToday`). Tách ra hai lần ghi thì một
lần hỏng sẽ để lại biểu đồ mâu thuẫn với chính con số nằm ngay cạnh nó, và không ai phát hiện.

**Chỉ chụp vào ngày thực sự có luyện.** Ngày không luyện thì mastery không đổi, nên một
dòng snapshot khi đó là **bản sao** chứ không phải phép đo — ghi xuống là bịa dữ liệu và
làm biểu đồ trông như có hoạt động. Hệ quả: biểu đồ có khoảng trống ở ngày nghỉ, và đó là
sự thật chứ không phải thiếu sót.

`UNIQUE (user_id, snapshot_date)` + upsert: luyện nhiều lần trong ngày thì lần cuối thắng.
Đã kiểm: 3 lần recompute liên tiếp → đúng **1** dòng.

### Ngày tính theo múi giờ người học, không theo UTC

`(now() AT TIME ZONE users.timezone)::date`. Việt Nam là UTC+7 nên phần lớn thời gian hai
ngày trùng nhau — nhưng không phải luôn. Đo lúc 14:28 giờ VN:

| Múi giờ | Nếu dùng UTC | Ngày đúng | |
|---|---|---|---|
| `Asia/Ho_Chi_Minh` | 28/07 | 28/07 | |
| `Pacific/Honolulu` | 28/07 | **27/07** | ← UTC ghi sai ngày |

Người luyện lúc 2 giờ sáng ở VN cũng rơi vào đúng cái bẫy này. `users.timezone` đã có sẵn
nên không phải đoán.

### Nội dung

```json
{
  "overall_score": 69.46,
  "phoneme_mastery": {"m": 92.70, "θ": 46.22},
  "skill_mastery": {"accuracy": 70.54, "completeness": 88.11, "final_consonant": 92.70}
}
```

Dạng gọn `phoneme → mastery`. Đủ để vẽ xu hướng và so hai mốc thời gian; `attempts` và
`status` suy lại được, không cần nhân bản vào từng ảnh chụp mỗi ngày.

### Job vá — `mastery:snapshot`

**Không phải** đường ghi chính. Chỉ để vá những ngày mà việc tính lại thất bại hẳn sau khi
hết retry: tìm người có luyện trong 2 ngày qua nhưng thiếu ảnh chụp hôm nay, rồi tính lại.

Một người hỏng không chặn phần còn lại — nếu không, một hồ sơ lỗi sẽ khiến cả job vá không
bao giờ chạy hết. Job trả lỗi nếu có bất kỳ ai hỏng, để asynq ghi lại.

Chạy tay:

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/backend && docker compose exec redis redis-cli --version >/dev/null && echo "đẩy task qua asynq client (xem cmd/ hoặc test)"
```

Chưa gắn cron. Đường chính đã đủ cho hoạt động bình thường; gắn scheduler khi có nhu cầu
vận hành thật.

## Cặp âm — `pair_mastery`

Nguồn của `minimal_pairs_progress` trong Coach. Tính cùng lượt với hồ sơ lỗi.

**NGHE và NÓI tách riêng** vì đó là hai kỹ năng hỏng độc lập: người học có thể nghe ra khác
biệt /l/–/n/ rất rõ mà vẫn không phát âm được, hoặc ngược lại. Gộp thành một con số sẽ che
mất đúng kỹ năng cần luyện.

| | Nguồn |
|---|---|
| `listen_mastery` | `mp_listen_answers` — tỷ lệ đúng, quy về thang 0–100 |
| `speak_mastery` | `practice_item_results.minimal_pair_id` — accuracy khi đọc chính cặp đó |
| `status` | trung bình hai vế |

Chưa luyện một trong hai kỹ năng thì vế đó bằng 0, nên cặp chưa thể đạt `good` — đó đúng là
điều muốn nói.

## Huy hiệu

`AwardBadges` trao mọi huy hiệu đã đạt nhưng chưa nhận. Trước đây `user_badges` không có
đường ghi nào — mọi huy hiệu nằm ở mục "chưa mở khoá" vĩnh viễn kể cả khi người học đã vượt
xa mốc.

Bốn loại tiêu chí, cả bốn giờ đều có nguồn dữ liệu thật:

| `criteria_type` | Nguồn |
|---|---|
| `streak` | `streak_records.current_streak` |
| `items_done` | số `practice_item_results` |
| `phoneme_mastered` | số `phoneme_mastery` ở mức `good` |
| `pairs_mastered` | số `pair_mastery` ở mức `good` |

Gọi ở **hai** chỗ, vì thành tích đổi ở hai nơi khác nhau: sau khi tính lại hồ sơ lỗi (phủ
ba loại sau) và sau khi điểm danh (`streak` chỉ đổi ở đó — thiếu lời gọi này thì mọi huy
hiệu chuỗi ngày vĩnh viễn khoá).

Trao SAU khi commit, không trong transaction: điều kiện đọc chính những bảng vừa ghi.
`ON CONFLICT DO NOTHING` giữ nguyên `earned_at`, nên ngày nhận huy hiệu không nhảy mỗi lần
luyện.

## Chưa làm

`exam_prompts` cố ý chưa seed — xem `CONTENT_SEED.md`.
