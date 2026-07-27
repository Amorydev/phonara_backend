# Pronunciation Engine — Kế hoạch triển khai

> **Quyết định kiến trúc:** thay Azure Speech bằng engine self-host dựa trên
> **alignment-free CTC GOP** (Wav2Vec2). Không dùng GOPT, MFA hay Kaldi.
>
> **Bối cảnh:** app chưa phát hành → được quyền thiết kế lại contract và luồng dữ liệu
> đúng bản chất, không mang compatibility debt của Azure.
>
> **Trạng thái:** kế hoạch, chưa triển khai.
>
> **Rev 2** — sửa mâu thuẫn miền giá trị `gop_raw`; định nghĩa confidence cho `omission` và
> loại omission khỏi kiểm tra confidence; thêm precedence chẩn đoán mức từ; chốt cách xử lý
> omission/uncertain trong `overall.accuracy`; thêm `g2p_version`; calibration theo nhóm âm.
>
> **Rev 3** — **R1 đã chạy và ĐẠT** (§9.1). G2P đổi sang dùng tokenizer của chính model thay
> vì gọi `phonemizer` trực tiếp. Chốt `model_version`, `g2p_version`. Phát hiện lỗ hổng nội
> dung: âm `/ʒ/` vắng mặt khỏi toàn bộ 20 câu seed.

---

## 0. Nguyên tắc thiết kế

Bảy nguyên tắc chi phối mọi quyết định phía dưới. Khi có tranh chấp, nguyên tắc thắng.

1. **Engine khai báo năng lực, không giả lập dữ liệu.** Trường nào không đo được thì trả
   `null` kèm `capabilities`. Tuyệt đối không bịa số để lấp schema.
2. **`null` ≠ 0 ≠ omission.** `said_phoneme = null` nghĩa là *chưa xác định*. Chỉ gắn nhãn
   `omission` khi engine có bằng chứng rõ ràng.
3. **Điểm số và chẩn đoán là hai nhánh độc lập.** GOP cho *điểm*; decode + alignment cho
   *chẩn đoán*. Không suy chẩn đoán từ điểm và ngược lại.
4. **Mọi kết quả mang dấu vết phiên bản.** `engine`, `model_version`, `algorithm_version`,
   `calibration_version` lưu cùng mỗi bản ghi. Không có chúng thì dữ liệu lịch sử vô nghĩa
   sau lần đổi model đầu tiên.
5. **Tham số hiệu chỉnh là artifact, không phải code.** Ngưỡng và bảng calibration nằm
   trong file JSON versioned, đổi được mà không cần deploy lại.
6. **Go backend là biên giới tin cậy.** Python service không xác thực, không rate-limit,
   không chạm DB. Nó là hàm thuần: audio + text → kết quả.
7. **Không xóa Azure cho tới khi có số liệu chứng minh.** Azure là chuẩn đối chiếu trong
   suốt giai đoạn 1–4.

---

## 1. Kiến trúc tổng thể

```
Android
  │  POST /v1/assessments  (multipart: audio + reference_text + session_id)
  ▼
Go Backend (biên giới tin cậy)
  ├─ auth · rate-limit · quota · validate
  ├─ lưu audio → S3/MinIO
  ├─ tạo assessment_job (status=pending)
  └─ enqueue asynq task
        │
        ▼
  asynq Worker
        │  POST /v1/assess  (nội bộ, không lộ ra Internet)
        ▼
  Python pronunciation-engine
        │  NormalizedAssessmentResult
        ▼
  Worker chuẩn hóa → practice_item_results + phoneme_scores
        │
        ▼
  Error Profile recompute · Fix Guide
        │
Android  ◄── GET /v1/assessments/{job_id}  (poll: 202 → 200)
```

**Vì sao qua worker thay vì gọi đồng bộ:** inference CPU mất 1–3 giây/câu. Giữ HTTP
connection mở suốt thời gian đó làm cạn connection pool khi có tải. Worker + poll cho
retry, idempotency và backpressure miễn phí — và asynq đã có sẵn trong `internal/worker/`.

**Ranh giới mạng:** `pronunciation-engine` chỉ nghe trên mạng nội bộ Docker. Không expose
port ra ngoài. Không có auth riêng — Go backend là lớp duy nhất tiếp xúc Internet.

---

## 2. Contract — thiết kế trước, code sau

Đây là hợp đồng ba bên (Python ↔ Go ↔ Android). Chốt xong mới viết bất kỳ dòng nào.

### 2.1 Python service — `POST /v1/assess`

**Request** (`multipart/form-data`):

| Field | Kiểu | Bắt buộc | Ghi chú |
|---|---|---|---|
| `audio` | file | ✓ | WAV PCM 16-bit, 16 kHz, mono |
| `reference_text` | string | ✓ | câu cần đọc, tiếng Anh |
| `locale` | string | | mặc định `en-US` |
| `request_id` | string | | để trace log xuyên service |

**Response 200** — `NormalizedAssessmentResult`:

```json
{
  "engine": "ctc_alignment_free",
  "model_version": "xlsr53-espeak@<hf-commit-sha>",
  "g2p_version": "espeak-ng-1.51",
  "algorithm_version": "gop-af-1.0.0",
  "calibration_version": "vi-2026-07-a",
  "capabilities": ["phone_accuracy", "phone_diagnosis", "word_accuracy", "completeness"],
  "audio": { "duration_ms": 3120, "sample_rate": 16000 },
  "timing_ms": { "total": 1840, "forward": 1210, "gop": 480, "diagnosis": 150 },
  "overall": {
    "accuracy": 78.4,
    "fluency": null,
    "completeness": 91.0,
    "prosody": null
  },
  "words": [
    { "word": "three", "word_index": 0, "accuracy": 62.3, "diagnosis": "mispronunciation" }
  ],
  "phonemes": [
    {
      "expected": "θ", "said": "t",
      "word_index": 0, "phoneme_index": 0,
      "accuracy": 41.2, "gop_raw": -2.83,
      "diagnosis": "substitution", "confidence": 0.81
    },
    {
      "expected": "ɹ", "said": null,
      "word_index": 0, "phoneme_index": 1,
      "accuracy": 55.0, "gop_raw": -1.10,
      "diagnosis": "uncertain", "confidence": 0.34
    }
  ]
}
```

**Enum `diagnosis` (phoneme):** `correct` · `substitution` · `omission` · `insertion` · `uncertain`
**Enum `diagnosis` (word):** `correct` · `mispronunciation` · `omission` · `uncertain`

`gop_raw` giữ lại giá trị thô chưa calibrate — cho phép tính lại điểm cho dữ liệu cũ khi
đổi bảng calibration mà không cần chạy lại inference. Đây là lý do trường này tồn tại.

