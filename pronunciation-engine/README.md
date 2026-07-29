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
| 3 | p95 < 3 s cho câu 5 giây | ❌ **TRƯỢT trên VPS** — FP32 RTF 1,51 → 7,6 s; INT8 RTF 0,78 → 3,9 s. Đạt trên máy dev nhưng máy dev không phải phần cứng đích |
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

### Đo lại trên máy rỗi (2026-07-29) — kết luận ở trên ĐƯỢC XÁC NHẬN

> **Ghi chú lịch sử.** Bản đính chính viết ngày 2026-07-28 tuyên bố con số VPS ở trên không
> hợp lệ và gạch bỏ phương án đổi máy. **Bản đính chính đó SAI và đã bị rút.** Nó dựa trên
> ước lượng lý thuyết từ thông số AVX-512 (70–90 GFLOPS thực dụng) chứ không dựa trên phép
> đo; workload thật chỉ đạt ~25–30 GFLOPS. Phần khảo sát máy bên dưới vẫn giữ vì là dữ kiện
> đo được và có ích — chỉ có kết luận rút ra từ nó là sai.

`bench_host.py` chạy trên chính VPS lúc máy rỗi, cùng file 7 giây, 2 luồng:

| | nhanh nhất | trung bình | RTF |
|---|---|---|---|
| FP32 | 9.740 ms | 10.581 ms | **1,51** |
| INT8 | 4.961 ms | 5.480 ms | **0,78** |

**FP32 RTF 1,51** so với 1,68 / 2,51 ghi ở trên. Số `11.730 ms` gốc chỉ lệch 11% — nó
**đúng**, không phải nhiễu; chỉ riêng `17.561 ms` là ngoại lệ. Suy ra mỗi nhân: VPS một
luồng ≈ 10.581 × 1,44 ≈ 15.200 ms, đối chiếu máy dev một luồng 1.282 ms → **11,9×**, nằm
gọn trong khoảng 9–14× đã ghi. **Phần cứng vẫn là ràng buộc chính.**

Điều kiện 3 của §3.6 (p95 < 3 s cho câu 5 giây) với FP32: 5 × 1,51 ≈ **7,6 s — vẫn trượt**.
Với INT8: 5 × 0,78 ≈ **3,9 s — vẫn trượt**, nhưng đã trong tầm với nếu cộng thêm mục 4
(cắt khoảng lặng).

Khảo sát máy, giữ lại vì là dữ kiện:

| | |
|---|---|
| CPU | Intel Xeon Silver 4216 @ 2,10 GHz, 2 vCPU — Cascade Lake |
| Tập lệnh | `avx512f`, `avx512bw`, `avx512dq`, `avx512vl`, **`avx512_vnni`** |
| Swap | **0 B**, `si/so = 0`, `pgmajfault` +1 trong 10 giây → không hề paging |
| CPU steal | **`st = 0`** ở mọi mẫu `vmstat` → không bị hàng xóm ăn CPU |

Không swap và không steal nghĩa là **CPU chậm thật**, chứ không phải máy bị tranh chấp —
đúng như kết luận gốc. Đừng suy tốc độ từ thông số tập lệnh: ước lượng "70–90 GFLOPS thực
dụng" từ một khối AVX-512 FMA mỗi nhân lệch **~3 lần** so với đo đạc. Nghi phạm chính là
"2 vCPU" thực chất là hai siêu luồng trên MỘT nhân vật lý (chung một khối AVX-512), cộng hạ
xung AVX-512 và overhead eager mode. Kiểm bằng `lscpu -e` chứ đừng tin số `CPU(s)`.

`avx512_vnni` **có** giúp — nó là lý do INT8 đạt 1,93× ở đây. Nhưng nó tăng tốc phần INT8,
không cứu được tốc độ nền FP32.

