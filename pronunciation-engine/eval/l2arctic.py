"""Đo engine trên giọng người Việt bằng L2-ARCTIC — TIỀN SÀNG LỌC cho bước 7.

    python eval/l2arctic.py --parquet /data/scripted-00000-of-00001.parquet

600 câu của 4 người Việt (HQTV, PNV, THV, TLV), mỗi câu có sẵn cặp phiên âm
**chuẩn ↔ nghe-thấy** do người gán nhãn. Tức là ~20.000 âm vị đã có nhãn, gấp 5 lần lượng
mà `BENCHMARK_PROTOCOL.md` yêu cầu người chấm tay tạo ra — với chi phí bằng không.

═══════════════════════════════════════════════════════════════════════════════
CÁI NÀY KHÔNG THAY THẾ ĐƯỢC BƯỚC 7
═══════════════════════════════════════════════════════════════════════════════

Bốn giới hạn, không cái nào sửa được bằng code:

  1. **4 người.** Không phủ vùng miền (Bắc/Trung/Nam) hay trình độ. §6.1 đòi ≥20 người vì
     phương ngữ tiếng Việt chuyển di sang tiếng Anh khác nhau.
  2. **Câu của CMU ARCTIC**, không phải câu trong `assessment_questions`. Phân bố âm khác.
  3. **Một người gán nhãn mỗi câu** → KHÔNG tính được đồng thuận. Cổng chặn kappa ≥ 0,60
     của `human_benchmark.py` không áp dụng được ở đây, nên nhãn ở đây kém tin cậy hơn về
     mặt phương pháp dù nhiều hơn về số lượng.
  4. **Nhãn nhị phân.** Người gán chỉ ghi "nghe thấy gì", không cho điểm mức độ. Không có
     thang 0/1/2, nên không đo được engine có phân biệt "sai hẳn" với "có accent" không.

Vì vậy: kết quả ở đây **loại trừ** được engine (nếu tệ thì khỏi tốn ngày công chấm tay),
nhưng **không chứng nhận** được nó. Đạt ở đây → vẫn phải làm bước 7.

═══════════════════════════════════════════════════════════════════════════════
GIẤY PHÉP — CC BY-NC 4.0
═══════════════════════════════════════════════════════════════════════════════

Script này **cố ý không có** `--emit-calibration`, khác `run_speechocean.py`.

Tham số calibration fit trên dữ liệu NC là sản phẩm phái sinh của dữ liệu NC. File đó ship
kèm sản phẩm thương mại → vi phạm. Đo thì được, học tham số thì không. Ranh giới đó nằm ở
chỗ vắng mặt của cờ này, không phải ở một dòng ghi chú mà người sau có thể bỏ qua.

═══════════════════════════════════════════════════════════════════════════════
SO SÁNH VỚI speechocean762: DÙNG AUC, ĐỪNG DÙNG PCC
═══════════════════════════════════════════════════════════════════════════════

speechocean762 có điểm người chấm 0/1/2; ở đây nhãn chỉ có đúng/sai. Tương quan với biến
nhị phân (point-biserial) bị **suy giảm có hệ thống** so với tương quan với biến thứ tự 3
mức — hai con số không cùng thang. Đặt PCC 0,393 cạnh PCC ở đây là so sai.

ROC-AUC thì so được: cả hai đều là "xếp hạng âm vị theo GOP, hỏi âm sai có tụt xuống đáy
không". Mốc speechocean762: **AUC 0,747**.

`--l1 Chinese` chạy đúng bộ đo này trên 600 câu người Trung trong cùng dataset. Đó là đối
chứng đắt giá nhất: nếu AUC Việt ≈ AUC Trung thì model chuyển được sang tiếng mẹ đẻ khác;
nếu tụt mạnh thì lever là ĐỔI MODEL (§10.1), không phải chỉnh bảng nhầm lẫn — thí nghiệm
§3.6.4 đã chứng minh lever đó vô hiệu.
"""

from __future__ import annotations

import argparse
import io
import json
import logging
import sys
import time
import wave
from collections import Counter
from pathlib import Path

