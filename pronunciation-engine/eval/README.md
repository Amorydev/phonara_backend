# Đánh giá trên speechocean762

Điều kiện 4 của §3.6 — tái lập kết quả trên bộ chuẩn công khai trước khi tin vào engine.

## Chạy

```bash
curl -sSL -o so762-test.parquet "https://huggingface.co/datasets/mispeech/speechocean762/resolve/main/data/test-00000-of-00001.parquet"
```

```bash
docker build -f eval/Dockerfile -t phonara-eval . && docker run --rm -v "$PWD":/w -v "$PWD/data":/data -w /w phonara-eval python eval/run_speechocean.py --parquet /data/so762-test.parquet --limit 0
```

`--limit 0` chạy toàn bộ 2500 utterance (~17 phút trên Apple Silicon).
`--emit-calibration <path>` ghi tham số đã fit ra file dùng được ngay.

## Quyết định thiết kế quan trọng

**Chuỗi chuẩn lấy từ dataset, KHÔNG chạy G2P trên `text`.**

speechocean762 gán nhãn ARPAbet (`AA0`, `IH1`, `NG`); engine dùng IPA espeak (`ɑː`, `ɪ`,
`ŋ`). `arpabet.py` map giữa hai bộ và xác thực với vocab model lúc nạp.

Vì sao không chạy G2P riêng: người chấm cho điểm trên **đúng chuỗi phone của dataset**.
Nếu ta sinh chuỗi riêng, nó có thể dài/ngắn khác đi và không còn tương ứng 1:1 với nhãn —
mọi con số tương quan sau đó sẽ vô nghĩa mà vẫn trông hợp lý.

Chữ số trọng âm mang thông tin thật ở hai chỗ, không strip vô tội vạ:

| ARPAbet | IPA | |
|---|---|---|
| `AH0` | `ə` | schwa |
| `AH1` `AH2` | `ʌ` | nguyên âm đủ |
| `ER0` | `ɚ` | |
| `ER1` `ER2` | `ɜː` | |

## Chỉ số

| | |
|---|---|
| PCC | tương quan `gop_raw` với điểm người chấm (0/1/2) |
| ROC-AUC | phát hiện lỗi nhị phân (`human < 2.0`) |
| Phân rã theo nhóm âm | bắt sai lệch cấu trúc do bảng nhầm lẫn (R9) |
| AUC tách omission | dùng trường `mispronunciations` với `pronounced-phone = <DEL>` |

Phân rã theo nhóm âm là bắt buộc chứ không phải để tham khảo: nếu GOP lệch có cấu trúc
giữa các lớp âm thì một logistic toàn cục không sửa được, và tiêu chí §6.4 sẽ trượt vì lý
do cấu trúc chứ không phải vì model tệ.

## Đọc kết quả — ba cảnh báo

**1. speechocean762 KHÔNG có người Việt.** 5.000 câu từ 250 người học tiếng mẹ đẻ là
**tiếng Quan Thoại**. Lỗi của người Việt khác: nuốt phụ âm cuối, giản lược cụm phụ âm,
`/l/–/n/`. Con số ở đây là **điểm khởi đầu**, không phải kết luận. Giai đoạn 4 mới là cổng
quyết định.

**2. Đừng so trực tiếp với PCC của GOPT.** GOPT là transformer có giám sát, đa khía cạnh,
đa mức độ, đã **học** ánh xạ GOP → điểm người. Ở đây ta đo chất lượng của **bản thân chỉ
số GOP** chưa qua học. Hai con số không cùng loại. Mốc so sánh hợp lệ là các baseline GOP
trong tài liệu alignment-free.

**3. Mất cân bằng lớp.** ~85% phoneme được chấm 2.0 (đúng). F1 và recall phải đọc cùng
precision; accuracy tổng là chỉ số vô dụng ở đây.

## Đầu ra

| File | |
|---|---|
| `results-full.json` | PCC, AUC, phân rã nhóm âm, tham số fit |
| `calibration_so762.json` | tham số calibration dùng được ngay (`--emit-calibration`) |

Muốn dùng bản fit thay cho PLACEHOLDER:

```bash
PE_CALIBRATION_PATH=/srv/eval/calibration_so762.json
```

Nhưng **đừng đưa lên production** — nó fit trên người Trung Quốc. Xem cảnh báo 1.
