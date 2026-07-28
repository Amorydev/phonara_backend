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
| 3 | p95 < 3 s cho câu 5 giây | ❌ **TRƯỢT trên VPS** — RTF 1,7–2,5, câu 5 giây mất 9–13 s. Đạt trên máy dev nhưng máy dev không phải phần cứng đích |
| 4 | Tái lập kết quả trên speechocean762 | ❌ **chưa làm** |

**Chưa dùng được cho production.** `calibration.json` mang hậu tố `-PLACEHOLDER`: tham
số đặt bằng tay, chưa fit trên bất kỳ dữ liệu nào. Điểm số hiện tại **chưa có ý nghĩa** —
chỉ có thứ tự tương đối là đáng tin. Xem giai đoạn 4 của plan.

## Đo được

**CÙNG MỘT FILE 7,0 giây** (16 kHz mono), đo `timing_ms.forward` do chính engine báo:

| | forward | RTF | |
|---|---|---|---|
| Máy dev — Apple Silicon, 7 luồng torch | 599–884 ms | **0,09–0,13** | sau khi ấm |
| Máy dev — lần gọi ĐẦU sau khi rỗi | 2.652 ms | 0,38 | chậm gấp ~4 lần |
| **VPS SSD Nodes** (Ubuntu 20.04, x86) | **11.730 / 17.561 ms** | **1,68 / 2,51** | hai lần đo cách nhau vài phút |

Chênh lệch máy dev ↔ VPS là **20–30 lần**, không phải khác biệt thế hệ CPU thông thường.

Ba điều số liệu này nói:

**1. Toàn bộ thời gian nằm ở forward pass.** GOP 57–262 ms, chẩn đoán 3–22 ms. Tối ưu
thuật toán không giúp gì — chỉ có thể tác động vào bước chạy model.

**2. RTF > 1 trên VPS** nghĩa là xử lý lâu hơn cả độ dài audio. Câu 5 giây mất 9–13 s. Điều
kiện 3 của §3.6 (p95 < 3 s) **trượt**.

**3. Dao động lớn** (11,7 → 17,6 s giữa hai lần đo) — dấu hiệu tranh chấp CPU. VPS này còn
chạy hai project khác.

⚠️ **Con số cũ trong tài liệu này (RTF 0,249, p95 1,9 s) là đo trên Apple Silicon.** Nó
từng được ghi kèm chú thích "cần đo lại trên phần cứng đích" — giờ đã đo, và thực tế tệ hơn
gần 7 lần so với con số lạc quan đó. Đừng dùng số máy dev để lập kế hoạch hạ tầng.

**VPS: 2 vCPU, 8 GB RAM.** Với `max_concurrent_inference = 2`, engine đang chạy **1 luồng**.

Giả thuyết "chậm vì thiếu luồng" đã **đo và bác bỏ**. Chạy engine ở nhiều mức luồng trên
cùng máy dev, cùng file:

| luồng | forward (ấm) | so với 1 luồng |
|---|---|---|
| 1 | 1.282 ms | — |
| 2 | 890 ms | 1,44× |
| 4 | 764 ms | 1,68× |
| 8 | 706 ms | 1,82× |
| 14 | 652 ms | **1,97×** |

Bão hoà sau 2–4 luồng. Máy dev **một luồng** vẫn là 1.282 ms trong khi VPS **một luồng** là
11.730–17.561 ms — chậm hơn **9–14 lần trên mỗi nhân**. Đây là CPU chậm thật.

**Hệ quả khi chọn máy:** mua thêm vCPU trên cùng dòng CPU gần như vô ích (2→4 luồng chỉ
1,16×). Cái cần là **tốc độ mỗi nhân**, không phải số nhân.

| | |
|---|---|
| RAM | **451 MiB** — thấp hơn nhiều so với ước tính 1,2 GB trong plan |
| Image | 2,4 GB (gồm weights nướng sẵn) |

