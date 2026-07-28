# Tiền sàng lọc trên giọng người Việt — L2-ARCTIC

`BENCHMARK_PROTOCOL.md` (bước 7) tốn ~20 người đọc và **2 người × ~4.000 âm vị chấm tay**.
Trước khi bỏ ngần ấy ngày công, chạy cái này: 600 câu của 4 người Việt đã có sẵn nhãn.

## Lấy dữ liệu

```bash
hf download Codec-SUPERB/L2Arctic --repo-type dataset --local-dir datasets/l2arctic
```

⚠️ **CC BY-NC 4.0.** Đo được, **không** fit tham số ship kèm sản phẩm, không phát hành lại,
không commit vào git (473 MB + ràng buộc giấy phép). `.gitignore` phải có `datasets/`.

## Chạy

```bash
docker build -f eval/Dockerfile -t phonara-eval . && docker run --rm -v "$PWD":/w -v "$PWD/../datasets/l2arctic/data":/data -w /w phonara-eval python eval/l2arctic.py --parquet /data/scripted-00000-of-00001.parquet
```

Đối chứng — cùng script, cùng thang đo, người bản ngữ tiếng Quan Thoại:

```bash
docker run --rm -v "$PWD":/w -v "$PWD/../datasets/l2arctic/data":/data -w /w phonara-eval python eval/l2arctic.py --parquet /data/scripted-00000-of-00001.parquet --l1 Chinese --out eval/results-l2arctic-zh.json
```

## Dữ liệu trông thế nào

| | |
|---|---|
| Câu người Việt | 600 — HQTV, PNV, THV, TLV × 150 |
| Âm vị chuẩn | ~20.150 |
| Audio | WAV 16 kHz mono 16-bit — không cần resample |
| `g2p` | phiên âm **chuẩn** (từ điển) |
| `ipa` | phiên âm **người gán nghe thấy** |

Nhãn lỗi suy ra bằng cách căn hai cột đó. Tỷ lệ lỗi ~26% — cân hơn speechocean762 (~15%,
lệch nặng về "đúng"), nên số liệu ổn định hơn.

Lỗi hay gặp nhất, khớp đúng `tier_vi` của `confusion.json`:

```
ð → d  354      nuốt: t 200   d 190   ɹ 188   l 140
ɝ → ʌ  236            s  85   n  77   z  74   k  44
ɪ → i  214
z → s  150      ← hữu thanh hoá ngược ở phụ âm cuối
l → w  102
ʃ → s   41
v → b   38
```

Chuỗi nghe-thấy **ngắn hơn** chuỗi chuẩn ở 484/600 câu (81%) — nuốt phụ âm cuối, đúng như
§6.1 dự đoán.

## Bốn quyết định thiết kế

### 1. Nhãn suy TRƯỚC khi map ký hiệu

Cả `g2p` lẫn `ipa` viết bằng bộ ARPAbet; so chúng với nhau là so cùng hệ. Map sang espeak
trước khi căn thì chỉ một vế đổi ký hiệu và mọi âm vốn khớp hoá thành substitution giả.

Không phải lo xa — đã đo: làm sai thứ tự thổi tỷ lệ lỗi nguyên âm từ **26,7% lên 40,6%**.
Bất biến để kiểm: `--refine espeak` và `--refine off` phải cho **cùng** tỷ lệ lỗi.

### 2. Khôi phục nguyên âm giảm (`--refine espeak`, mặc định)

`AH0`=ə và `AH1`=ʌ khác nhau, nhưng cột `g2p` đã vứt chữ số trọng âm → mọi schwa đội lốt
`ʌ`. Không sửa thì GOP bị phạt oan ở mọi âm tiết không trọng âm.

Khôi phục bằng cách hỏi espeak — chính G2P mà model được huấn luyện cùng và production
dùng thật. Chỉ đổi **ký hiệu**, giữ nguyên số lượng và thứ tự, nên chỉ số nhãn không xê
dịch.

Ảnh hưởng đo được (60 câu đầu):