**Response lỗi** — phân biệt tạm thời và vĩnh viễn, vì worker cần biết có nên retry không:

| HTTP | `code` | Retry? | Nguyên nhân |
|---|---|---|---|
| 422 | `audio_too_short` | ✗ | < 300 ms |
| 422 | `audio_too_long` | ✗ | > 30 s |
| 422 | `no_speech_detected` | ✗ | năng lượng dưới ngưỡng |
| 422 | `g2p_failed` | ✗ | reference_text không phiên âm được |
| 503 | `model_overloaded` | ✓ | hàng đợi inference đầy |
| 500 | `internal` | ✓ | lỗi không phân loại |

### 2.2 Go backend — API cho app

```
POST /v1/assessments            → 202 { job_id, status: "pending" }
GET  /v1/assessments/{job_id}   → 202 { status: "processing" }
                                → 200 { status: "done",  result: {...} }
                                → 200 { status: "failed", error: {...} }
```

`POST` nhận `Idempotency-Key` header — gửi lại cùng key trả về job cũ, không tạo job mới.

---

## 3. Giai đoạn 1 — Python `pronunciation-engine`

**Vị trí:** `Phonara_Backend/pronunciation-engine/` (ngang hàng `backend/`)

### 3.1 Cấu trúc

```
pronunciation-engine/
├── pyproject.toml
├── Dockerfile
├── README.md
├── app/
│   ├── main.py            # FastAPI, lifespan, /health, /v1/assess
│   ├── config.py          # settings từ env (pydantic-settings)
│   ├── schemas.py         # contract §2.1 dưới dạng pydantic
│   ├── audio.py           # decode, validate, resample, VAD
│   └── engine/
│       ├── loader.py      # singleton model + warm-up
│       ├── g2p.py         # text → canonical phonemes + word boundaries
│       ├── confusion.py   # nạp bảng nhầm lẫn phoneme
│       ├── gop.py         # alignment-free CTC GOP
│       ├── diagnosis.py   # CTC decode + Needleman-Wunsch
│       ├── calibrate.py   # GOP thô → điểm 0–100
│       └── assess.py      # điều phối → NormalizedAssessmentResult
├── artifacts/
│   ├── confusion_vi.json      # bảng nhầm lẫn, versioned  → algorithm_version
│   ├── merge_rules_vi.json    # τ_uncertain, τ_gop_low/high → algorithm_version
│   └── calibration_vi.json    # tham số calibration        → calibration_version
└── tests/
    ├── test_g2p.py
    ├── test_alignment.py      # Needleman-Wunsch thuần, không cần model
    ├── test_calibrate.py
    ├── test_word_diagnosis.py # bảng precedence §3.2 bước 7, thuần
    └── test_golden.py         # audio cố định → kết quả kỳ vọng
```

**Nguồn của `confusion_vi.json`.** Hai tầng, hợp nhất khi nạp:

1. **Tầng chung** — sinh từ khoảng cách đặc trưng ngữ âm (nơi/phương thức cấu âm, hữu
   thanh). Đảm bảo mọi phoneme đều có ứng viên.
2. **Tầng người Việt** — bổ sung thủ công các cặp lỗi đã biết: `θ→t,s,f` · `ð→d,z` ·
   `ʃ→s` · `ʒ→z,dʒ` · `tʃ→t,ʃ` · `/l/↔/n/` · xóa phụ âm cuối · giản lược cụm phụ âm.

Tầng 2 là nơi kiến thức miền tạo khác biệt, nhưng **phải giữ số ứng viên mỗi phoneme
tương đối đồng đều** — chênh lệch lớn tạo sai lệch cấu trúc trong GOP (xem §3.3).

### 3.2 Thuật toán

**Bước 1 — G2P.** Dùng **`Wav2Vec2PhonemeCTCTokenizer.phonemize()` của chính model**, không
gọi `phonemizer` trực tiếp. `tokenizer_config.json` khai báo sẵn `backend=espeak`,
`lang=en-us` — đi qua tokenizer thì cấu hình G2P khớp với lúc huấn luyện **theo cấu tạo**,
không phải nhờ ta đoán đúng tham số (`with_stress`, `strip`, `separator`…).

Xác nhận thực nghiệm bởi R1 (`pronunciation-engine/r1/`):

```
"three cats" → "θ ɹ iː | k æ t s |"
             → [["θ","ɹ","iː"], ["k","æ","t","s"]]
```

Hai chi tiết bắt buộc, cả hai đều do R1 phát hiện:

- `|` **được sinh ra nhưng KHÔNG có trong vocab.** Giữ nó để suy `word_index`, nhưng phải
  **strip khỏi chuỗi phoneme trước khi đưa vào `ctc_loss`** ở bước 3 — nếu không, token
  ngoài vocab sẽ làm hỏng phép tính likelihood.
- `phonemize()` luôn kết thúc bằng một `|` thừa. Phải `rstrip` trước khi tách từ, nếu không
  từ cuối câu dính `|` vào đuôi.

Vocab dùng token đa ký tự (`iː`, `ɑːɹ`, tối đa 4 ký tự Unicode) nên **không được tách chuỗi
theo từng ký tự** — dùng nguyên phân đoạn cách nhau bằng space mà `phonemize()` trả về.

**Bước 2 — Forward.** Wav2Vec2 CTC → log-probs `[T, V]`. Chạy **một lần duy nhất**; mọi
tính toán sau đều dùng lại tensor này.

**Bước 3 — GOP alignment-free.** Với chuỗi chuẩn `P = [p₁…pₙ]`:

```
L(S)   = log P(S | audio)              # −CTC loss, tính qua CTC forward algorithm
GOPᵢ   = L(P) − max L(P với pᵢ bị nhiễu)

nhiễu tại vị trí i (không bao gồm chính pᵢ):
  · thay pᵢ bằng từng c ∈ Confuse(pᵢ)   (bảng nhầm lẫn, ~4 ứng viên)
  · xóa pᵢ
```

`GOPᵢ` **không** bị chặn trên bởi 0. Miền giá trị thật là `(−∞, +∞)`:

- `GOPᵢ > 0` → chuỗi chuẩn khớp âm thanh hơn *mọi* phương án nhiễu đã thử → phát âm tốt,
  càng dương càng tốt.
- `GOPᵢ ≤ 0` → có ít nhất một phương án nhiễu khớp bằng hoặc hơn → nghi ngờ hoặc sai.

