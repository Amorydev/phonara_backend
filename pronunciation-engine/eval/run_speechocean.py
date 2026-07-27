"""Đánh giá engine trên speechocean762 — điều kiện 4 của §3.6.

Hai mục tiêu tách bạch:
  1. ĐO chất lượng GOP: tương quan với điểm người chấm ở mức phoneme
  2. FIT tham số calibration khởi điểm cho `calibration_vi.json`

Chuỗi chuẩn lấy TỪ DATASET (ARPAbet → IPA), không chạy G2P trên `text`. Người chấm cho
điểm trên đúng chuỗi phone đó; chạy G2P riêng sẽ sinh chuỗi dài/ngắn khác và phá tương
ứng 1:1 với nhãn.

CẢNH BÁO khi đọc kết quả: speechocean762 là người bản ngữ **tiếng Quan Thoại**, không có
người Việt. Con số ở đây là điểm khởi đầu, KHÔNG phải kết luận cho sản phẩm. Xem giai
đoạn 4 của plan.

    docker run --rm -v "$PWD":/w -v /path/to/data:/data -w /w phonara-eval \
        python eval/run_speechocean.py --parquet /data/so762-test.parquet --limit 200
"""

from __future__ import annotations

import argparse
import io
import json
import logging
import sys
import time
import wave
from collections import defaultdict
from pathlib import Path

import numpy as np
import pyarrow.parquet as pq
import torch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.engine import gop as gop_mod  # noqa: E402
from app.engine import loader  # noqa: E402
from eval.arpabet import DELETION, to_ipa, validate  # noqa: E402

log = logging.getLogger("eval")

# Người chấm cho 0/1/2 (0=sai, 1=có accent, 2=đúng), nội suy thành số thực.
# Ngưỡng phân loại nhị phân "có lỗi": mọi thứ dưới 2.0 đều không phải phát âm chuẩn.
ERROR_THRESHOLD = 2.0


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
    """AUC bằng thống kê hạng (Mann-Whitney U) — không cần sklearn."""
    pos, neg = labels == 1, labels == 0
    n_pos, n_neg = int(pos.sum()), int(neg.sum())
    if n_pos == 0 or n_neg == 0:
        return float("nan")
    order = scores.argsort()
    ranks = np.empty(len(scores), dtype=np.float64)
    ranks[order] = np.arange(1, len(scores) + 1)
    # xử lý hạng đồng giá trị
    _, inv, counts = np.unique(scores, return_inverse=True, return_counts=True)
    mean_rank = np.zeros(len(counts))
    np.add.at(mean_rank, inv, ranks)
    mean_rank /= counts
    ranks = mean_rank[inv]
    return float((ranks[pos].sum() - n_pos * (n_pos + 1) / 2) / (n_pos * n_neg))


