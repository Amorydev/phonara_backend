# pronunciation-engine

Chấm phát âm bằng **alignment-free CTC GOP** trên Wav2Vec2. Không Kaldi, không MFA, không GOPT.

Thiết kế đầy đủ: [`../PRONUNCIATION_ENGINE_PLAN.md`](../PRONUNCIATION_ENGINE_PLAN.md).
Service này là **hàm thuần** — không auth, không rate-limit, không chạm DB. Go backend là
biên giới tin cậy duy nhất (nguyên tắc 6).

## Chạy

```bash
docker build -t phonara-engine . && docker run -d --name phonara-engine-dev -p 8801:8000 phonara-engine
```

```bash
curl -s localhost:8801/health | python3 -m json.tool
```

```bash
curl -s -F "audio=@sample.wav" -F "reference_text=three cats" localhost:8801/v1/assess | python3 -m json.tool
```

Audio phải là **WAV PCM 16-bit, mono, 16 kHz** — đúng thứ Android `AudioRecord` tạo ra.
Ràng buộc chặt này bỏ được ffmpeg khỏi image và loại luôn lớp lỗi "tưởng đã resample".

## Đường ống

```
1. G2P            tokenizer của chính model → chuỗi chuẩn + word_index
2. Forward        Wav2Vec2 CTC → log-probs        ← chạy MỘT lần, chiếm ~99% thời gian
3. GOP            L(P) − max L(nhiễu)             ┐ hai nhánh
4. Decode + NW    chuỗi nghe được → chẩn đoán     ┘ độc lập (nguyên tắc 3)
5. Hợp nhất       τ_uncertain, τ_gop_low/high
6. Tổng hợp       phoneme → word → overall
```

Bước 3 và 4 gần như miễn phí (3 ms mỗi bước); toàn bộ chi phí nằm ở forward pass.

## Trạng thái — điều kiện thoát giai đoạn 1 (§3.6)

| # | Điều kiện | |
|---|---|---|
| 1 | `/v1/assess` trả đúng contract §2.1 | ✅ |
| 2 | Vocab G2P khớp vocab model | ✅ R1 |
| 3 | p95 < 3 s cho câu 5 giây | ⚠️ p95 1,9 s cho audio 2,4 s (RTF 0,249) — cần đo lại trên phần cứng đích |
| 4 | Tái lập kết quả trên speechocean762 | ❌ **chưa làm** |

**Chưa dùng được cho production.** `calibration.json` mang hậu tố `-PLACEHOLDER`: tham
số đặt bằng tay, chưa fit trên bất kỳ dữ liệu nào. Điểm số hiện tại **chưa có ý nghĩa** —
chỉ có thứ tự tương đối là đáng tin. Xem giai đoạn 4 của plan.

## Đo được

| | |
|---|---|
| p50 / p95 | 606 ms / 1895 ms (audio 2,43 s, Docker Desktop trên Apple Silicon) |
| RTF | 0,249 |
| RAM | **451 MiB** — thấp hơn nhiều so với ước tính 1,2 GB trong plan |
| Image | 2,4 GB (gồm weights nướng sẵn) |

RAM thấp làm rủi ro R7 (VPS rẻ) nhẹ hơn hẳn dự kiến.

## Test

```bash
docker build -f tests/Dockerfile -t phonara-pytest . && docker run --rm -v "$PWD":/w -w /w phonara-pytest python -m pytest tests/ -q
```

Test logic thuần, không cần model: Needleman-Wunsch, calibration, tổng hợp, precedence
chẩn đoán mức từ. Trong đó `test_no_clipping_of_positive_gop` là **bắt buộc** — nó chặn
đúng lỗi miền giá trị đã từng có trong plan.

`test_diagnosis.py` cần torch (quy tắc hợp nhất nhận log-probs) nên image test dựng trên
`phonara-engine`, không dùng được image nhẹ cũ. Nó dựng tensor bằng tay thay vì chạy model:
test quy tắc qua model thật sẽ biến một lỗi luật thành một lỗi "model đoán khác hôm nay".

Hai test trong đó là **đối trọng**, không phải test tính năng — `test_uncertain_stays_
uncertain_when_phoneme_present_in_posterior` và
`test_confident_correct_is_never_downgraded_to_omission`. Thiếu chúng thì quy tắc 5 có thể
biến mọi `uncertain` thành `omission` mà suite vẫn xanh, và đó đúng là cách precision tụt.

Smoke test đường ống (cần container đang chạy):

```bash
docker cp r1/smoke.py phonara-engine-dev:/srv/smoke.py && docker exec phonara-engine-dev python /srv/smoke.py
```

> ⚠️ Smoke test dùng `espeak-ng` tổng hợp audio. Nó chứng minh **đường ống thông**, tuyệt
> đối **không** dùng để đánh giá chất lượng — xem mục dưới.

## espeak-ng KHÔNG dùng để đánh giá chất lượng

Đo thực tế trên audio espeak tổng hợp:

| espeak nói | model nghe | chuẩn |
|---|---|---|
| `three cats` | `f ɹ iː k æ t` | `θ ɹ iː k æ t s` |
| `tree cats` | **`θ ɹ iː k æ t z`** | `t ɹ iː k æ t s` |
| `thirty three` | `θ ɚ d i θ ɹ iː` | `θ ɜː ɾ i θ ɹ iː` |

espeak là bộ tổng hợp formant: `/θ/` của nó nghe như `/f/`, còn cụm `/tɹ/` lại nghe thành
`/θɹ/` — **đảo ngược** so với sự thật. Trong "thirty" thì `/θ/` lại đúng. Phụ âm cuối
`/s/`, `/z/` thường bị nuốt.

Điều này **không phải lỗi engine**. Bằng chứng: khi cho đề bài lệch hoàn toàn khỏi audio,
GOP sụp từ khoảng `+1,7…+5,8` xuống `−0,5…−13,0` và accuracy còn 6,5 — engine phân biệt
đúng. Chỉ là audio tổng hợp không đại diện cho giọng người.

Đây chính là lý do plan đặt **giọng người Việt thật** làm cổng quyết định ở giai đoạn 4.

## Artifact

| File | Trục version | Nội dung |
|---|---|---|
| `confusion.json` | `algorithm_version` | bảng nhầm lẫn 2 tầng + nhóm âm |
| `merge_rules.json` | `algorithm_version` | `τ_uncertain`, `τ_gop_low/high` |
| `calibration.json` | `calibration_version` | ánh xạ GOP → 0–100, theo nhóm âm |

Tách `merge_rules` khỏi `calibration` là cố ý — hai trục version khác nhau (§8).

Sửa `confusion.json` xong phải chạy lại:

```bash
docker run --rm -v "$PWD":/w -w /w/r1 phonara-r1 python inventory.py
```

Nó đối chiếu bảng với inventory **thật** của espeak. Đây là cách phát hiện `juː` không hề
tồn tại (espeak trả `j uː`) và `əl`, `ɜː`, `i`, `ɐ`, `ɔ`, `ᵻ` tồn tại nhưng đã bị thiếu —
những thứ không suy ra được bằng kiến thức IPA.