*(Khác GOP cổ điển Witt & Young, nơi max lấy trên toàn bộ tập phone kể cả phone đúng nên
miền luôn `(−∞,0]`. Ở đây tập nhiễu loại trừ chính pᵢ nên tính chất đó mất đi.
`calibrate.py` **không được clip** `gop_raw` vào khoảng có chặn trên — xem test bắt buộc ở §3.5.)*

Bảng nhầm lẫn giới hạn giữ số phép tính ở mức ~5n thay vì ~Vn. Tất cả chuỗi nhiễu được
batch qua `ctc_loss` một lần — phần đắt là forward ở bước 2, đã xong.

**Bước 4 — Chẩn đoán (nhánh độc lập).** CTC greedy decode → chuỗi phoneme *thực sự nghe
được* `D`. Needleman-Wunsch align `P` với `D`, chi phí thay thế lấy từ khoảng cách đặc
trưng ngữ âm:

| Kết quả align | `diagnosis` | `said` | `confidence` |
|---|---|---|---|
| khớp | `correct` | = expected | posterior CTC của phoneme decode tại frame tương ứng |
| lệch | `substitution` | phoneme trong `D` | posterior CTC của phoneme decode tại frame tương ứng |
| khoảng trống ở `D` | `omission` | `null` | posterior của **blank token**, trung bình trên vùng frame NW gán cho vị trí này |
| khoảng trống ở `P` | `insertion` | phoneme thừa | posterior CTC của phoneme decode tại frame tương ứng |

Omission không có phoneme nào được decode để lấy posterior — dùng posterior blank token làm
thước đo độ chắc chắn "không có gì được nói ở đây". Blank cao = bằng chứng **tốt** cho
omission, không phải bất định.

Nhánh này **xử lý được insertion** — điều mà GOP alignment-free trong paper không làm.

**Bước 5 — Hợp nhất.** Hai nhánh có thể mâu thuẫn. Hai tham số mới: `τ_gop_low`,
`τ_gop_high`, sống trong `artifacts/merge_rules_vi.json` — **không** gộp vào
`calibration_vi.json`, vì theo §8 tham số của quy tắc hợp nhất thuộc `algorithm_version`
chứ không phải `calibration_version`; gộp chung file sẽ lẫn ngữ nghĩa hai trục version.
Tinh chỉnh cùng đợt với `τ_uncertain`, trên cùng bộ dữ liệu gán nhãn giai đoạn 4.

1. **Omission được loại trừ khỏi kiểm tra confidence.** Nếu alignment xác định `omission`,
   giữ nguyên `omission` — không hạ xuống `uncertain` dù blank-posterior thấp. Bằng chứng
   cho omission nằm ở sự **vắng mặt** trong `D`, không phải ở độ tự tin của một decode
   không tồn tại.
2. Với `correct`, `substitution`, `insertion`: `confidence < τ_uncertain` →
   `diagnosis = "uncertain"`, `said = null`, bất kể alignment nói gì.
3. Align nói `correct` nhưng `GOPᵢ < τ_gop_low` → giữ `correct`, điểm vẫn thấp (âm đúng
   nhưng méo).
4. Align nói `substitution` nhưng `GOPᵢ > τ_gop_high` → hạ xuống `uncertain` (nhiều khả
   năng lỗi decode).

Quy tắc 1 tồn tại vì omission thật thường rơi vào vùng frame mà posterior dồn vào blank
thay vì bất kỳ phone nào. Không loại trừ, cơ chế hợp nhất sẽ **hệ thống hoá** việc biến
omission thật thành `uncertain` — làm yếu đúng thứ mà nhánh alignment được thiết kế để
phát hiện.

**Bước 6 — Tổng hợp.**

```
overall.completeness = (số phoneme không phải omission / tổng phoneme) × 100
overall.fluency      = null   (v1)
overall.prosody      = null   (v1)

accuracy_pool  = phoneme có diagnosis ∉ {omission, insertion}
word.accuracy  = trung bình accuracy_pool của từ đó       (rỗng → null)
overall.accuracy = trung bình accuracy_pool toàn câu      (rỗng → null)
```

**`omission` bị loại khỏi accuracy, `uncertain` được giữ.** Hai lý do:

- `completeness` đã đo omission rồi. Tính vào cả hai là **phạt kép cùng một lỗi**. Ngoài
  ra, con số GOP của một âm không được phát ra không mang nghĩa "phát âm chính xác đến
  đâu".
- `uncertain` giữ lại vì bất định nằm ở **nhãn**, không ở **điểm**. GOP vẫn tính được bình
  thường; chỉ nhánh alignment không kết luận nổi. Loại nó đi là để nhánh chẩn đoán can
  thiệp vào nhánh điểm — đúng thứ nguyên tắc 3 cấm.

Pool rỗng (đọc sót toàn bộ) → `accuracy = null`, **không phải 0**. Nhờ vậy `accuracy` và
`completeness` trực giao thật sự: phân biệt được "đọc đủ nhưng ngọng" với "bỏ chữ nhưng
chỗ nào đọc thì đọc chuẩn" — đúng sự phân biệt mà Error Profile và Fix Guide cần.

`fluency` cần thông tin thời lượng. Frame index từ CTC decode cho timing gần đúng — khả
thi ở v2, cố tình bỏ ở v1 để không phải calibrate thêm một chiều nữa.

**Bước 7 — Chẩn đoán mức từ.** Áp theo thứ tự, điều kiện đầu tiên khớp thì dừng:

| # | Điều kiện trên các phoneme thuộc từ | `word.diagnosis` |
|---|---|---|
| 1 | 100% phoneme là `omission` | `omission` |
| 2 | ≥1 phoneme là `substitution`, `insertion`, hoặc `omission` (không phải 100%) | `mispronunciation` |
| 3 | Không rơi vào #1/#2, nhưng có ≥1 `uncertain` | `uncertain` |
| 4 | Toàn bộ `correct` | `correct` |

```python
def word_diagnosis(phoneme_diagnoses: list[str]) -> str:
    if all(d == "omission" for d in phoneme_diagnoses):
        return "omission"
    if any(d in ("substitution", "insertion", "omission") for d in phoneme_diagnoses):
        return "mispronunciation"
    if any(d == "uncertain" for d in phoneme_diagnoses):
        return "uncertain"
    return "correct"
```

Tách #1 khỏi #2 vì "bỏ cả từ" là tín hiệu UX khác "nói từ đó nhưng sai" — gộp chung sẽ mất
thông tin mà `completeness` ở mức overall vốn đã tách riêng.

*Giả định ngầm:* không xử lý trường hợp người dùng chèn thêm **cả một từ** không có trong
`reference_text` — ngoài phạm vi use case đọc-theo-câu-cho-trước.

### 3.3 Calibration — phần dễ bị xem nhẹ nhất