import numpy as np
import pyarrow.parquet as pq
import torch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.engine import gop as gop_mod  # noqa: E402
from app.engine import loader  # noqa: E402
from app.engine.alignment import Op, align  # noqa: E402
from app.engine.diagnosis import diagnose, greedy_decode  # noqa: E402
from app.schemas import PhonemeDiagnosis  # noqa: E402

log = logging.getLogger("eval")

# ── Bộ ký hiệu của L2-ARCTIC ───────────────────────────────────────────────────
#
# Hai cột `g2p` và `ipa` là chuỗi IPA LIỀN, không dấu phân cách. Muốn so từng âm vị thì
# phải tách, mà tách theo ký tự là sai: `tʃ`, `dʒ`, `aɪ`, `eɪ`, `oʊ`, `aʊ`, `ɔɪ` là MỘT âm
# vị viết bằng hai ký tự.
#
# Bảng dưới đây định nghĩa tập ký hiệu bằng cách dẫn xuất từ 39 phone ARPAbet — tức tập
# đóng mà người gán nhãn được phép chọn. Nhờ vậy "tập đóng" là tính chất cấu tạo, không
# phải giả định: ký tự lạ sẽ rơi vào `unsegmentable` và script kêu lên.
ARPABET_TO_L2ARCTIC: dict[str, str] = {
    "AA": "ɑ", "AE": "æ", "AH": "ʌ", "AO": "ɔ", "AW": "aʊ", "AY": "aɪ",
    "B": "b", "CH": "tʃ", "D": "d", "DH": "ð", "EH": "ɛ", "ER": "ɝ",
    "EY": "eɪ", "F": "f", "G": "ɡ", "HH": "h", "IH": "ɪ", "IY": "i",
    "JH": "dʒ", "K": "k", "L": "l", "M": "m", "N": "n", "NG": "ŋ",
    "OW": "oʊ", "OY": "ɔɪ", "P": "p", "R": "ɹ", "S": "s", "SH": "ʃ",
    "T": "t", "TH": "θ", "UH": "ʊ", "UW": "u", "V": "v", "W": "w",
    "Y": "j", "Z": "z", "ZH": "ʒ",
}

# Khớp dài nhất: ký hiệu 2 ký tự phải thử trước ký hiệu 1 ký tự.
INVENTORY: list[str] = sorted(set(ARPABET_TO_L2ARCTIC.values()), key=len, reverse=True)

# Ba chỗ khớp dài nhất có thể sai thật, vì cả hai cách tách đều hợp lệ:
#   `tʃ` vs `t`+`ʃ`   ("watch it" vs "hot shot")
#   `dʒ` vs `d`+`ʒ`
#   `ɔɪ` vs `ɔ`+`ɪ`
# Dataset đã vứt bỏ ranh giới âm vị nên không khôi phục được. Đây là sàn sai số của phép
# đo — script ĐẾM và BÁO nó thay vì giả vờ phép tách là chính xác.
AMBIGUOUS: frozenset[str] = frozenset({"tʃ", "dʒ", "ɔɪ"})

# Người gán đôi khi viết `ə` ở cột `ipa`, dù ARPAbet không có ký hiệu riêng cho schwa —
# `AH` gộp cả `ʌ` lẫn `ə`. Xuất hiện ở 5/6 nhóm L1 (người Việt: 0 lần).
#
# GỘP về `ʌ`, không nhận làm ký hiệu riêng. Cột chuẩn `g2p` không có cách nào biểu diễn
# schwa, nên coi `ə` khác `ʌ` là chấm điểm một phân biệt mà bản tham chiếu không diễn đạt
# được — mọi nguyên âm giảm sẽ hoá thành lỗi giả. Cả hai vế phải ở cùng một mức chi tiết.
#
# Chỉ áp dụng cho cột `ipa`. Ký hiệu lạ ở cột `g2p` vẫn phải kêu lên: cột đó do từ điển
# sinh ra, có ký hiệu ngoài bộ nghĩa là dataset đã đổi.
PERCEIVED_FOLD: dict[str, str] = {"ə": "ʌ"}

