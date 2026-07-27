# R1 — kiểm chứng vocab G2P ↔ vocab model

Cổng đầu tiên của `PRONUNCIATION_ENGINE_PLAN.md` (§9, R1). Chạy **trước** khi viết bất kỳ
dòng service nào.

## Vì sao tồn tại

Chuỗi phoneme do G2P sinh ra phải nằm trọn trong vocab của model. Nếu lệch, hệ thống hỏng
**im lặng** — không exception, chỉ là điểm số vô nghĩa. Ví dụ cụ thể: vocab của
`wav2vec2-xlsr-53-espeak-cv-ft` **không chứa dấu trọng âm** (`ˈ`, `ˌ`), nên gọi
`phonemizer` với `with_stress=True` sẽ sinh token OOV ở mọi câu mà không báo lỗi.

## Chạy

```bash
docker build -t phonara-r1 . && docker run --rm phonara-r1
```

Không cài gì lên máy dev. Container pin sẵn `espeak-ng` đúng phiên bản mà production dùng.

## Kiểm gì

| | Nội dung |
|---|---|
| A | Phonemize 20 câu seed → mọi ký hiệu có nằm trong vocab không |
| B | Ranh giới từ có bảo toàn không (cần cho `word_index`) |
| C | 13 âm mục tiêu của người học Việt có trong vocab không + phủ âm cho benchmark §6.1 |

Exit code `0` = đạt, `1` = không đạt.

## Kết quả lần chạy gần nhất

```
✓ 51/51 ký hiệu nằm trong vocab (447 token / 20 câu)
✓ ranh giới từ bảo toàn qua '|'
✓ 13/13 âm mục tiêu có trong vocab
✗ /ʒ/ không xuất hiện trong bất kỳ câu seed nào  ← lỗ hổng NỘI DUNG, không phải vocab

model_version = xlsr53-espeak@2c733782da5604684829819a5eb744c193fe9398
g2p_version   = espeak-ng-1.52.0
```

## Hai điều phải mang sang `g2p.py`

1. `|` được sinh ra nhưng **không có trong vocab** — giữ để suy `word_index`, nhưng
   **strip trước khi đưa vào `ctc_loss`**.
2. `phonemize()` luôn thừa một `|` ở cuối — `rstrip` trước khi tách từ.

## Khi nào chạy lại

- Đổi `MODEL_REVISION` hoặc đổi model
- Đổi base image / phiên bản `espeak-ng` (`g2p_version`)
- Thêm hoặc sửa câu trong `assessment_questions` → cập nhật `sentences.py` cho khớp