GOP thô là hiệu hai log-likelihood, miền giá trị **toàn trục số thực**, không phải
`(−∞, 0]` và không phải điểm 0–100. Dấu là tín hiệu chính (dương = tốt hơn mọi phương án
nhiễu đã thử); độ lớn phản ánh biên độ chênh lệch. Ánh xạ sang thang điểm là bài toán riêng.

- v1: logistic có tham số, nạp từ `artifacts/calibration_vi.json`, nhận đầu vào trên toàn
  trục thực, **không clip**
- **Calibration theo nhóm âm** (vowel / stop / fricative / affricate / nasal / approximant),
  không phải một logistic toàn cục — lý do ở dưới
- Tham số ban đầu fit trên **speechocean762** (có nhãn phoneme do người chấm)
- Giai đoạn 4 fit lại trên dữ liệu người Việt

**Vì sao phải theo nhóm âm.** Số ứng viên trong `Confuse(pᵢ)` khác nhau giữa các phoneme.
Âm có nhiều đối thủ gần sẽ có GOP thấp hơn **một cách có cấu trúc** dù phát âm tốt ngang
nhau. Một logistic toàn cục không sửa được sai lệch này, và tiêu chí "không lớp âm nào sai
hệ thống" ở §6.4 sẽ trượt vì lý do cấu trúc chứ không phải vì model tệ. Nhóm âm là mức chi
tiết phù hợp với ~200 mẫu của giai đoạn 4; per-phoneme quá thưa.

Vì `gop_raw` được lưu, đổi calibration **không cần chạy lại inference** cho dữ liệu cũ.

Cảnh báo: speechocean762 gồm 5.000 câu từ 250 người học có tiếng mẹ đẻ là tiếng Quan Thoại
— **không có người Việt**. Tham số fit trên đó là **điểm khởi đầu**, không phải kết quả.
Đừng đưa lên production trước khi qua giai đoạn 4.

### 3.4 Serving

- **Một** uvicorn worker. Nhiều worker = nhiều bản sao model trong RAM.
- **Inference phải chạy trong threadpool** (`asyncio.to_thread` / `run_in_threadpool`). Một
  lời gọi PyTorch CPU blocking đặt thẳng trong `async def` sẽ chặn event loop — khi đó
  semaphore vô nghĩa vì không có đồng thời thật để giới hạn.
- Semaphore giới hạn inference đồng thời (khởi điểm 2). Vượt → `503 model_overloaded`.
- `torch.set_num_threads(N)` đặt tường minh, **theo tỉ lệ `số core / semaphore`**, để hai
  request đồng thời không tranh CPU của nhau. Mặc định của PyTorch hay tranh với chính nó.
- Warm-up: chạy một inference giả lúc startup. Lần đầu luôn chậm bất thường.
- Model pin theo **commit SHA** của HuggingFace, không chỉ tên. `model_version` ghi SHA đó.
- **Pin phiên bản `espeak-ng` và `phonemizer` trong Dockerfile.** Output G2P có thể đổi âm
  thầm giữa các lần build, và toàn bộ downstream phụ thuộc chuỗi `P` đó. Ghi vào `g2p_version`.
- `/health` chỉ trả `ok` khi model đã nạp **và** warm-up xong.

### 3.5 Kiểm thử

| Loại | Nội dung |
|---|---|
| Unit | Needleman-Wunsch, calibration, G2P word-boundary, precedence chẩn đoán mức từ — thuần, không cần model |
| **Bắt buộc** | `calibrate()` **không clip giá trị dương**. Đây là bẫy trực tiếp của lỗi domain đã sửa ở §3.3 — clip sai sẽ bóp méo điểm của đúng những ca phát âm tốt nhất, và không có test thì hỏng âm thầm |
| Golden | ~10 file audio cố định → snapshot kết quả. Bắt drift khi đổi model/thuật toán |
| Contract | Response validate được bằng pydantic schema ở cả hai phía |
| Load | Tìm ngưỡng đồng thời thực tế trên phần cứng mục tiêu |

### 3.6 Điều kiện hoàn thành giai đoạn 1

Không sang giai đoạn 2 nếu chưa đủ **cả bốn**:

| # | Điều kiện | Trạng thái |
|---|---|---|
| 1 | `/v1/assess` trả đúng contract §2.1 trên audio thật | ✅ |
| 2 | ~~Vocab G2P khớp vocab model~~ | ✅ §9.1 |
| 3 | p50/p95 trên phần cứng đích; p95 < 3 s cho câu 5 giây | ⚠️ một phần — xem dưới |
| 4 | Tái lập kết quả trên speechocean762 tương đương paper | ❌ **chưa làm** |

**Điều kiện 3 — đo được (Docker Desktop / Apple Silicon, audio 2,43 s):**

```
p50 606 ms · p95 1895 ms · RTF 0,249 · RTF ổn định qua 20 lần
breakdown: forward 779 ms · GOP 3 ms · diagnosis 3 ms
```

Hai điều rút ra:

- **Forward pass chiếm ~99% thời gian.** GOP alignment-free và Needleman-Wunsch gần như
  miễn phí. Nghĩa là mọi tối ưu độ trễ về sau phải nhắm vào model (quantize, ONNX, batch),
  không phải vào thuật toán GOP. Cũng nghĩa là **mở rộng bảng nhầm lẫn gần như không tốn
  thêm gì** — lever cho recall ở §6.3 rẻ hơn plan giả định.
- Chưa đo trên phần cứng production thật và chưa đo với câu 5 giây. Đánh dấu một phần.

**Điều kiện 4** — vòng đánh giá đã dựng tại `pronunciation-engine/eval/`. Xem §3.6.2.

### 3.6.2 Đánh giá speechocean762 — một cạm bẫy phải biết

Dataset gán nhãn bằng **ARPAbet** (`AA0`, `IH1`, `NG`), engine dùng **IPA espeak** (`ɑː`,
`ɪ`, `ŋ`). Cần mapping, và `eval/arpabet.py` xác thực nó với vocab lúc nạp.

**Quyết định: chuỗi chuẩn lấy TỪ DATASET, không chạy G2P trên `text`.** Người chấm cho
điểm trên đúng chuỗi phone của dataset; nếu ta sinh chuỗi riêng, nó có thể dài/ngắn khác
đi và không còn tương ứng 1:1 với nhãn — mọi con số tương quan sau đó **vô nghĩa mà vẫn
trông hợp lý**. Đây là dạng lỗi tệ nhất trong đánh giá.

Chữ số trọng âm mang thông tin thật, không strip vô tội vạ: `AH0`→`ə` nhưng `AH1`→`ʌ`;
`ER0`→`ɚ` nhưng `ER1`→`ɜː`.