INVENTORY_PERCEIVED: list[str] = sorted(
    set(INVENTORY) | set(PERCEIVED_FOLD), key=len, reverse=True
)

# ── L2-ARCTIC IPA → vocab espeak của model ─────────────────────────────────────
#
# Hai bộ ký hiệu khác quy ước. L2-ARCTIC theo lối ARPAbet (không dấu trường, `ɝ` cho ER);
# vocab model theo espeak (`ɑː`, `iː`, `ɜː`). Chỉ 5 ký hiệu cần đổi, còn lại trùng.
TO_ESPEAK: dict[str, str] = {
    "ɝ": "ɜː",  # ER — vocab không có `ɝ`; `ɜː` là dạng có trọng âm (xem arpabet.py)
    "ɑ": "ɑː",  # AA
    "ɔ": "ɔː",  # AO
    "i": "iː",  # IY
    "u": "uː",  # UW
}

# Dấu trọng âm chỉ có ở cột `ipa`, không có ở `g2p` — và vocab model KHÔNG chứa chúng
# (đây chính là cạm bẫy mà `g2p.py` sinh ra để chặn). Bỏ trước khi căn, nếu không mọi âm
# tiết có trọng âm sẽ bị tính thành insertion.
STRESS: frozenset[str] = frozenset({"ˈ", "ˌ"})

# ── Tinh chỉnh nguyên âm giảm ──────────────────────────────────────────────────
#
# ARPAbet phân biệt nguyên âm đủ với nguyên âm giảm bằng CHỮ SỐ TRỌNG ÂM (`AH0`=ə,
# `AH1`=ʌ) — và cột `g2p` của L2-ARCTIC đã vứt chữ số đó đi. Hệ quả: mọi schwa trong
# dataset đều đội lốt `ʌ`.
#
# Không sửa thì GOP bị phạt oan ở mọi âm tiết không trọng âm, tức phần lớn nguyên âm
# tiếng Anh — đúng nhóm âm mà số liệu cần đọc chính xác nhất.
#
# Khôi phục bằng cách hỏi espeak — chính G2P mà model được huấn luyện cùng và production
# dùng thật — xem từ đó có nguyên âm giảm ở đâu. Hợp lệ vì cả hai đều là phiên âm CHUẨN
# của CÙNG một câu chữ: ta chỉ chọn lại KÝ HIỆU, không đụng vào nhãn người gán và không
# đổi số lượng hay thứ tự âm vị. Chỉ nhận thay thế trong cùng họ nguyên âm; mọi bất đồng
# khác giữ nguyên ký hiệu của dataset.
REDUCED_VARIANTS: dict[str, frozenset[str]] = {
    "ʌ": frozenset({"ə", "ɐ", "ᵻ"}),  # AH0
    "ɜː": frozenset({"ɚ"}),            # ER0
    "iː": frozenset({"i"}),            # IY không trọng âm ("happy")
    "uː": frozenset({"u"}),            # UW không trọng âm
}


def to_espeak(symbol: str) -> str:
    return TO_ESPEAK.get(symbol, symbol)


# Chiều ngược, gộp cả nguyên âm giảm về lớp ARPAbet mà chúng thuộc về. Dựng bằng code
# thay vì gõ tay để không thể lệch với hai bảng trên.
FROM_ESPEAK: dict[str, str] = {v: k for k, v in TO_ESPEAK.items()}
for _base, _variants in REDUCED_VARIANTS.items():
    for _v in _variants:
        FROM_ESPEAK[_v] = FROM_ESPEAK.get(_base, _base)


def to_dataset(symbol: str) -> str:
    """espeak → bộ ký hiệu của dataset.

    Cần khi đối chiếu `said`: engine trả ký hiệu espeak, người gán viết ký hiệu ARPAbet.
    Gộp về bộ thô hơn là mức chi tiết TRUNG THỰC — người gán không có sẵn phân biệt
    `ə`/`ʌ` để chọn, nên bắt engine trúng phân biệt đó là chấm điểm một thứ không tồn tại
    trong nhãn.
    """
    return FROM_ESPEAK.get(symbol, symbol)