| | `--refine off` | `--refine espeak` |
|---|---|---|
| AUC tổng | 0,838 | **0,858** |
| AUC nguyên âm | 0,743 | **0,793** |
| AUC phụ âm | 0,868–0,935 | 0,869–0,936 *(không đổi)* |
| precision | 0,561 | **0,641** |

Chỉ nguyên âm đổi. Đó là bằng chứng phép sửa đúng chỗ chứ không phải chỉnh cho đẹp số.

### 3. Tách chuỗi bằng tập đóng ARPAbet

Hai cột là chuỗi IPA **liền, không dấu phân cách**. Tách theo ký tự sẽ sai vì `tʃ`, `dʒ`,
`aɪ`, `eɪ`, `oʊ`, `aʊ`, `ɔɪ` là một âm vị viết bằng hai ký tự.

Tập ký hiệu dẫn xuất từ 39 phone ARPAbet nên "tập đóng" là tính chất cấu tạo: ký tự lạ rơi
vào `unsegmentable` và script kêu lên. Thực tế: **0 ký tự không tách được** trên cả 600 câu.

Còn lại ~1,4% quyết định nhập nhằng thật (`tʃ` vs `t`+`ʃ`, `dʒ`, `ɔɪ`) — dataset đã vứt
ranh giới âm vị nên không khôi phục được. Script **đếm và báo** con số đó thay vì giả vờ
phép tách là chính xác.

### 4. Gộp `ə` của người gán về `ʌ` — chỉ ở cột `ipa`

Cái này do **chính cơ chế cảnh báo ở mục 3 phát hiện**, khi chạy đối chứng người Trung:

```
ký tự không tách được : {'ə': 84}
→ ĐỪNG tin số liệu bên dưới.
```

Người gán đôi khi viết `ə` dù ARPAbet không có ký hiệu riêng cho schwa. Xuất hiện ở **5/6
nhóm L1** — người Việt là ngoại lệ duy nhất với 0 lần, nên số liệu tiếng Việt không bị ảnh
hưởng.

Gộp về `ʌ` thay vì nhận làm ký hiệu thứ 40: cột chuẩn `g2p` không có cách nào biểu diễn
schwa, nên coi `ə` khác `ʌ` là chấm điểm một phân biệt mà **bản tham chiếu không diễn đạt
được** — mọi nguyên âm giảm sẽ hoá thành lỗi giả. Hai vế phải ở cùng mức chi tiết.

Chỉ áp dụng cho cột `ipa`. Ký hiệu lạ ở `g2p` vẫn phải kêu lên: cột đó do từ điển sinh ra,
có ký hiệu ngoài bộ nghĩa là dataset đã đổi chứ không phải người gán viết thoáng.

## Kết quả (chạy 2026-07-28, toàn bộ 600 câu mỗi nhóm)

Toàn bộ 6 tiếng mẹ đẻ, cùng một engine, cùng một bộ artifact (`eval/compare_l1.py`):

| Tiếng mẹ đẻ | Âm vị | AUC | Lỗi thật | P | R | recall om | said | `unc` |
|---|---|---|---|---|---|---|---|---|
| **Việt** | 20.152 | **0,8314** | 25,9% | 0,637 | 0,577 | 0,611 | 72% | 14,7% |
| Ả Rập | 19.724 | 0,8012 | 11,1% | 0,317 | 0,513 | 0,353 | 63% | 15,1% |
| Hindi | 19.763 | 0,7771 | 13,8% | 0,329 | 0,481 | 0,361 | 65% | 16,9% |
| Hàn | 19.710 | 0,7717 | 12,9% | 0,375 | 0,403 | 0,372 | 82% | 12,8% |
| Tây Ban Nha | 20.067 | 0,7572 | 18,3% | 0,414 | 0,402 | 0,416 | 65% | 15,3% |
| Quan Thoại | 19.796 | 0,7484 | 18,1% | 0,427 | 0,435 | 0,483 | 73% | 14,6% |
| *(mốc)* speechocean762 | 47.369 | *0,747* | *~15%* | — | — | — | — | — |

### Engine có thiên vị theo tiếng mẹ đẻ không? — Không