Dataset còn có trường `mispronunciations` với `canonical-phone`/`pronounced-phone`, trong
đó `<DEL>` map thẳng sang nhãn `omission` của ta — dùng để kiểm chứng riêng recall omission
theo yêu cầu §6.3.

### 3.6.3 Kết quả — toàn bộ 2500 utterance / 47.369 phoneme

```
PCC(gop_raw, điểm người)        +0,3930
ROC-AUC phát hiện lỗi            0,7473
tỷ lệ phoneme bị chấm có lỗi     18,7%
GOP: mean +0,96  sd 3,46  min −14,60  max +10,39
```

| nhóm âm | n | PCC | AUC | GOP mean |
|---|---|---|---|---|
| affricate | 360 | +0,566 | 0,798 | +1,15 |
| nasal | 5.041 | +0,456 | 0,800 | +2,67 |
| fricative | 8.408 | +0,443 | 0,741 | +1,23 |
| approximant | 5.245 | +0,405 | 0,817 | +1,27 |
| stop | 9.332 | +0,398 | 0,782 | +2,26 |
| **vowel** | 18.983 | **+0,361** | **0,682** | **−0,34** |

**R9 được xác nhận bằng số liệu.** PCC chênh 0,36–0,57 và GOP mean chênh −0,34…+2,67 giữa
các lớp âm. Một logistic toàn cục sẽ chấm nasal cao hơn vowel một cách hệ thống dù chất
lượng phát âm như nhau. Calibration theo nhóm âm không phải tinh chỉnh — nó là điều kiện
cần để §6.4 đo được thứ nó định đo.

**Phát hiện mạnh nhất — GOP tách omission gần như hoàn hảo:**

```
396 phoneme bị nuốt (nhãn <DEL> từ mispronunciations)
  GOP mean khi bị nuốt : −6,01
  GOP mean khi có nói  : +1,02
  ROC-AUC tách omission:  0,9478
```

Đây là tin tốt đặc biệt cho người học Việt: **nuốt phụ âm cuối là lỗi số một của họ**, và
chỉ riêng nhánh GOP đã tách được nó ở AUC 0,948 — trước cả khi nhánh chẩn đoán vào cuộc.
Điều này cũng làm quy tắc 1 ở §3.2 bước 5 (miễn trừ omission khỏi kiểm tra confidence)
đáng giá hơn dự tính.

**Định vị so với tài liệu** — đọc kèm cảnh báo bên dưới:

| | PCC phoneme |
|---|---|
| Forced-alignment GOP (baseline trong tài liệu alignment-free) | 0,297 |
| **Engine này — tập thay thế giới hạn ~4 ứng viên** | **0,393** |
| PP-AF GOP UPS — tập thay thế không giới hạn | 0,502 |

Ta nằm **giữa** hai mốc. Giả thuyết ban đầu: khoảng cách do giới hạn tập thay thế, và vì
GOP chỉ tốn 3 ms trong 780 ms nên mở rộng bảng là lever rẻ để thu hẹp.

**Giả thuyết này đã được kiểm chứng và BÁC BỎ.**

### 3.6.4 Thí nghiệm mở rộng tập ứng viên — kết quả âm tính

`--candidates all` (toàn bộ ~50 phoneme tiếng Anh) so với bảng nhầm lẫn (~4 ứng viên),
cùng 400 utterance / 6.568 phoneme:

| | ~4 ứng viên | toàn bộ ~50 | chênh |
|---|---|---|---|
| PCC toàn bộ | +0,3560 | +0,3666 | **+0,0107** |
| AUC phát hiện lỗi | 0,7655 | 0,7663 | +0,0008 |
| Tốc độ | 1,2 utt/s | 1,2 utt/s | không đổi |

Phân rã theo nhóm âm còn lẫn dấu: fricative +0,027 và vowel +0,024 khá hơn, nhưng
affricate −0,059 và stop −0,017 kém đi. Đây là biên độ nhiễu, không phải xu hướng.

**Chênh lệch nhỏ hơn nhiễu lấy mẫu.** Cùng cấu hình `table`, 400 utterance cho PCC 0,356
còn 2.500 utterance cho 0,393 — dao động 0,037 chỉ do cỡ mẫu, gấp ~3 lần hiệu ứng 0,011
của việc mở rộng tập ứng viên.

**Hai kết luận:**

1. **Giữ bảng nhầm lẫn giới hạn.** Nó không đánh đổi chất lượng lấy tốc độ như tài liệu
   gợi ý — ở pipeline này nó gần như miễn phí theo cả hai chiều. Ràng buộc R9 (giữ số ứng
   viên đồng đều) vẫn giữ nguyên giá trị vì lý do sai lệch cấu trúc, không phải vì recall.
2. **Khoảng cách 0,393 → 0,502 đến từ chỗ khác**, chưa xác định. Có thể là chi tiết công
   thức GOP, cách chuẩn hoá, hoặc đơn giản là hai thiết lập không so được với nhau — đúng
   như cảnh báo "định vị, không phải tái lập" ở trên. **Đừng dùng con số 0,502 làm mục
   tiêu** cho đến khi có một phép so sánh chặt chẽ.

**Cảnh báo khi đọc bảng trên:** đây là **định vị**, không phải tái lập chặt chẽ. Pipeline
của ta khác tài liệu ở nhiều điểm (bảng nhầm lẫn, cách lấy chuỗi chuẩn, cách dùng model).
Và tuyệt đối **không** so với PCC 0,612 của GOPT — GOPT là scorer có giám sát đã học ánh
xạ GOP→điểm người, còn đây là chất lượng của bản thân chỉ số GOP chưa qua học.

### 3.6.1 Bài học từ smoke test — audio tổng hợp không dùng để đánh giá

Smoke test dùng `espeak-ng` tổng hợp audio. Kết quả đo:

| espeak nói | model nghe | chuẩn |
|---|---|---|
| `three cats` | `f ɹ iː k æ t` | `θ ɹ iː k æ t s` |
| `tree cats` | **`θ ɹ iː k æ t z`** | `t ɹ iː k æ t s` |

espeak là bộ tổng hợp formant: `/θ/` của nó nghe như `/f/`, cụm `/tɹ/` lại nghe thành
`/θɹ/` — **đảo ngược** so với sự thật.

**Không phải lỗi engine.** Đối chứng: cho đề bài lệch hoàn toàn khỏi audio thì GOP sụp từ
`+1,7…+5,8` xuống `−0,5…−13,0`, accuracy còn 6,5 — engine phân biệt đúng.

Bài học cho §6.1: **không có đường tắt nào thay được giọng người thật.** Mọi ý định dùng
TTS để sinh dữ liệu benchmark đều sẽ đo nhầm bộ tổng hợp thay vì đo engine.