**Còn một hiện tượng riêng chưa giải thích được.** Log production một phiên, cùng engine:
17,1 → 14,7 → 12,9 → 10,2 → 9,1 → 8,5 → 5,1 → **3,7 s**, trong đó câu **dài nhất** (27
phoneme) lại **nhanh nhất**. Với RTF 1,5 ổn định thì một câu ~1,5 giây phải mất ~2,3 s, chứ
không phải 17,1 s. Cửa sổ đó nằm trong 20 phút ngay sau `docker compose up`, nên deploy
chiếm CPU là lời giải thích khả dĩ — nhưng **chưa được kiểm chứng**. Nếu hiện tượng này lặp
lại lúc máy đã ổn định thì nó là một vấn đề riêng, không liên quan tới tốc độ nền.

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
| 2 | Lượng tử hoá động INT8 — `PE_QUANTIZE=int8` | **1,93×** *(đã đo trên VPS)* | **đã dựng** | **phải đo lại độ chính xác** |
| 3 | ONNX Runtime | ~1,5–2× *(ước)* | 1–2 ngày | thêm một trục version (§8) |
| 4 | Cắt khoảng lặng đầu/cuối | tỉ lệ thuận độ dài | vài giờ | ít |
| 5 | **VPS có CPU nhanh hơn mỗi nhân** | **9–14×** *(suy từ đo, đã xác nhận lại)* | 0 | tiền |

Mục 1, 2 và 5 có cơ sở đo đạc; mục 3 là ước lượng từ tài liệu chung, **chưa thử trên engine
này**. Mục 2 nay đã đo trên chính phần cứng đích: **1,93×**, đưa RTF từ 1,51 xuống 0,78.

**Mục 2 là đòn bẩy kỹ thuật lớn nhất**, và điểm mạnh là **đo được**: chạy lại
`eval/l2arctic.py` sau khi lượng tử hoá rồi so AUC với **0,8314** hiện tại. Mất chính xác
bao nhiêu sẽ là con số, không phải phỏng đoán — đây đúng là thứ bộ đo ở §6.1.1 sinh ra để
trả lời.

### `PE_QUANTIZE` — lượng tử hoá động INT8

```bash
docker run --rm -v "$PWD":/w -w /w phonara-engine python bench/bench_host.py 2   # đo trước
```

| | |
|---|---|
| `off` *(mặc định)* | FP32, không đổi gì |
| `int8` | INT8 động trên `nn.Linear` của khối transformer |

**Mặc định TẮT vì nó đổi điểm số, không phải vì nó chậm.** Bật lên là đổi một trục version
(§8): `model_version` mang hậu tố `+int8`, để kết quả trước và sau không bị trộn và để
calibration fit trên FP32 không âm thầm được dùng cho INT8. Engine cũng log cảnh báo đúng
điều đó mỗi lần khởi động ở chế độ này.

Ba điều kiện, mỗi điều fail fast **lúc khởi động** chứ không phải lúc chấm:

1. **Chỉ CPU.** `PE_QUANTIZE=int8` cùng `PE_DEVICE=cuda` bị từ chối — kernel INT8 động là
   fbgemm/qnnpack, đều của CPU, bật cùng GPU là tự bỏ GPU đã trả tiền.
2. **Phải có backend x86** — `x86` **hoặc** `fbgemm`. Trên ARM chỉ có qnnpack, và ở đó
   `bench_host.py` đo được **0,94× — chậm hơn FP32** mà vẫn lệch điểm. Khởi động được
   trong tình trạng đó là kịch bản tệ nhất.

   Engine **không ép** `torch.backends.quantized.engine`. Từ torch 1.13 mặc định trên x86
   là `x86`, một bộ điều phối chọn fbgemm hay onednn theo từng op — trên CPU có VNNI
   thường nhanh hơn fbgemm thuần. Ép về `fbgemm` vừa là tự hạ cấp, vừa khiến
   `bench_host.py` (chạy với mặc định) đo một backend khác với backend production dùng.
3. **Đổi tại chỗ** (`inplace=True`). Mặc định `quantize_dynamic` deepcopy cả model: weights
   FP32 ~1,27 GB, bản sao đẩy đỉnh lên ~2,5 GB, vượt hạn mức 2G của container engine trong
   `docker-compose.prod.yml` → OOMKill lúc khởi động, với triệu chứng không hề chỉ về
   nguyên nhân.

Bộ trích đặc trưng tích chập **không** được lượng tử hoá (`quantize_dynamic` không đụng
`nn.Conv1d`), nên mọi ước lượng tốc độ chỉ tính riêng khối transformer.

Quy trình bật ở production: `bench_host.py` → nếu ≥1,8× thì `eval/l2arctic.py` so AUC với
0,8314 → đặt `PE_QUANTIZE=int8` trong `.env` → dựng lại. Kiểm chứng đã bật đúng bằng
`model_version` trong `/health` (phải có `+int8`), chứ không bằng việc container lên được.

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