def segment(raw: str, *, perceived: bool = False) -> tuple[list[str], int, list[str]]:
    """Chuỗi IPA liền → danh sách âm vị. → (tokens, số quyết định nhập nhằng, ký tự lạ).

    `perceived=True` cho cột `ipa`: chấp nhận thêm ký hiệu của người gán và gộp về lớp
    ARPAbet tương ứng (xem PERCEIVED_FOLD).
    """
    inventory = INVENTORY_PERCEIVED if perceived else INVENTORY
    text = "".join(c for c in raw if c not in STRESS and not c.isspace())
    tokens: list[str] = []
    unknown: list[str] = []
    ambiguous = 0
    i = 0
    while i < len(text):
        for symbol in inventory:
            if text.startswith(symbol, i):
                if symbol in AMBIGUOUS:
                    ambiguous += 1
                tokens.append(PERCEIVED_FOLD.get(symbol, symbol) if perceived else symbol)
                i += len(symbol)
                break
        else:
            unknown.append(text[i])
            i += 1
    return tokens, ambiguous, unknown


def read_wav(data: bytes) -> tuple[np.ndarray, int]:
    with wave.open(io.BytesIO(data), "rb") as w:
        rate = w.getframerate()
        raw = w.readframes(w.getnframes())
    return np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0, rate


def pearson(x: np.ndarray, y: np.ndarray) -> float:
    if len(x) < 2 or x.std() == 0 or y.std() == 0:
        return float("nan")
    return float(np.corrcoef(x, y)[0, 1])


def roc_auc(scores: np.ndarray, labels: np.ndarray) -> float:
    """AUC bằng thống kê hạng (Mann-Whitney U), có xử lý hạng đồng giá trị."""
    pos, neg = labels == 1, labels == 0
    n_pos, n_neg = int(pos.sum()), int(neg.sum())
    if n_pos == 0 or n_neg == 0:
        return float("nan")
    order = scores.argsort()
    ranks = np.empty(len(scores), dtype=np.float64)
    ranks[order] = np.arange(1, len(scores) + 1)
    _, inv, counts = np.unique(scores, return_inverse=True, return_counts=True)
    mean_rank = np.zeros(len(counts))
    np.add.at(mean_rank, inv, ranks)
    mean_rank /= counts
    return float((mean_rank[inv][pos].sum() - n_pos * (n_pos + 1) / 2) / (n_pos * n_neg))


def prf(tp: int, fp: int, fn: int) -> tuple[float, float, float]:
    precision = tp / (tp + fp) if tp + fp else 0.0
    recall = tp / (tp + fn) if tp + fn else 0.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    return precision, recall, f1


def ground_truth(canonical: list[str], perceived: list[str]) -> list[tuple[Op, str | None]]:
    """Nhãn cho từng âm vị chuẩn, suy từ cặp phiên âm của người gán.

    Dùng `align()` với chi phí thay thế MẶC ĐỊNH, cố ý không nạp bảng nhầm lẫn của engine.
    Nạp vào sẽ khiến nhãn "thật" nghiêng theo đúng giả định mà ta đang muốn kiểm — engine
    sẽ trông đúng hơn thực tế ở chính những cặp âm mà bảng liệt kê.
    """
    out: list[tuple[Op, str | None]] = [(Op.MATCH, None)] * len(canonical)
    for pair in align(canonical, perceived):
        if pair.canon_index is None:
            continue  # insertion: không có âm chuẩn tương ứng để gán nhãn
        said = perceived[pair.decoded_index] if pair.decoded_index is not None else None
        out[pair.canon_index] = (pair.op, said)
    return out


def refine_canonical(canonical: list[str], espeak: list[str]) -> tuple[list[str], int]:
    """Thay ký hiệu nguyên âm đủ bằng nguyên âm giảm ở nơi espeak nói là giảm.

    → (chuỗi đã tinh chỉnh, số ký hiệu đã đổi). Độ dài và thứ tự KHÔNG đổi, nên chỉ số
    của nhãn người gán vẫn khớp nguyên vẹn.
    """
    out = list(canonical)
    changed = 0
    for pair in align(canonical, espeak):
        if pair.op is not Op.SUBSTITUTION:
            continue
        i, j = pair.canon_index, pair.decoded_index
        if i is None or j is None:
            continue
        if espeak[j] in REDUCED_VARIANTS.get(canonical[i], frozenset()):
            out[i] = espeak[j]
            changed += 1
    return out, changed


