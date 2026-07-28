# Quy trình benchmark trên giọng người dùng thật

Bước 7 của `PRONUNCIATION_ENGINE_PLAN.md` — **cổng quyết định của cả dự án**.

Cho tới khi có số liệu này, ta **không biết** engine có dùng được cho người dùng thật hay
không. Số liệu speechocean762 (PCC 0,393) chỉ nói engine hoạt động đúng nguyên lý trên
người bản ngữ **tiếng Quan Thoại** — không nói gì về người dùng của Phonara.

## Hai việc khác nhau, đừng gộp

App có giao diện tiếng Việt nên phần lớn người dùng là người Việt, nhưng **không phải tất
cả** — và engine cố ý **không nhận tiếng mẹ đẻ làm đầu vào** (xem docstring
`app/engine/confusion.py`). Vì vậy có hai câu hỏi tách bạch:

| | Câu hỏi | Công cụ | Chi phí |
|---|---|---|---|
| **Hiệu chỉnh** | Điểm số có đúng với người dùng THẬT của ta không? | quy trình dưới đây | ngày công chấm tay |
| **Thiên vị** | Engine có tệ hẳn đi với một tiếng mẹ đẻ nào không? | `eval/compare_l1.py` | ~25 phút máy |

Hiệu chỉnh làm trên **phân bố người dùng thật của mình** — đó là điều đúng đắn, không phải
thiếu sót. Cái phải canh là **thiên vị**: người dùng thiểu số không được nhận một engine
hỏng. Đó là việc của `compare_l1.py`, chạy lại mỗi lần đổi model hoặc đổi artifact.

---

## Vì sao không có đường tắt

Ba cám dỗ, cả ba đều hỏng:

| Ý tưởng | Vì sao không dùng được |
|---|---|
| Dùng TTS sinh giọng "người học" | Đo bộ tổng hợp chứ không đo engine. Bằng chứng: espeak phát `three` mà model nghe `f ɹ iː` — xem plan §3.6.1 |
| Một người chấm cho nhanh | Không tính được đồng thuận, nên không biết nhãn có đáng tin không |
| Lấy điểm Azure làm nhãn | Đo độ giống Azure, không đo độ đúng. Azure cũng sai với người Việt |

Chỉ có một đường: **người Việt thật đọc, người thật chấm**.

---

## Chạy tiền sàng lọc TRƯỚC — đã có, mất 5 phút

Phần đắt của quy trình này là ngày công chấm tay. Trước khi tiêu nó, chạy
`pronunciation-engine/eval/l2arctic.py`: 600 câu của 4 người Việt đã có nhãn sẵn, 20.152
âm vị, chi phí bằng không.

Kết quả 2026-07-28: **AUC 0,8314** (mốc speechocean762: 0,747), không sụt ở nhóm âm nào.
Tức engine **không bị loại** và ngày công chấm tay dưới đây không bị phí — xem
`eval/README-l2arctic.md`.

Nếu lần chạy sau (đổi model, đổi bảng nhầm lẫn) cho AUC tụt mạnh thì **dừng ở đó**, đừng
tổ chức thu âm.

Cùng bộ dữ liệu có **6 tiếng mẹ đẻ**, mỗi thứ 600 câu. Chạy hết rồi so:

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/pronunciation-engine && for L in Vietnamese Chinese Korean Hindi Spanish Arabic; do docker run --rm -v "$PWD":/w -v "$PWD/../datasets/l2arctic/data":/data -w /w phonara-eval python eval/l2arctic.py --parquet /data/scripted-00000-of-00001.parquet --l1 $L --out eval/results-l2arctic-$L.json; done
```

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/pronunciation-engine && docker run --rm -v "$PWD":/w -w /w phonara-eval python eval/compare_l1.py
```

`compare_l1.py` thoát khác 0 nếu chênh lệch AUC giữa L1 tốt nhất và tệ nhất vượt ngưỡng —
dùng được trong CI.

Nó không thay thế được quy trình dưới đây: 4 người không phủ vùng miền, câu là CMU ARCTIC
chứ không phải câu trong app, và **mỗi câu chỉ một người gán nhãn** nên không tính được
đồng thuận — đúng cái cổng chặn ở Bước 4.

---

## Bước 1 — Thu dữ liệu

Không cần công cụ mới. Người tham gia **dùng chính app**; mỗi lượt đọc tự lưu audio, câu
mẫu và kết quả engine vào `assessment_jobs`.

**Yêu cầu mẫu (§6.1):**

| | |
|---|---|
| Số lượt | ≥ 200 |
| Số người | ≥ 20 |
| Trình độ | trải đều sơ cấp → khá |
| Vùng miền | Bắc / Trung / Nam — phương ngữ chuyển di sang tiếng Anh khác nhau |
| Âm mục tiêu | mỗi âm `θ ð ʃ ʒ tʃ dʒ`, phụ âm cuối, cụm phụ âm, `/l/–/n/` xuất hiện ≥ 30 lần |