**Chênh lệch AUC 0,083.** Thấp nhất (0,7484) vẫn ngang mốc speechocean762. Không thứ tiếng
nào sụp đổ, nên giả định "một cấu hình dùng chung cho mọi người học" đứng vững — và app
không cần biết người học nói tiếng gì.

**Precision chênh 2× (0,317 → 0,637) nhưng đó KHÔNG phải thiên vị:**

```
corr(tỷ lệ lỗi nền, precision) = +0,965   ← gần như hoàn hảo
corr(tỷ lệ lỗi nền, AUC)       = +0,386   ← yếu
```

Với ngưỡng cố định, tỷ lệ lỗi nền càng thấp thì cùng một số lần báo động sinh ra càng nhiều
báo nhầm. Đó là **số học**, không phải engine kém với nhóm nào. Tương quan AUC thấp là đối
chứng: AUC đo chất lượng xếp hạng thật chứ không chỉ phản chiếu độ khó của bài toán.

Hệ quả sản phẩm, **không liên quan tiếng mẹ đẻ**: người học **ít lỗi** sẽ thấy nhiều báo
động sai hơn. Đó là chuyện **trình độ**. Cần gạt đúng là **ngưỡng thích ứng theo lịch sử
từng người** — trung lập với L1 theo cấu tạo, và xử lý luôn cả trục trình độ.

**Hai điểm yếu có cấu trúc**, theo dõi chứ chưa chặn:

| Nhóm âm | Chênh giữa các L1 | Tệ nhất |
|---|---|---|
| stop | 0,228 | Hindi 0,646 — Hindi có đối lập 4 chiều (hữu/vô thanh × bật hơi/không) mà tiếng Anh không có |
| affricate | 0,183 | Tây Ban Nha 0,577 |

Không nhóm nào dưới 0,5 — dưới 0,5 mới nghĩa là GOP xếp hạng **ngược**.

### Bộ đo này tự chứng minh được là đáng tin

Hàng Quan Thoại cho **0,7484** so với **0,747** của speechocean762 — khác dataset, khác
người gán nhãn, khác hẳn cách suy nhãn (căn hai chuỗi phiên âm thay vì đọc điểm 0/1/2 có
sẵn). Trùng đến chữ số thứ ba là **tái lập độc lập**: nếu cách suy nhãn ở đây sai, không có
lý do gì nó rơi đúng vào con số cũ.

⚠️ **Đừng đọc bảng trên thành "engine chạy tốt nhất với người Việt".** Người Việt trong bộ
này mắc lỗi nhiều và nặng hơn hẳn (25,9%, nuốt âm 7,7%). Lỗi thô thì dễ tách hơn. Kết luận
đứng vững là kết luận yếu hơn nhưng đủ dùng: **không thứ tiếng nào bị sụt** — tức lever
"đổi model" ở §10.1 chưa cần dùng đến.

### Điểm yếu lộ ra

| | | |
|---|---|---|
| ~~**recall omission 0,513**~~ | **đã xử lý** — quy tắc 5 nâng lên 0,611 | đánh đổi: precision riêng nhãn omission 0,426 → 0,381. Xem §3.2 bước 5 |
| **nguyên âm: AUC 0,768, 33,5% `uncertain`** | 1/3 nguyên âm engine không dám kết luận | §6.4 đặt trần `uncertain` < 20% cho toàn bộ; riêng nguyên âm vẫn gấp rưỡi (quy tắc 5 đã kéo từ 36,7% xuống) |
| **âm tắc-xát: AUC 0,757, lỗi 42,4%** | nhóm yếu nhất, cũng là nhóm sai nhiều nhất | n=276, mẫu nhỏ — cần bổ sung câu chứa `tʃ dʒ` trước khi kết luận |
| **báo động sai nhiều hơn với người ÍT lỗi** | precision tụt xuống 0,317 khi tỷ lệ lỗi nền còn 11% | không phải chuyện tiếng mẹ đẻ mà là **trình độ** — cần ngưỡng thích ứng theo lịch sử từng người (v2) |

Bốn chỗ này là danh sách việc cụ thể cho §6.4, không phải nhận xét chung chung.

### Quy tắc 5 sửa được gì (đo trên đúng 600 câu này)