def check_symbol_map(eng, rows: list[dict], sample: int) -> None:
    """Đối chiếu tần suất ký hiệu: dataset (đã map) vs G2P của chính engine trên cùng câu.

    Đây là bảo hiểm cho `TO_ESPEAK`. Map sai — ví dụ IY→`i` trong khi espeak phát `iː` —
    không gây lỗi ở đâu cả: nó chỉ làm GOP tụt ở mọi chỗ có âm đó, và ta sẽ kết luận nhầm
    là model kém. Lệch lớn ở bảng dưới nghĩa là phải sửa map trước khi tin số liệu.
    """
    from_dataset: Counter[str] = Counter()
    from_engine: Counter[str] = Counter()
    for row in rows[:sample]:
        tokens, _, _ = segment(row["g2p"])
        from_dataset.update(to_espeak(t) for t in tokens)
        try:
            from_engine.update(eng.g2p(row["text"]).phonemes)
        except Exception:  # noqa: BLE001 — chẩn đoán, không được làm hỏng phép đo
            log.warning("g2p lỗi trên %r", row["text"], exc_info=True)

    log.info("\n%s", "=" * 74)
    log.info("KIỂM MAP KÝ HIỆU — %d câu đầu (chẩn đoán, không phải cổng chặn)", sample)
    log.info("%s", "=" * 74)
    log.info("%-10s %10s %10s %10s", "ký hiệu", "dataset", "espeak", "lệch")
    log.info("%s", "-" * 74)
    keys = set(from_dataset) | set(from_engine)
    ranked = sorted(keys, key=lambda k: -abs(from_dataset[k] - from_engine[k]))
    for symbol in ranked[:12]:
        d, e = from_dataset[symbol], from_engine[symbol]
        log.info("%-10s %10d %10d %+10d", symbol, d, e, d - e)
    log.info(
        "Lệch lớn ở ký hiệu TẦN SUẤT CAO = nghi map sai. Lệch ở `ə`/`ɐ`/`ᵻ`/`əl` là bình "
        "thường: espeak có nguyên âm giảm mà ARPAbet gộp vào AH."
    )