---

## 4. Giai đoạn 2 — Go backend

### 4.1 Migration `000007_assessment_engine_neutral`

Thứ tự trong migration: tạo `assessment_jobs` **trước**, rồi mới `ALTER` bảng kết quả (có
FK trỏ sang).

```sql
-- Nguồn gốc kết quả: bắt buộc để so sánh dữ liệu qua các đời engine
ALTER TABLE practice_item_results
  ADD COLUMN engine              TEXT,
  ADD COLUMN model_version       TEXT,
  ADD COLUMN g2p_version         TEXT,
  ADD COLUMN algorithm_version   TEXT,
  ADD COLUMN calibration_version TEXT,
  ADD COLUMN assessment_question_id UUID
    REFERENCES assessment_questions(id) ON DELETE SET NULL,
  -- truy ngược raw response của engine khi user khiếu nại về điểm
  ADD COLUMN assessment_job_id UUID
    REFERENCES assessment_jobs(id) ON DELETE SET NULL;

-- said_phoneme đã nullable; bổ sung chẩn đoán tường minh
ALTER TABLE phoneme_scores
  ADD COLUMN diagnosis  TEXT
    CHECK (diagnosis IN ('correct','substitution','omission','insertion','uncertain')),
  ADD COLUMN confidence REAL,
  ADD COLUMN gop_raw    REAL;

-- is_omission trở thành cột dẫn xuất, giữ tạm để không phá query cũ
```

`assessment_job_id` là đường duy nhất truy ngược từ một điểm số đáng ngờ về audio gốc và
response thô của engine. Không có nó, mọi khiếu nại "sao tôi đọc đúng mà bị chấm sai" đều
không điều tra được.

Bảng job mới:

```sql
CREATE TABLE assessment_jobs (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id      UUID REFERENCES practice_sessions(id) ON DELETE CASCADE,
  status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','processing','done','failed')),
  audio_ref       TEXT NOT NULL,
  reference_text  TEXT NOT NULL,
  idempotency_key TEXT UNIQUE,
  error_code      TEXT,
  attempts        INT  NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at    TIMESTAMPTZ
);
```

### 4.2 Sửa logic hiện tại

Hai chỗ **bắt buộc** sửa, nếu không dữ liệu sẽ sai âm thầm:

| File | Vấn đề | Sửa thành |
|---|---|---|
| `internal/service/session.go:273` | `isOmission := ph.Said == "" \|\| ph.Accuracy < 10` | đọc thẳng `ph.Diagnosis == "omission"` |
| `internal/service/session.go:377` | `buildMiscue()` bỏ qua word không có `ErrorType` | giữ lại `uncertain`, đừng coi là đúng |

Bỏ hẳn suy diễn từ chuỗi rỗng. `null` là `null`.

### 4.3 Thành phần mới

- `internal/integration/speech/client.go` — HTTP client gọi Python, timeout, phân loại lỗi retry/không-retry
- `internal/integration/storage/` — presigned upload lên MinIO/S3 (**hiện đang rỗng**)
- `internal/handler/assessment.go` — thêm `POST /v1/assessments`, `GET /v1/assessments/{id}`
- `internal/worker/handlers/assess.go` — asynq task: tải audio → gọi engine → chuẩn hóa → lưu

### 4.4 Retry & idempotency

- Retry chỉ với lỗi phân loại `✓` ở §2.1. `422` không bao giờ retry.
- Backoff lũy thừa, tối đa 3 lần, rồi `failed` kèm `error_code`.
- Job có `idempotency_key UNIQUE` — upload lại cùng key trả job cũ.
- Audio giữ tối thiểu tới khi job xong; xóa theo `consent_store_recordings` của user.

---

## 5. Giai đoạn 3 — Android

`AssessmentStage` hiện có `Processing` nhưng chỉ là trạng thái UI giả. Cần thành máy trạng
thái thật:

```
Idle → Recording → Uploading → Processing → Result
                       ↓            ↓
                     Error ◄────────┘
```

Việc cần làm:

- Ghi âm ra **WAV PCM 16 kHz mono** (`AudioRecord`, không dùng `MediaRecorder`) — bỏ được
  ffmpeg khỏi Python service
- Upload multipart + `Idempotency-Key`
- Poll `GET /v1/assessments/{id}` với backoff; timeout tổng ~30 s
- Cho phép hủy, và thử lại **không** tạo job mới
- Trạng thái lỗi phân biệt: mạng · quá ngắn · không nghe thấy tiếng · engine lỗi

**Lưu ý UX cho `uncertain`.** Contract trả `accuracy` cụ thể cạnh `diagnosis = "uncertain"`
— đúng theo nguyên tắc 3 (điểm và nhãn độc lập), nhưng hiển thị thẳng cả hai lên màn hình
sẽ khó hiểu: một con số chính xác đến 0,1 đặt cạnh chữ "không chắc" là thông điệp mâu
thuẫn. Đề xuất: với `uncertain`, hiện âm ở trạng thái trung tính (xám, không đỏ/xanh) và
**không hiện số**; số vẫn lưu trong DB cho phân tích. Quyết định cuối thuộc về thiết kế.

Tầng shared KMP: `SessionApi` cần method mới; `PaRawDto` thay bằng DTO trung lập theo §2.1.

Đây là phần nhiều việc nhất phía client. Đừng bắt đầu trước khi giai đoạn 4 xác nhận engine
dùng được.

---

## 6. Giai đoạn 4 — Benchmark & hiệu chỉnh cho người Việt

**Đây là cổng quyết định của toàn bộ dự án.** Mọi thứ trước đó là chuẩn bị.

### 6.1 Thu dữ liệu

- ≥ 200 bản ghi, ≥ 20 người, trải đều trình độ
- Câu lấy từ chính bộ `assessment_questions` đang có
- **Đặt số lượng tối thiểu cho từng âm mục tiêu**, không chỉ "ưu tiên" định tính: mỗi âm
  trong `θ ð ʃ ʒ tʃ dʒ`, phụ âm cuối, cụm phụ âm, `/l/–/n/` phải xuất hiện ≥ 30 lần. Không
  có sàn này thì tiêu chí "không lớp âm nào sai hệ thống" ở §6.4 không có ý nghĩa thống kê.
- **Trải theo vùng miền** nếu app nhắm toàn quốc. Phương ngữ Bắc/Trung/Nam chuyển di sang
  tiếng Anh khác nhau đủ để lệch benchmark nếu mẫu dồn về một vùng.

**Trạng thái phủ âm của bộ 20 câu hiện tại** (đo bởi R1, §9.1) — 20 người đọc cả bộ:

| Âm | Lần/lượt | × 20 người | Đạt sàn 30? |
|---|---|---|---|
| `l` `n` `s` `ð` `ɹ` `z` | 13–25 | 260–500 | ✓ dư nhiều |
| `ʃ` `v` `f` `θ` `dʒ` | 7–10 | 140–200 | ✓ |
| `tʃ` | 5 | 100 | ✓ |
| **`ʒ`** | **0** | **0** | **✗ phải bổ sung câu** |

`/ʒ/` vắng mặt hoàn toàn. Cần thêm ≥2 câu chứa nó vào `assessment_questions` trước giai
đoạn 4 — ví dụ *measure*, *vision*, *usually*, *decision*, *television*.

### 6.2 Gán nhãn

- **Ít nhất 2 người chấm độc lập**, mức phoneme (đúng/sai) và mức từ
- Tính **inter-rater agreement trước tiên**. Nếu người không đồng ý với người thì không mô
  hình nào đạt được — phải sửa hướng dẫn chấm trước khi đo model
- Chia dev/test; **chỉ tinh chỉnh trên dev**

### 6.3 Chỉ số

| Chỉ số | Ý nghĩa |
|---|---|
| PCC mức phoneme | tương quan điểm với người chấm |
| **Precision và recall tách riêng** | **không chỉ F1 gộp** — xem ghi chú dưới |
| F1 phát hiện lỗi | chất lượng chẩn đoán, quan trọng hơn PCC cho trải nghiệm |
| **Recall riêng của nhãn `omission`** | trực tiếp kiểm chứng quy tắc 1 ở §3.2 bước 5 có hiệu lực không |
| ROC-AUC | dùng để chọn ngưỡng `τ_uncertain`, `τ_gop_low/high` |
| Tỷ lệ `uncertain` | quá cao thì UX vô dụng dù số đẹp |
| Phân rã theo nhóm âm | bắt sai lệch cấu trúc do bảng nhầm lẫn (§3.3) |
| So với Azure trên cùng bộ | Azure là trần tham chiếu |

**Vì sao tách precision/recall.** Hai họ phương pháp lệch ngược hướng nhau: GOP dựa
forced-alignment có recall rất cao nhưng precision thấp (gắn nhầm phát âm đúng thành lỗi),
còn alignment-free thường vượt ở hầu hết chỉ số **ngoại trừ recall**. F1 gộp che mất sự
đánh đổi này, mà với sản phẩm học phát âm thì precision thấp (báo sai khi user nói đúng)
gây mất niềm tin nhanh hơn recall thấp.

**Nếu recall không đạt:** mở rộng `Confuse(pᵢ)` là lever có cơ sở, không phải dấu hiệu sai
hướng — tài liệu cho thấy không giới hạn tập thay thế cho recall/MCC cao hơn, đổi lại chi
phí tính toán. Cân bằng lại với ràng buộc độ trễ ở §3.6.

### 6.4 Tiêu chí thông qua

Chỉ bỏ Azure khi **cả ba** đạt:

1. F1 phát hiện lỗi ≥ 80% mức của Azure trên cùng bộ dữ liệu
2. Tỷ lệ `uncertain` < 20% số phoneme
3. Không có lớp âm nào bị sai hệ thống (ví dụ: mọi `θ` đều báo lỗi)

Không đạt → giữ Azure, quay lại tinh chỉnh. Đây là lý do các giai đoạn trước không được
xóa `/v1/speech/token`.

---

## 7. Giai đoạn 5 — Vận hành

- Metric: độ trễ p50/p95, tỷ lệ lỗi theo `error_code`, độ sâu hàng đợi, phân bố `uncertain`
- Cảnh báo khi phân bố `uncertain` hoặc điểm trung bình dịch chuyển đột ngột → dấu hiệu
  drift hoặc lỗi deploy
- GPU chỉ khi p95 CPU không đạt yêu cầu. Đo trước, đừng mua trước.
- Rate-limit ở Go backend, không ở Python
- Xóa `/v1/speech/token`, `AzureConfig`, `TokenBrokerService` — **bước cuối cùng**, sau khi
  §6.4 đạt và chạy ổn định ít nhất 2 tuần

---

## 8. Chiến lược phiên bản

| Trường | Đổi khi | Artifact liên quan |
|---|---|---|
| `model_version` | đổi checkpoint HuggingFace | — (pin commit SHA) |
| `g2p_version` | đổi phiên bản `espeak-ng` / `phonemizer` | — (pin trong Dockerfile) |
| `algorithm_version` | đổi công thức GOP, bảng nhầm lẫn, quy tắc hợp nhất | `confusion_vi.json`, `merge_rules_vi.json` |
| `calibration_version` | đổi tham số ánh xạ điểm | `calibration_vi.json` |

Cả bốn lưu cùng mỗi bản ghi. Biểu đồ tiến bộ của user **phải lọc theo bộ bốn này** hoặc
tính lại từ `gop_raw` — nếu không, một lần đổi calibration sẽ tạo ra bước nhảy giả trong đồ
thị và user tưởng mình đột nhiên giỏi/dở đi.

`g2p_version` dễ bị bỏ sót nhất: nó không nằm trong code, không nằm trong artifact, mà nằm
trong base image. Không pin thì một lần `docker build` lại có thể đổi chuỗi `P` và kéo theo
mọi thứ phía sau, không để lại dấu vết nào.

Rollback: giữ artifact calibration cũ trong repo. Đổi env var, restart, không cần deploy.

---

## 9. Sổ rủi ro

| # | Rủi ro | Ảnh hưởng | Giảm thiểu |
|---|---|---|---|
| ~~R1~~ | ~~Vocab espeak G2P không khớp vocab tokenizer của model~~ | — | **✅ ĐÃ ĐÓNG** — xem §9.1. Rủi ro biến mất khi dùng tokenizer của model làm G2P |
| R2 | Model không tương quan với người Việt | Dự án không dùng được | Cổng §6.4; Azure vẫn chạy |
| R3 | CPU quá chậm | Trải nghiệm tệ | Đo ở §3.6 trước khi xây tiếp |
| R4 | Tỷ lệ `uncertain` quá cao | UX vô dụng dù số đẹp | Đưa vào tiêu chí thông qua |
| R5 | Calibration lệch → điểm vô nghĩa | Mất niềm tin user | `gop_raw` cho phép tính lại |
| R6 | Không có insertion trong GOP | Bỏ sót lỗi thêm âm | Nhánh chẩn đoán xử lý |
| ~~R7~~ | ~~RAM không đủ trên VPS rẻ~~ | Thấp hơn dự kiến | **Đo được: 451 MiB** lúc chạy (ước tính cũ 1,2 GB sai). VPS 1 GB là đủ. Image 2,4 GB do nướng weights sẵn — là dung lượng đĩa, không phải RAM |
| R8 | Đảo chiều luồng làm tăng độ trễ cảm nhận | Bỏ ngang | UI processing tường minh; đo end-to-end |
| R9 | Bảng nhầm lẫn lệch số ứng viên → GOP sai lệch theo lớp âm | Trượt §6.4 vì lý do cấu trúc, không phải model tệ | Calibration theo nhóm âm (§3.3); giữ số ứng viên đồng đều; phân rã chỉ số theo nhóm âm ở §6.3 |
| R10 | `espeak-ng` đổi phiên bản âm thầm khi build lại | Kết quả đổi không rõ nguyên nhân | Pin trong Dockerfile, ghi `g2p_version` (§8) |