RAM thấp làm rủi ro R7 (VPS rẻ) nhẹ hơn hẳn dự kiến — nhưng **tốc độ** thì không, và đó mới
là ràng buộc thật.

## Có cải thiện được tốc độ không

| # | Cách | Ước tính | Công sức | Rủi ro |
|---|---|---|---|---|
| 1 | `PE_TORCH_THREADS=2`, `PE_MAX_CONCURRENT_INFERENCE=1` | **~1,4×** *(đã đo)* | vài phút | mất chạy song song |
| 2 | Lượng tử hoá động INT8 | ~2–3× *(ước)* | nửa ngày | **phải đo lại độ chính xác** |
| 3 | ONNX Runtime | ~1,5–2× *(ước)* | 1–2 ngày | thêm một trục version (§8) |
| 4 | Cắt khoảng lặng đầu/cuối | tỉ lệ thuận độ dài | vài giờ | ít |
| 5 | **VPS có CPU nhanh hơn mỗi nhân** | **9–14×** *(suy từ đo)* | 0 | tiền |

Chỉ mục 1 và 5 có cơ sở đo đạc. Mục 2–3 là ước lượng từ tài liệu chung về suy luận
transformer trên CPU, **chưa thử trên engine này**.

**Mục 1 làm trước** vì miễn phí — nhưng chỉ được ~1,4×, không cứu được điều kiện 3.

**Mục 2 là đòn bẩy kỹ thuật lớn nhất**, và điểm mạnh là **đo được**: chạy lại
`eval/l2arctic.py` sau khi lượng tử hoá rồi so AUC với **0,8314** hiện tại. Mất chính xác
bao nhiêu sẽ là con số, không phải phỏng đoán — đây đúng là thứ bộ đo ở §6.1.1 sinh ra để
trả lời.

**Trần của phần mềm:** ngay cả khi mục 1 + 2 + 3 đạt hết mức lý thuyết (~1,4 × 3 × 2 ≈ 8×),
11,7 s xuống còn ~1,5 s cho câu 7 giây — vừa đủ, nhưng chưa tính dao động do tranh chấp CPU
và phải chấp nhận rủi ro mất chính xác. **Phần cứng vẫn là ràng buộc chính.**

## Chạy trên GPU

```bash
docker build -f Dockerfile.gpu -t phonara-engine-gpu . && docker run --gpus all -e PE_DEVICE=cuda -p 8000:8000 phonara-engine-gpu
```

`Dockerfile.gpu` khác `Dockerfile` **đúng một dòng**: cài PyTorch bản CUDA thay vì bản
CPU-only. Image phình từ ~3,8 GB lên ~7–9 GB.

| `PE_DEVICE` | |
|---|---|
| `auto` *(mặc định)* | dùng GPU nếu có, ngược lại CPU |
| `cpu` | ép CPU kể cả khi có GPU |
| `cuda` | bắt buộc GPU — **ném lỗi lúc khởi động** nếu không có |

Dùng `cuda` chứ đừng dùng `auto` trên máy production có GPU. Bản CPU-only của PyTorch không
chứa kernel CUDA nào, nên chạy nó trên máy GPU sẽ **âm thầm rơi về CPU** — trả tiền thuê GPU
mà chạy bằng CPU, và chỉ phát hiện khi nhìn hoá đơn. `cuda` biến chuyện đó thành lỗi to
tiếng ngay lúc khởi động.

**Chỉ forward pass chạy trên GPU.** Log-probs được kéo về CPU ngay sau đó, nên `gop.py` và
`diagnosis.py` không cần biết GPU tồn tại. Lý do: hai bước ấy cộng lại chỉ 60–280 ms trong
khi forward chiếm ~99% thời gian — sửa chúng để nhận thiết bị là mở rộng bề mặt lỗi cho một
khoản lời rất nhỏ. Ranh giới thiết bị nằm gọn ở một chỗ duy nhất trong `assess.run`.

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