def main() -> int:  # noqa: PLR0915 — báo cáo tuần tự, tách hàm chỉ làm khó đọc
    ap = argparse.ArgumentParser()
    ap.add_argument("--parquet", required=True, type=Path)
    ap.add_argument("--l1", default="Vietnamese", help="lọc theo tiếng mẹ đẻ; 'all' = không lọc")
    ap.add_argument("--limit", type=int, default=0, help="0 = toàn bộ")
    ap.add_argument("--out", type=Path, default=Path("eval/results-l2arctic-vi.json"))
    ap.add_argument("--map-check-sample", type=int, default=40)
    ap.add_argument(
        "--refine",
        choices=["espeak", "off"],
        default="espeak",
        help="khôi phục nguyên âm giảm mà cột g2p đã đánh mất (xem REDUCED_VARIANTS). "
        "'off' để đo xem việc này ảnh hưởng bao nhiêu.",
    )
    args = ap.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(message)s")
    eng = loader.load()
    vocab = eng.tokenizer.get_vocab()

    # Fail fast: mọi ký hiệu sau khi map phải nằm trong vocab. Ký hiệu lạ không gây
    # exception lúc chấm — nó chỉ cho ra likelihood vô nghĩa (cùng lý do với confusion.py).
    oov = sorted({to_espeak(s) for s in INVENTORY} - set(vocab))
    if oov:
        raise SystemExit(f"ký hiệu ngoài vocab sau khi map: {oov} — sửa TO_ESPEAK")

    table = pq.read_table(
        args.parquet,
        columns=["audio", "text", "ipa", "g2p", "speaker_code", "speaker_native_language"],
    )
    rows = table.to_pylist()
    if args.l1 != "all":
        rows = [r for r in rows if r["speaker_native_language"] == args.l1]
    if not rows:
        langs = sorted({r["speaker_native_language"] for r in table.to_pylist()})
        raise SystemExit(f"không có câu nào cho --l1 {args.l1!r}. Có: {langs}")
    if args.limit:
        rows = rows[: args.limit]

    speakers = Counter(r["speaker_code"] for r in rows)
    log.info("L1=%s | %d câu | %d người: %s", args.l1, len(rows), len(speakers), dict(speakers))

    check_symbol_map(eng, rows, min(args.map_check_sample, len(rows)))

    # ── chạy engine ─────────────────────────────────────────────────────────────
    gops: list[float] = []
    truth_error: list[int] = []       # người gán: âm này KHÔNG khớp chuẩn
    truth_omission: list[int] = []
    engine_error: list[int] = []      # engine: substitution hoặc omission
    engine_omission: list[int] = []
    engine_uncertain: list[int] = []
    groups: list[str] = []
    said_hit: list[int] = []          # engine đoán ĐÚNG âm được thay bằng gì
    said_total = 0

    amb_total = tokens_total = refined_total = 0
    unknown_chars: Counter[str] = Counter()
    skipped = 0
    t0 = time.perf_counter()

    for n, row in enumerate(rows, 1):
        canon_raw, amb_c, unk_c = segment(row["g2p"])
        perc_raw, amb_p, unk_p = segment(row["ipa"], perceived=True)
        amb_total += amb_c + amb_p
        tokens_total += len(canon_raw) + len(perc_raw)
        unknown_chars.update(unk_c + unk_p)

        if not canon_raw or not perc_raw:
            skipped += 1
            continue

        # NHÃN SUY TRƯỚC, MAP SAU — và nhãn suy trong bộ ký hiệu GỐC của dataset.
        #
        # Cả `g2p` lẫn `ipa` đều do người gán viết bằng bộ ARPAbet; so chúng với nhau là
        # so cùng hệ. Nếu map sang espeak (hoặc tinh chỉnh nguyên âm giảm) TRƯỚC khi căn
        # thì chỉ một vế đổi ký hiệu, và mọi âm vốn khớp sẽ hoá thành substitution giả.
        # Đo thực tế: làm sai thứ tự này thổi tỷ lệ lỗi nguyên âm từ 26,7% lên 40,6%.
        labels = ground_truth(canon_raw, perc_raw)

        canonical = [to_espeak(s) for s in canon_raw]

        if args.refine == "espeak":
            try:
                canonical, changed = refine_canonical(
                    canonical, eng.g2p(row["text"]).phonemes
                )
                refined_total += changed
            except Exception:  # noqa: BLE001 — G2P hỏng thì giữ ký hiệu dataset, vẫn đo được
                log.warning("g2p lỗi trên %r — bỏ tinh chỉnh câu này", row["text"])

        waveform, rate = read_wav(row["audio"]["bytes"])
        if rate != 16_000 or len(waveform) < 4800:
            skipped += 1
            continue

        with torch.no_grad():
            logits = eng.model(torch.from_numpy(waveform).unsqueeze(0)).logits[0]
            log_probs = torch.log_softmax(logits, dim=-1)

        canonical_ids = [vocab[p] for p in canonical]
        gop_raw = gop_mod.compute(
            log_probs,
            canonical_ids,
            eng.confusion_ids(canonical),
            blank_id=eng.blank_id,
        )

        runs = greedy_decode(log_probs, eng.id_to_symbol, eng.blank_id)

        def sub_cost(expected: str, said: str) -> float:
            return 0.6 if said in eng.confusion.confuse(expected) else 1.0

        pairs = align(canonical, [r.symbol for r in runs], sub_cost=sub_cost)
        verdicts = diagnose(
            pairs,
            canonical,
            canonical_ids,
            runs,
            gop_raw,
            log_probs,
            eng.blank_id,
            eng.merge_rules,
        )

        for i, expected in enumerate(canonical):
            op, human_said = labels[i]
            diagnosis, engine_said, _conf = verdicts[i]

            gops.append(gop_raw[i])
            truth_error.append(int(op is not Op.MATCH))
            truth_omission.append(int(op is Op.OMISSION))
            engine_error.append(
                int(diagnosis in (PhonemeDiagnosis.SUBSTITUTION, PhonemeDiagnosis.OMISSION))
            )
            engine_omission.append(int(diagnosis is PhonemeDiagnosis.OMISSION))
            engine_uncertain.append(int(diagnosis is PhonemeDiagnosis.UNCERTAIN))
            groups.append(eng.confusion.group(expected))

            # Chỉ tính khi CẢ HAI đều nói "thay thế": hỏi engine có nhận ra người học nói
            # thành âm NÀO, không phải có phát hiện được lỗi không (đã đo ở trên).
            if op is Op.SUBSTITUTION and diagnosis is PhonemeDiagnosis.SUBSTITUTION:
                said_total += 1
                said_hit.append(int(to_dataset(engine_said or "") == human_said))

        if n % 25 == 0:
            log.info(
                "  %d/%d  (%.2f câu/s, %d âm vị)",
                n, len(rows), n / (time.perf_counter() - t0), len(gops),
            )

    g = np.array(gops)
    terr = np.array(truth_error)
    tomit = np.array(truth_omission)
    eerr = np.array(engine_error)
    eomit = np.array(engine_omission)
    eunc = np.array(engine_uncertain)
    grp = np.array(groups)

    # ── chất lượng phép tách chuỗi ──────────────────────────────────────────────
    log.info("\n%s", "=" * 74)
    log.info("CHẤT LƯỢNG PHÉP TÁCH CHUỖI IPA")
    log.info("%s", "=" * 74)
    log.info("ký tự không tách được : %s", dict(unknown_chars) or "không có ✓")
    log.info(
        "quyết định nhập nhằng : %d / %d token = %.2f%%  (tʃ|t+ʃ, dʒ|d+ʒ, ɔɪ|ɔ+ɪ)",
        amb_total, tokens_total, 100 * amb_total / max(tokens_total, 1),
    )
    log.info(
        "nguyên âm giảm khôi phục: %d (--refine %s) — chạy `--refine off` để đo ảnh hưởng",
        refined_total, args.refine,
    )
    if unknown_chars:
        log.warning(
            "→ có ký tự ngoài bộ 39 phone ARPAbet. Bộ ký hiệu của dataset đã đổi, "
            "hoặc file parquet không phải bản mong đợi. ĐỪNG tin số liệu bên dưới."
        )

    # ── kết quả ─────────────────────────────────────────────────────────────────
    log.info("\n%s", "=" * 74)
    log.info("KẾT QUẢ — %d âm vị / %d câu (bỏ qua %d)", len(g), len(rows) - skipped, skipped)
    log.info("%s", "=" * 74)

    auc = roc_auc(-g, terr)
    log.info("ROC-AUC phát hiện lỗi        : %.4f   ← SO ĐƯỢC với speechocean762 (0,747)", auc)
    log.info("PCC point-biserial(gop, lỗi) : %+.4f   ← KHÔNG so được với PCC 0,393, xem docstring",
             pearson(g, -terr.astype(float)))
    log.info("tỷ lệ âm vị người gán là sai : %.1f%%", 100 * terr.mean())
    log.info("  trong đó bị nuốt hẳn       : %.1f%%", 100 * tomit.mean())
    log.info("tỷ lệ engine trả 'uncertain' : %.1f%%", 100 * eunc.mean())
    log.info("GOP: mean %+.2f  sd %.2f", g.mean(), g.std())

    p, r, f1 = prf(
        int(((eerr == 1) & (terr == 1)).sum()),
        int(((eerr == 1) & (terr == 0)).sum()),
        int(((eerr == 0) & (terr == 1)).sum()),
    )
    log.info("\nchẩn đoán lỗi: precision=%.3f  recall=%.3f  F1=%.3f", p, r, f1)
    log.info("  precision thấp = báo sai khi người học nói ĐÚNG → mất niềm tin nhanh nhất")
    log.info("  recall thấp    = bỏ sót lỗi → người học không biết mình sai")

    po, ro, f1o = prf(
        int(((eomit == 1) & (tomit == 1)).sum()),
        int(((eomit == 1) & (tomit == 0)).sum()),
        int(((eomit == 0) & (tomit == 1)).sum()),
    )
    log.info("\nriêng omission: precision=%.3f  recall=%.3f  F1=%.3f  (AUC từ GOP %.4f)",
             po, ro, f1o, roc_auc(-g, tomit))
    log.info("  Nuốt phụ âm cuối là lỗi số một của người Việt — recall thấp ở đây là lỗ hổng")
    log.info("  nghiêm trọng hơn F1 tổng thấp.")

    said_acc = float(np.mean(said_hit)) if said_hit else float("nan")
    log.info("\nđoán đúng âm thay thế: %.1f%% trên %d ca cả hai đều báo substitution",
             100 * said_acc, said_total)
    log.info("  Đây là thứ Fix Guide dựa vào. Phát hiện được lỗi mà chỉ sai âm thì lời")
    log.info("  khuyên đưa cho người học sẽ sai chỗ.")

    log.info("\n%-14s %7s %8s %10s %10s", "nhóm âm", "n", "AUC", "tỷ lệ lỗi", "uncertain")
    log.info("%s", "-" * 74)
    per_group: dict[str, dict] = {}
    for name in sorted(set(groups)):
        m = grp == name
        if m.sum() < 20:
            continue
        auc_g = roc_auc(-g[m], terr[m])
        per_group[name] = {
            "n": int(m.sum()),
            "auc": auc_g,
            "error_rate": float(terr[m].mean()),
        }
        log.info("%-14s %7d %8.4f %9.1f%% %9.1f%%",
                 name, m.sum(), auc_g, 100 * terr[m].mean(), 100 * eunc[m].mean())
    log.info("Nhóm có AUC ≤ 0,5 = GOP xếp hạng NGƯỢC ở lớp âm đó. Một logistic toàn cục")
    log.info("không sửa được sai lệch cấu trúc — xem R9.")

    # ── kết luận ────────────────────────────────────────────────────────────────
    log.info("\n%s", "=" * 74)
    log.info("ĐỌC KẾT QUẢ")
    log.info("%s", "=" * 74)
    log.info("Chạy `--l1 Chinese` trên cùng script để có đối chứng cùng thang đo.")
    log.info("  AUC Việt ≈ AUC Trung  → model chuyển được sang tiếng mẹ đẻ khác; đi tiếp bước 7")
    log.info("  AUC Việt << AUC Trung → lever là ĐỔI MODEL (§10.1), không phải chỉnh bảng")
    log.info("                          nhầm lẫn (§3.6.4 đã chứng minh lever đó vô hiệu)")
    log.info("\nDù kết quả tốt: đây là 4 người, không phải 20; nhãn một người gán, không đo")
    log.info("được đồng thuận. KHÔNG thay thế bước 7. Xem BENCHMARK_PROTOCOL.md.")
    log.info("\nL2-ARCTIC là CC BY-NC 4.0: đo được, KHÔNG fit tham số ship kèm sản phẩm.")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(
        json.dumps(
            {
                "dataset": "l2arctic",
                "license": "CC BY-NC 4.0 — measurement only, no fitted parameters",
                "l1": args.l1,
                "speakers": dict(speakers),
                "n_utterances": len(rows) - skipped,
                "n_phonemes": int(len(g)),
                "segmentation_ambiguity_rate": amb_total / max(tokens_total, 1),
                "unsegmentable_chars": dict(unknown_chars),
                "refine": args.refine,
                "refined_symbols": refined_total,
                "error_detection_auc": auc,
                "truth_error_rate": float(terr.mean()),
                "diagnosis": {"precision": p, "recall": r, "f1": f1},
                "omission": {"precision": po, "recall": ro, "f1": f1o},
                "said_accuracy": said_acc,
                "said_n": said_total,
                "uncertain_rate": float(eunc.mean()),
                "per_group": per_group,
            },
            ensure_ascii=False,
            indent=2,
        ),
        encoding="utf-8",
    )
    log.info("\nđã ghi %s", args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