| | trước | sau |
|---|---|---|
| recall omission | 0,513 | **0,611** |
| precision omission | 0,426 | **0,381** ↓ |
| F1 omission | 0,465 | 0,469 |
| **precision lỗi tổng thể** | 0,630 | **0,637** |
| **recall lỗi tổng thể** | 0,496 | **0,577** |
| F1 lỗi tổng thể | 0,555 | **0,605** |
| `uncertain` | 17,8% | 14,7% |
| AUC | 0,8314 | 0,8314 *(không đổi)* |

AUC **phải** không đổi — nó tính từ GOP, mà quy tắc hợp nhất không được phép chạm vào nhánh
điểm số (nguyên tắc 3). Con số này đứng yên là một phép kiểm bất biến, không phải trùng hợp.

Đọc cho đúng: **precision riêng nhãn omission xấu đi.** Cái được nằm ở mức người học thật
sự trải nghiệm — "có lỗi hay không" tăng cả hai chiều, và ít câu trả lời "không rõ" hơn.

## Đọc kết quả

**Dùng AUC. Đừng dùng PCC.**

speechocean762 có điểm người chấm 0/1/2; ở đây nhãn chỉ nhị phân. Tương quan với biến nhị
phân bị suy giảm có hệ thống so với biến thứ tự 3 mức — hai con số không cùng thang. Đặt
PCC 0,393 cạnh PCC ở đây là so sai. AUC thì so được: cả hai đều hỏi "âm sai có tụt xuống
đáy bảng xếp hạng GOP không". Mốc speechocean762: **AUC 0,747**.

| So sánh | Ý nghĩa | Việc tiếp theo |
|---|---|---|
| AUC Việt ≈ AUC Trung | model chuyển được sang tiếng mẹ đẻ khác | đi tiếp bước 7 |
| AUC Việt ≪ AUC Trung | lỗi người Việt nằm ngoài thứ model phân biệt được | **đổi model** (§10.1) — không phải chỉnh bảng nhầm lẫn, §3.6.4 đã chứng minh lever đó vô hiệu |

Ba chỉ số cần đọc riêng, không gộp:

- **recall omission** — nuốt phụ âm cuối là lỗi số một của người Việt. Thấp ở đây nghiêm
  trọng hơn F1 tổng thấp.
- **precision** — báo sai khi người học nói đúng làm mất niềm tin nhanh nhất.
- **đoán đúng âm thay thế** — thứ Fix Guide dựa vào. Phát hiện được lỗi mà sai âm thì lời
  khuyên đưa cho người học sẽ sai chỗ.

## Cái này KHÔNG thay thế bước 7

| Giới hạn | Vì sao không sửa được bằng code |
|---|---|
| **4 người** | §6.1 đòi ≥20, phủ Bắc/Trung/Nam — phương ngữ chuyển di khác nhau |
| **Câu CMU ARCTIC** | không phải câu trong `assessment_questions`, phân bố âm khác |
| **Một người gán/câu** | không tính được đồng thuận → cổng chặn kappa ≥ 0,60 không áp dụng được |
| **Nhãn nhị phân** | không đo được engine phân biệt "sai hẳn" với "có accent" không |

Nói gọn: kết quả ở đây **loại trừ** được engine, nhưng **không chứng nhận** được nó. Đạt ở
đây thì vẫn phải làm bước 7 — chỉ là biết trước tiền công chấm tay không bị phí.

## Đầu ra

| File | |
|---|---|
| `results-l2arctic-vi.json` | AUC, precision/recall, phân rã nhóm âm, tỷ lệ nhập nhằng |
| `results-l2arctic-zh.json` | đối chứng người Trung, cùng thang đo |

Không có `--emit-calibration`, khác `run_speechocean.py`. Đó là cưỡng chế giấy phép NC
bằng cấu trúc: tham số fit trên dữ liệu NC là sản phẩm phái sinh của dữ liệu NC, ship kèm
sản phẩm thương mại là vi phạm. Ranh giới nằm ở chỗ **vắng mặt của cờ đó**, không phải ở
một dòng ghi chú mà người sau có thể bỏ qua.
