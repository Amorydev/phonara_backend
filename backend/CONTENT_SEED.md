# Nội dung dạy phát âm — seed

`cmd/seed/content.go`. Ba bảng trước đây được **đọc nhưng chưa bao giờ được ghi**:
`GET /v1/content/fix-guide` luôn rơi vào nhánh fallback `is_simplified: true`, và
`GET /v1/content/minimal-pairs` luôn trả mảng rỗng.

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/backend && go run ./cmd/seed
```

| Bảng | Số dòng | |
|---|---|---|
| `l1_error_tags` | 11 | kiểu lỗi phát âm |
| `fix_guides` | 26 | hướng dẫn đặt lưỡi/môi theo âm vị |
| `minimal_pairs` | 34 | cặp từ khác nhau một âm (17 miễn phí) |

Chạy lại bao nhiêu lần cũng ra đúng số đó — upsert theo khoá tự nhiên, không nhân bản.

## Khoá chọn nội dung là âm vị, không phải tiếng mẹ đẻ

`ContentService.GetFixGuide` lọc `phoneme = $1`, và âm vị đó đến từ `top_errors` — tức lỗi
**đo được** của chính người học (xem `ERROR_PROFILE.md`). App không hỏi tiếng mẹ đẻ; xem
§6.1.0 của `PRONUNCIATION_ENGINE_PLAN.md`.

Tên bảng `l1_error_tags` có từ trước và giữ nguyên để khỏi migration, nhưng ngữ nghĩa là
**kiểu lỗi**, không phải tiếng mẹ đẻ của ai. Dùng để nhóm nội dung, không dùng để suy đoán
người học nói tiếng gì.

## Ký hiệu âm vị phải khớp CHÍNH XÁC vocab engine

Đây là chỗ hỏng âm thầm nguy hiểm nhất trong toàn bộ file nội dung.

| Gõ nhầm | Đúng phải là |
|---|---|
| `g` (U+0067) | `ɡ` (U+0261) — chữ g một tầng của IPA |
| `r` (U+0072) | `ɹ` (U+0279) — r lộn ngược |
| `:` (U+003A) | `ː` (U+02D0) — dấu kéo dài IPA |

Lệch một ký tự thì code vẫn biên dịch, seed vẫn chạy, nhưng `phoneme = $1` **không bao giờ
khớp** — hướng dẫn biến mất mà không có lỗi nào được ghi ra.

`TestFixGuidePhonemeSymbolsAreRealIPA` và `TestMinimalPairPhonemeSymbolsAreRealIPA` chặn
đúng các ký tự nhìn giống đó. Toàn bộ 26 ký hiệu đã đối chiếu với vocab thật của model.

## Bất biến được test giữ

| Test | Vì sao |
|---|---|
| `TestNoDuplicateFixGuidePhonemes` | `GetFixGuide` dùng `LIMIT 1`; hai hướng dẫn cùng âm thì một cái không bao giờ hiển thị, và cái nào thắng là tuỳ Postgres |
| `TestEveryMinimalPairPhonemeHasAFixGuide` | sai một cặp rồi bấm "sửa thế nào" phải có gì để đọc |
| `TestEveryContentTagExists` | bắt liên kết hỏng ngay lúc build, khỏi phải dựng database |
| `TestContentHasSubstance` | chặn mục rỗng: hướng dẫn ≥80 ký tự, ≥3 ví dụ |
| `TestFreemiumHasEnoughFreePairs` | ≥10 cặp miễn phí để người dùng mới thấy giá trị trước khi bị chặn |

## Audio cho cặp âm

Drill "nghe rồi chọn" **không tồn tại được** nếu không phát được từ nào, nên `handleTTSBatch`
đã được mở rộng để phủ `minimal_pairs`.

Bốn cột cho mỗi cặp: hai từ × hai accent (`audio_a_us`, `audio_a_uk`, `audio_b_us`,
`audio_b_uk`). Hai accent vì `users.target_accent` cho người học chọn giọng Mỹ hay Anh —
và phát hai giọng khác nhau cho hai từ trong cùng một cặp thì phép so sánh mất ý nghĩa:
người học sẽ nghe ra khác biệt giữa hai **giọng** chứ không phải giữa hai **âm**.

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/backend && go run ./cmd/seed -tts
```

Seed **không** đụng vào các cột `audio_*` — chúng do worker TTS sinh ra, ghi đè bằng NULL
sẽ xoá sạch audio đã tạo mỗi lần chạy lại seed.

## ⚠️ Drill nghe hiện vẫn gian lận được

`MinimalPairService.SubmitAnswer` coi **word_a luôn là đáp án đúng**:

```go
// For simplicity, the "played" word is word_a; real implementation randomizes
playedWord := wordA
```

Ai cũng đạt 100% bằng cách luôn chọn từ bên trái. Trước khi có nội dung thì lỗi này không
chạm tới được; giờ thì có. Sửa đúng phải đổi cả response của
`POST /v1/minimal-pairs/listen/start` (chỉ trả audio của từ được chọn), nên ảnh hưởng cả
app Android — vì vậy tách ra làm riêng thay vì gộp vào đợt seed này.

## Huy hiệu

13 mốc, dày ở đầu rồi thưa dần: người mới cần thấy tiến bộ sớm, người đã gắn bó thì mốc xa
mới còn ý nghĩa. Logic trao huy hiệu ở `ERROR_PROFILE.md`.

⚠️ `description_vi`, `icon_url` và `criteria_ref` phải **khác NULL**. `BadgeService.List`
quét ba cột này vào `string` không phải con trỏ, nên một dòng NULL sẽ làm sập cả endpoint
`/v1/badges` — không chỉ dòng đó.

## Văn bản pháp lý và gói thuê bao — BẢN GIỮ CHỖ

Hai thứ này **không được bịa**, nhưng để bảng rỗng thì endpoint lỗi và app không dựng nổi
màn hình. Cách xử lý: seed bản giữ chỗ có cảnh báo ngay dòng đầu, hiện lên chính màn hình
app chứ không giấu trong comment.

**`legal_documents`** — điều khoản và chính sách riêng tư ràng buộc trách nhiệm pháp lý,
phải do luật sư soạn và phải phản ánh đúng việc app làm gì với dữ liệu. Riêng app này còn
lưu **bản ghi giọng nói**, thuộc dữ liệu sinh trắc học ở nhiều pháp lý.

**`plan_configs`** — `product_id_ios` và `product_id_android` phải khớp **chính xác** mã
khai trong App Store Connect và Google Play Console. Mã hiện tại có tiền tố `PLACEHOLDER`;
sai mã thì thanh toán hỏng đúng lúc người dùng đã quyết định trả tiền.

## `exam_prompts` — cố ý CHƯA seed

Bài thi nói là nói tự do theo đề, không có văn bản chuẩn, nên pronunciation-engine **không
chấm được** (engine cần `reference_text` để tính GOP). `ExamService.Submit` giờ trả 503 và
vẫn lưu bản ghi.

Seed đề vào lúc này chỉ dẫn người học tới ngõ cụt: họ chọn đề, nói hai phút, rồi nhận
"chưa hỗ trợ". Màn hình trống thành thật hơn.

## Đã seed đủ

`app_configs` seed sẵn trong migration `000001`.