def fit_logistic(gop: np.ndarray, target: np.ndarray) -> tuple[float, float]:
    """Khớp `sigmoid(a·gop + b)` với `target` ∈ [0,1] bằng bình phương tối thiểu.

    Dùng gradient descent thuần numpy thay vì scipy — ít phụ thuộc, và bài toán 2 tham số
    này hội tụ dễ.
    """
    a, b = 0.5, 0.0
    lr, n = 0.05, len(gop)
    if n == 0:
        return 1.0, 0.0
    # chuẩn hoá thang GOP để learning rate không phụ thuộc dữ liệu
    scale = float(np.abs(gop).mean()) or 1.0
    g = gop / scale
    for _ in range(4000):
        z = a * g + b
        p = 1.0 / (1.0 + np.exp(-np.clip(z, -60, 60)))
        err = p - target
        d = err * p * (1 - p)
        a -= lr * 2 * float((d * g).mean())
        b -= lr * 2 * float(d.mean())
    return a / scale, b


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--parquet", required=True, type=Path)
    ap.add_argument("--limit", type=int, default=200, help="0 = toàn bộ")
    ap.add_argument("--out", type=Path, default=Path("eval/results.json"))
    ap.add_argument(
        "--emit-calibration",
        type=Path,
        default=None,
        help="ghi tham số calibration đã fit ra file",
    )
    ap.add_argument(
        "--candidates",
        choices=["table", "all"],
        default="table",
        help="table = bảng nhầm lẫn (~4 ứng viên, mặc định production); "
        "all = toàn bộ inventory tiếng Anh (~45). Tài liệu cho thấy không giới hạn tập "
        "thay thế cho recall cao hơn, đổi lại chi phí tính toán — mà đo thực tế cho thấy "
        "GOP chỉ chiếm 3/780 ms, nên chi phí đó gần như không tồn tại.",
    )
    args = ap.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(message)s")
    eng = loader.load()
    vocab = eng.tokenizer.get_vocab()
    validate(vocab)

    # Tập ứng viên nhiễu: bảng nhầm lẫn (production) hoặc toàn bộ inventory tiếng Anh.
    # Inventory lấy từ chính khoá của tier_general — tức tập phoneme mà bảng đã phủ.
    english_inventory = sorted(eng.confusion.candidates)
    if args.candidates == "all":
        log.info("dùng TOÀN BỘ %d phoneme làm ứng viên nhiễu", len(english_inventory))

    def candidates_for(seq: list[str]) -> list[list[int]]:
        if args.candidates == "table":
            return eng.confusion_ids(seq)
        pool = [vocab[p] for p in english_inventory]
        return [[c for c in pool if c != vocab[p]] for p in seq]

    table = pq.read_table(
        args.parquet, columns=["text", "words", "audio", "speaker", "accuracy"]
    )
    # Cắt TRƯỚC khi to_pylist(): parquet nhúng audio, materialize cả 2500 dòng tốn
    # vài phút và hàng GB RAM dù chỉ cần 300 dòng đầu.
    if args.limit:
        table = table.slice(0, args.limit)
    rows = table.to_pylist()
    log.info("đánh giá %d utterance", len(rows))

    gops: list[float] = []
    humans: list[float] = []
    groups: list[str] = []
    phones: list[str] = []
    # nhãn omission thật từ trường mispronunciations
    is_deletion: list[bool] = []

    skipped = 0
    t0 = time.perf_counter()

    for n, row in enumerate(rows, 1):
        canonical: list[str] = []
        human: list[float] = []
        deletion: list[bool] = []
        ok = True

        for word in row["words"]:
            del_idx = {
                m["index"]
                for m in word["mispronunciations"]
                if m["pronounced-phone"] == DELETION
            }
            for i, (arpa, score) in enumerate(
                zip(word["phones"], word["phones-accuracy"], strict=False)
            ):
                ipa = to_ipa(arpa)
                if ipa is None or ipa not in vocab:
                    ok = False
                    break
                canonical.append(ipa)
                human.append(float(score))
                deletion.append(i in del_idx)
            if not ok:
                break

        if not ok or not canonical:
            skipped += 1
            continue

        waveform, rate = read_wav(row["audio"]["bytes"])
        if rate != 16_000 or len(waveform) < 4800:
            skipped += 1
            continue

        with torch.no_grad():
            logits = eng.model(torch.from_numpy(waveform).unsqueeze(0)).logits[0]
            log_probs = torch.log_softmax(logits, dim=-1)

        values = gop_mod.compute(
            log_probs,
            [vocab[p] for p in canonical],
            candidates_for(canonical),
            blank_id=eng.blank_id,
        )

        gops.extend(values)
        humans.extend(human)
        groups.extend(eng.confusion.group(p) for p in canonical)
        phones.extend(canonical)
        is_deletion.extend(deletion)

        if n % 25 == 0:
            rate_s = n / (time.perf_counter() - t0)
            log.info("  %d/%d  (%.1f utt/s, %d phoneme)", n, len(rows), rate_s, len(gops))

    g = np.array(gops)
    h = np.array(humans)
    grp = np.array(groups)
    dele = np.array(is_deletion)

    log.info("\n%s", "=" * 74)
    log.info("KẾT QUẢ — %d phoneme từ %d utterance (bỏ qua %d)", len(g), len(rows) - skipped, skipped)
    log.info("%s", "=" * 74)

    overall_pcc = pearson(g, h)
    labels = (h < ERROR_THRESHOLD).astype(int)
    auc = roc_auc(-g, labels)  # GOP thấp = lỗi, nên đảo dấu

    log.info("PCC(gop_raw, điểm người) toàn bộ : %+.4f", overall_pcc)
    log.info("ROC-AUC phát hiện lỗi (human<2.0) : %.4f", auc)
    log.info("tỷ lệ phoneme bị chấm có lỗi      : %.1f%%", 100 * labels.mean())
    log.info("GOP: mean %+.2f  sd %.2f  min %+.2f  max %+.2f", g.mean(), g.std(), g.min(), g.max())

    log.info("\n%-14s %7s %8s %8s %9s", "nhóm âm", "n", "PCC", "AUC", "GOP mean")
    log.info("%s", "-" * 74)
    per_group: dict[str, dict] = {}
    for name in sorted(set(groups)):
        m = grp == name
        pcc_g = pearson(g[m], h[m])
        auc_g = roc_auc(-g[m], labels[m])
        per_group[name] = {"n": int(m.sum()), "pcc": pcc_g, "auc": auc_g}
        log.info("%-14s %7d %+8.4f %8.4f %+9.2f", name, m.sum(), pcc_g, auc_g, g[m].mean())

    # Kiểm chứng riêng nhãn omission (§6.3) — GOP có tách được âm bị nuốt không
    if dele.any():
        log.info("\nomission thật (từ mispronunciations): %d phoneme", int(dele.sum()))
        log.info("  GOP mean khi bị nuốt : %+.2f", g[dele].mean())
        log.info("  GOP mean khi có nói  : %+.2f", g[~dele].mean())
        log.info("  ROC-AUC tách omission: %.4f", roc_auc(-g, dele.astype(int)))

    # ── fit calibration ─────────────────────────────────────────────────────────
    log.info("\n%s\nTHAM SỐ CALIBRATION FIT ĐƯỢC (theo nhóm âm)\n%s", "=" * 74, "=" * 74)
    fitted: dict[str, dict[str, float]] = {}
    for name in sorted(set(groups)):
        m = grp == name
        a, b = fit_logistic(g[m], h[m] / 2.0)
        fitted[name] = {"a": round(a, 6), "b": round(b, 6)}
        log.info("  %-14s a=%+9.4f  b=%+8.4f   (n=%d)", name, a, b, int(m.sum()))
    a, b = fit_logistic(g, h / 2.0)
    fitted["other"] = {"a": round(a, 6), "b": round(b, 6)}
    log.info("  %-14s a=%+9.4f  b=%+8.4f   (toàn bộ, dùng làm fallback)", "other", a, b)

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(
        json.dumps(
            {
                "n_phonemes": len(g),
                "n_utterances": len(rows) - skipped,
                "overall_pcc": overall_pcc,
                "error_detection_auc": auc,
                "per_group": per_group,
                "fitted": fitted,
            },
            ensure_ascii=False,
            indent=2,
        ),
        encoding="utf-8",
    )
    log.info("\nđã ghi %s", args.out)

    if args.emit_calibration:
        payload = {
            "version": f"so762-fit-{len(g)}p",
            "_doc": [
                "Fit trên speechocean762 (người bản ngữ tiếng Quan Thoại).",
                "ĐÂY LÀ ĐIỂM KHỞI ĐẦU, chưa phải tham số cho người Việt.",
                "Phải fit lại ở giai đoạn 4 trước khi lên production.",
                "Miền đầu vào là toàn trục thực — không clip.",
            ],
            "formula": "logistic",
            "groups": fitted,
        }
        args.emit_calibration.write_text(
            json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8"
        )
        log.info("đã ghi %s", args.emit_calibration)

    return 0


if __name__ == "__main__":
    sys.exit(main())