⚠️ **`/ʒ/` hiện KHÔNG có trong bất kỳ câu nào** của bộ 20 câu seed (phát hiện bởi
`r1/inventory.py`). Thêm người đọc không cứu được — phải bổ sung câu vào
`assessment_questions` trước khi thu: *measure*, *vision*, *usually*, *decision*.

Ghi lại vùng miền và trình độ của từng người vào một bảng riêng, khớp theo `speaker_id`
trong manifest. Không thêm cột vào `users` — đó là nhu cầu nghiên cứu, không phải nhu cầu
sản phẩm.

## Bước 2 — Xuất bộ dữ liệu

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/backend && go run ./cmd/export-benchmark -out ./benchmark-bundle
```

Sinh ra:

```
benchmark-bundle/
  audio/<job_id>.wav      bản ghi để nghe
  manifest.jsonl          câu mẫu + kết quả engine
  labels_A.jsonl          phiếu chấm trống cho người A
  labels_B.jsonl          phiếu chấm trống cho người B
```

## Bước 3 — Chấm tay

**Mỗi người chấm một file riêng, KHÔNG xem của nhau.** Chấm độc lập là điều kiện để tính
đồng thuận; nhìn thấy điểm người kia thì con số đồng thuận trở nên vô nghĩa.

**Cũng không mở `manifest.jsonl`** khi chấm — trong đó có điểm của engine, và thấy nó
trước sẽ neo phán đoán.

Với mỗi âm vị, điền `score`:

| Điểm | Nghĩa |
|---|---|
| **2** | Đúng. Người bản ngữ nghe không thấy gợn. |
| **1** | Nghe ra được nhưng có accent rõ. Không gây hiểu nhầm. |
| **0** | Sai. Thành âm khác, hoặc bị nuốt. |
| `-1` | Chưa chấm (giá trị mặc định) |

Nếu `score < 2` và nghe rõ người học nói âm gì, điền vào `said` (dùng ký hiệu IPA). Không
chắc thì để trống — **đoán bừa làm hỏng dữ liệu nặng hơn là bỏ trống**.

**Thống nhất trước khi bắt đầu.** Chấm thử chung 20 âm vị, so kết quả, thống nhất cách xử
lý ca khó. Bước này tốn 30 phút và quyết định phần lớn chất lượng của cả bộ nhãn.

## Bước 4 — Đo

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/pronunciation-engine && python eval/human_benchmark.py --bundle ../backend/benchmark-bundle
```

Chạy bốn bước theo thứ tự có chủ đích:

**① Đồng thuận giữa người chấm — cổng chặn.** Kappa có trọng số bậc hai. Dưới 0,60 thì
script **dừng** và không in số liệu engine.

Đây không phải sự cẩn thận thừa: nếu hai người chấm không đồng ý với nhau thì **không mô
hình nào đạt được**, và mọi con số phía sau chỉ đang đo độ nhiễu của nhãn. Gặp trường hợp
đó, việc cần làm là sửa hướng dẫn chấm và chấm lại — **không phải chỉnh model**.

**② Chất lượng engine.** PCC, ROC-AUC, và precision/recall **tách riêng**. Tách vì hai
loại lỗi có hậu quả khác hẳn:

- precision thấp → báo sai khi người học nói **đúng** → mất niềm tin nhanh nhất
- recall thấp → bỏ sót lỗi → người học không biết mình sai

**③ Ngưỡng đề xuất** cho `merge_rules.json`.

**④ Tiêu chí thông qua** theo §6.4.

## Bước 5 — Quyết định

| Kết quả | Việc tiếp theo |
|---|---|
| Đạt cả 3 tiêu chí | Fit lại calibration trên dữ liệu này, bỏ hậu tố `-PLACEHOLDER`, rồi mới xoá `/v1/speech/token` |
| Không đạt | **Giữ Azure.** Lever thực sự là đổi model (ZIPA-CR, §10.1) hoặc fine-tune — không phải tinh chỉnh bảng nhầm lẫn, vì thí nghiệm §3.6.4 đã chứng minh lever đó vô hiệu |

---

## Chi phí thật

Phần đắt **không phải code** — bộ đo đã xong. Đắt là:

- ~20 người, mỗi người 15 phút đọc
- **2 người chấm × ~4.000 âm vị** — đây là phần tốn nhất, tính bằng ngày công

Vì vậy đừng bỏ qua bước thống nhất ở Bước 3. Chấm lại vì hướng dẫn không rõ là tốn gấp đôi
phần đắt nhất.