### 9.1 R1 — kết quả (đã chạy)

Harness: `pronunciation-engine/r1/` (Docker, không đụng máy dev). Chạy lại: `r1/README.md`.

| Hạng mục | Kết quả |
|---|---|
| Ký hiệu G2P nằm trong vocab | **51/51 — 100%**, trên 447 token của 20 câu seed |
| Ranh giới từ | bảo toàn qua `\|`, tách được `word_index` |
| 13 âm mục tiêu người Việt | **toàn bộ có trong vocab** |
| License model | `apache-2.0` — không vướng thương mại |

**Giá trị chốt để pin:**

```
model_version = xlsr53-espeak@2c733782da5604684829819a5eb744c193fe9398
g2p_version   = espeak-ng-1.52.0        (python:3.11-slim / Debian)
vocab size    = 392 token (đa ngôn ngữ; tập con tiếng Anh dùng tới: 51)
```

**Vì sao R1 chuyển từ rủi ro thành không-rủi-ro:** ban đầu ta định gọi `phonemizer` trực
tiếp và *hy vọng* tham số trùng với lúc huấn luyện. Phát hiện `tokenizer_config.json` khai
báo sẵn backend/lang cho phép dùng chính tokenizer của model — sai lệch G2P không còn là
thứ có thể xảy ra, thay vì là thứ ta phải canh. Vocab **không chứa dấu trọng âm** (`ˈ`,
`ˌ`), nên nếu gọi `phonemizer` thẳng với `with_stress=True` thì mọi câu đều sinh token OOV.
Đó chính là kịch bản hỏng-im-lặng mà R1 sinh ra để chặn.

**Phát sinh — không phải lỗi vocab, mà là lỗ hổng nội dung:** âm **`/ʒ/` không xuất hiện
trong bất kỳ câu nào** của 20 câu seed. Thêm người đọc không cứu được. Phải bổ sung câu vào
`assessment_questions` trước giai đoạn 4, nếu không tiêu chí "không lớp âm nào sai hệ
thống" (§6.4) sẽ không đánh giá được `/ʒ/`. Xem §6.1.

12 âm mục tiêu còn lại đều dư sàn ≥30 lần khi 20 người đọc cả bộ (thấp nhất `tʃ`: 100 lần).

---

## 10. Cổng quyết định

```
R1 kiểm tra vocab
   ├─ khớp → tiếp
   └─ lệch → xử lý mapping hoặc đổi model    ← nửa ngày
   (song song, không chặn: bench thử ZIPA-CR — xem §10.1)

Giai đoạn 1: Python service
   └─ §3.6 đủ 4 điều kiện?
        ├─ có → tiếp
        └─ không → dừng, đánh giá lại        ← ~1 tuần

Giai đoạn 4: Benchmark người Việt            ← ~2 tuần (gồm gán nhãn)
   └─ §6.4 đủ 3 tiêu chí?
        ├─ có → giai đoạn 2, 3, 5
        └─ không → giữ Azure, tinh chỉnh

Giai đoạn 2+3: Backend + Android              ← ~2–3 tuần
Giai đoạn 5: Vận hành + xóa Azure             ← sau 2 tuần ổn định
```

**Điểm quan trọng:** giai đoạn 4 (benchmark) đứng **trước** giai đoạn 2 và 3 trong thứ tự
rủi ro, dù đánh số sau. Để lấy 200 mẫu **không cần** xây luồng upload hoàn chỉnh — dùng
script hoặc trang web tạm. Xây backend và Android trước khi biết model có dùng được không
là rủi ro lớn nhất trong kế hoạch này.

---

### 10.1 Model — ứng viên chính và ứng viên phụ

**Chính: `facebook/wav2vec2-xlsr-53-espeak-cv-ft`.** Fine-tune từ `wav2vec2-large-xlsr-53`
trên CommonVoice cho nhãn ngữ âm đa ngôn ngữ. License Apache-2.0 — không vướng thương mại.
Tích hợp sẵn qua `transformers`, chi phí engineering thấp nhất. Đây là ứng viên để R1 kiểm.

**Phụ: ZIPA-CR.** Họ model phone recognition mới hơn (2025), huấn luyện noisy-student trên
hơn 11.000 giờ pseudo-label thuộc hơn 4.000 ngôn ngữ, license permissive. Checkpoint chính
ra đời ~2021–2022 nên đáng bench thử song song.

Nếu thử ZIPA, **bắt buộc dùng biến thể CTC (ZIPA-CR), không phải transducer (ZIPA-T)** —
toàn bộ công thức GOP ở §3.2 phụ thuộc việc tính log-likelihood của chuỗi tuỳ ý qua CTC
forward algorithm; transducer không cho làm việc đó theo cùng cách.

ZIPA có thể chưa tích hợp gọn qua `transformers` như xlsr-53-espeak → chi phí engineering
cao hơn. **Là benchmark phụ, không phải blocker cho R1.**

---

## 11. Việc chưa quyết

- Giữ hay bỏ `is_omission` sau khi có `diagnosis` (đề xuất: bỏ ở migration sau, khi không
  còn query nào dùng)
- Chính sách lưu audio dài hạn — phụ thuộc `consent_store_recordings` và quy định riêng tư
- `fluency` v2: có đáng làm không, hay để `null` vĩnh viễn
- Ngưỡng `τ_uncertain`, `τ_gop_low`, `τ_gop_high` khởi điểm — chờ số liệu §6.3
- Hiển thị `uncertain` phía Android (§5) — thuộc thiết kế

**Đã chốt trong bản này:** domain của `gop_raw` (§3.2 bước 3, §3.3) · confidence cho
omission (§3.2 bước 4–5) · precedence chẩn đoán mức từ (§3.2 bước 7) · xử lý
omission/uncertain trong `overall.accuracy` (§3.2 bước 6).
