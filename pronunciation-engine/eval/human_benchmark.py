"""Đo engine trên giọng người Việt — cổng quyết định (§6 của plan).

Nhận bộ dữ liệu do `backend/cmd/export-benchmark` xuất ra, cộng với phiếu chấm đã điền
của ≥2 người chấm độc lập.

    python eval/human_benchmark.py --bundle ./benchmark-bundle

THỨ TỰ CÁC BƯỚC LÀ CÓ CHỦ ĐÍCH:

  1. Đồng thuận giữa NGƯỜI CHẤM — chạy trước, và là cổng chặn
  2. Chất lượng engine so với nhãn người
  3. Hiệu chỉnh ngưỡng
  4. Đối chiếu tiêu chí thông qua §6.4

Bước 1 phải trước vì nếu hai người chấm không đồng ý với nhau thì **không mô hình nào có
thể đạt được** — mọi con số ở bước 2 chỉ là đo độ nhiễu của nhãn. Gặp trường hợp đó thì
phải sửa hướng dẫn chấm và chấm lại, chứ không phải chỉnh model.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import numpy as np

# Ngưỡng nhị phân: dưới 2.0 nghĩa là không phải phát âm chuẩn.
ERROR_THRESHOLD = 2.0

# Ngưỡng đồng thuận tối thiểu giữa hai người chấm. Dưới mức này thì dữ liệu nhãn quá
# nhiễu để kết luận bất cứ điều gì về engine.
MIN_KAPPA = 0.60


def load_jsonl(path: Path) -> list[dict]:
    if not path.exists():
        raise SystemExit(f"không tìm thấy {path}")
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def quadratic_weighted_kappa(a: np.ndarray, b: np.ndarray, n_classes: int = 3) -> float:
    """Cohen's kappa có trọng số bậc hai — hợp cho thang thứ tự 0/1/2.

    Trọng số bậc hai phạt lệch 2 bậc (0 vs 2) nặng hơn lệch 1 bậc (1 vs 2) rất nhiều.
    Kappa thường (không trọng số) coi mọi bất đồng như nhau, nên với thang thứ tự nó
    báo động giả: hai người chấm 1 và 2 gần như đồng ý, còn 0 và 2 thì hoàn toàn không.
    """
    if len(a) == 0:
        return float("nan")

    observed = np.zeros((n_classes, n_classes))
    for x, y in zip(a, b, strict=True):
        observed[int(x), int(y)] += 1

    weights = np.array(
        [[(i - j) ** 2 for j in range(n_classes)] for i in range(n_classes)],
        dtype=float,
    ) / (n_classes - 1) ** 2

    hist_a = np.bincount(a.astype(int), minlength=n_classes)
    hist_b = np.bincount(b.astype(int), minlength=n_classes)
    expected = np.outer(hist_a, hist_b).astype(float)

    observed = observed / observed.sum()
    expected = expected / expected.sum()

    denom = (weights * expected).sum()
    if denom == 0:
        return float("nan")
    return float(1 - (weights * observed).sum() / denom)


def pearson(x: np.ndarray, y: np.ndarray) -> float:
    if len(x) < 2 or x.std() == 0 or y.std() == 0:
        return float("nan")
    return float(np.corrcoef(x, y)[0, 1])


def roc_auc(scores: np.ndarray, labels: np.ndarray) -> float:
    pos, neg = labels == 1, labels == 0
    n_pos, n_neg = int(pos.sum()), int(neg.sum())
    if n_pos == 0 or n_neg == 0:
        return float("nan")
    order = scores.argsort()
    ranks = np.empty(len(scores), dtype=np.float64)
    ranks[order] = np.arange(1, len(scores) + 1)
    return float((ranks[pos].sum() - n_pos * (n_pos + 1) / 2) / (n_pos * n_neg))


def prf(tp: int, fp: int, fn: int) -> tuple[float, float, float]:
    precision = tp / (tp + fp) if tp + fp else 0.0
    recall = tp / (tp + fn) if tp + fn else 0.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    return precision, recall, f1


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--bundle", required=True, type=Path)
    ap.add_argument("--raters", default="A,B")
    ap.add_argument("--out", type=Path, default=None)
    args = ap.parse_args()

    manifest = {u["job_id"]: u for u in load_jsonl(args.bundle / "manifest.jsonl")}
    rater_ids = [r.strip() for r in args.raters.split(",") if r.strip()]
    if len(rater_ids) < 2:
        raise SystemExit("cần ít nhất 2 người chấm để đo đồng thuận")

    # nhãn[rater][(job_id, index)] = score
    labels: dict[str, dict[tuple[str, int], int]] = {}
    said: dict[str, dict[tuple[str, int], str]] = {}
    for rid in rater_ids:
        rows = load_jsonl(args.bundle / f"labels_{rid}.jsonl")
        labels[rid] = {}
        said[rid] = {}
        for row in rows:
            for cell in row["phonemes"]:
                if cell["score"] < 0:
                    continue  # chưa chấm
                labels[rid][(row["job_id"], cell["index"])] = int(cell["score"])
                said[rid][(row["job_id"], cell["index"])] = cell.get("said", "")

    shared = set(labels[rater_ids[0]])
    for rid in rater_ids[1:]:
        shared &= set(labels[rid])
    shared_keys = sorted(shared)

    print("=" * 74)
    print("BƯỚC 1 — ĐỒNG THUẬN GIỮA NGƯỜI CHẤM  (cổng chặn)")
    print("=" * 74)

    if not shared_keys:
        print("✗ chưa có âm vị nào được CẢ HAI người chấm. Chưa đo được gì.")
        return 1

    print(f"âm vị được cả {len(rater_ids)} người chấm: {len(shared_keys)}")
    kappas = []
    for i in range(len(rater_ids)):
        for j in range(i + 1, len(rater_ids)):
            a = np.array([labels[rater_ids[i]][k] for k in shared_keys])
            b = np.array([labels[rater_ids[j]][k] for k in shared_keys])
            k = quadratic_weighted_kappa(a, b)
            exact = float((a == b).mean())
            kappas.append(k)
            print(f"  {rater_ids[i]} ↔ {rater_ids[j]}: kappa={k:+.3f}  trùng khít={exact:.1%}")

    worst = min(kappas)
    if worst < MIN_KAPPA:
        print(f"\n✗ DỪNG. Đồng thuận {worst:.3f} < {MIN_KAPPA} — nhãn quá nhiễu.")
        print("  Hai người chấm không đồng ý với nhau thì KHÔNG mô hình nào đạt được.")
        print("  Việc cần làm: soát lại hướng dẫn chấm, thống nhất ca khó, chấm lại.")
        print("  KHÔNG chỉnh model dựa trên số liệu này.")
        return 1
    print(f"\n✓ đồng thuận đạt ({worst:.3f} ≥ {MIN_KAPPA}) — số liệu bên dưới có ý nghĩa")

    # ── nhãn hợp nhất ───────────────────────────────────────────────────────────
    # Trung bình cho tương quan (giữ thông tin thứ tự); "bất kỳ ai nói sai" cho phân
    # loại nhị phân (thận trọng: thà bắt nhầm còn hơn bỏ sót lỗi phát âm).
    human_mean, human_error = {}, {}
    for k in shared_keys:
        scores = [labels[r][k] for r in rater_ids]
        human_mean[k] = float(np.mean(scores))
        human_error[k] = int(any(s < ERROR_THRESHOLD for s in scores))

    # ── đối chiếu với engine ────────────────────────────────────────────────────
    groups = _phone_groups()

    gop, acc, hmean, herr, grp, phones = [], [], [], [], [], []
    engine_says_error, engine_uncertain, engine_omission = [], [], []
    for (job_id, idx) in shared_keys:
        u = manifest.get(job_id)
        if not u:
            continue
        ph = next((p for p in u["engine_phonemes"] if p["index"] == idx), None)
        if ph is None:
            continue
        gop.append(ph["gop_raw"])
        acc.append(ph["accuracy"] if ph["accuracy"] is not None else np.nan)
        hmean.append(human_mean[(job_id, idx)])
        herr.append(human_error[(job_id, idx)])
        grp.append(groups.get(ph["expected"], "other"))
        phones.append(ph["expected"])
        engine_says_error.append(int(ph["diagnosis"] in ("substitution", "omission", "insertion")))
        engine_uncertain.append(int(ph["diagnosis"] == "uncertain"))
        engine_omission.append(int(ph["diagnosis"] == "omission"))

    gop = np.array(gop)
    hmean = np.array(hmean)
    herr = np.array(herr)
    grp = np.array(grp)
    eerr = np.array(engine_says_error)
    eunc = np.array(engine_uncertain)

    print("\n" + "=" * 74)
    print(f"BƯỚC 2 — CHẤT LƯỢNG ENGINE  ({len(gop)} âm vị)")
    print("=" * 74)
    print(f"PCC(gop_raw, điểm người)      : {pearson(gop, hmean):+.4f}")
    print(f"ROC-AUC phát hiện lỗi          : {roc_auc(-gop, herr):.4f}")
    print(f"tỷ lệ người chấm là có lỗi     : {herr.mean():.1%}")
    print(f"tỷ lệ engine trả 'uncertain'   : {eunc.mean():.1%}")

    tp = int(((eerr == 1) & (herr == 1)).sum())
    fp = int(((eerr == 1) & (herr == 0)).sum())
    fn = int(((eerr == 0) & (herr == 1)).sum())
    p, r, f1 = prf(tp, fp, fn)
    print(f"\nphát hiện lỗi: precision={p:.3f}  recall={r:.3f}  F1={f1:.3f}")
    print("  precision thấp = báo sai khi người học nói ĐÚNG → mất niềm tin nhanh nhất")
    print("  recall thấp    = bỏ sót lỗi → người học không biết mình sai")

    print(f"\n{'nhóm âm':14} {'n':>6} {'PCC':>9} {'AUC':>8} {'uncertain':>10}")
    print("-" * 74)
    for name in sorted(set(grp)):
        m = grp == name
        if m.sum() < 5:
            continue
        print(f"{name:14} {m.sum():6d} {pearson(gop[m], hmean[m]):+9.4f} "
              f"{roc_auc(-gop[m], herr[m]):8.4f} {eunc[m].mean():10.1%}")

    # ── hiệu chỉnh ngưỡng ───────────────────────────────────────────────────────
    print("\n" + "=" * 74)
    print("BƯỚC 3 — NGƯỠNG ĐỀ XUẤT")
    print("=" * 74)
    best = max(
        ((thr, *_f1_at(gop, herr, thr)) for thr in np.percentile(gop, np.arange(1, 100))),
        key=lambda t: t[3],
    )
    print(f"τ_gop tách lỗi tốt nhất: {best[0]:+.3f}  "
          f"(precision={best[1]:.3f} recall={best[2]:.3f} F1={best[3]:.3f})")
    print("  → dùng làm điểm khởi đầu cho tau_gop_low trong merge_rules.json")

    # ── tiêu chí thông qua §6.4 ─────────────────────────────────────────────────
    print("\n" + "=" * 74)
    print("BƯỚC 4 — TIÊU CHÍ THÔNG QUA (§6.4)")
    print("=" * 74)
    checks = [
        ("tỷ lệ uncertain < 20%", eunc.mean() < 0.20, f"{eunc.mean():.1%}"),
        ("không lớp âm nào sai hệ thống",
         all(roc_auc(-gop[grp == g], herr[grp == g]) > 0.5
             for g in set(grp) if (grp == g).sum() >= 5),
         "xem bảng nhóm âm"),
    ]
    for name, ok, detail in checks:
        print(f"  {'✓' if ok else '✗'} {name:38} {detail}")
    print("\n  ⚠ tiêu chí F1 ≥ 80% mức Azure cần chạy Azure trên CÙNG bộ này để so.")

    if args.out:
        args.out.write_text(json.dumps({
            "n_phonemes": int(len(gop)),
            "kappa_min": worst,
            "pcc": pearson(gop, hmean),
            "auc": roc_auc(-gop, herr),
            "precision": p, "recall": r, "f1": f1,
            "uncertain_rate": float(eunc.mean()),
            "suggested_tau_gop": float(best[0]),
        }, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"\nđã ghi {args.out}")
    return 0


def _f1_at(gop: np.ndarray, herr: np.ndarray, thr: float) -> tuple[float, float, float]:
    pred = (gop < thr).astype(int)
    tp = int(((pred == 1) & (herr == 1)).sum())
    fp = int(((pred == 1) & (herr == 0)).sum())
    fn = int(((pred == 0) & (herr == 1)).sum())
    return prf(tp, fp, fn)


def _phone_groups() -> dict[str, str]:
    path = Path(__file__).resolve().parents[1] / "artifacts" / "confusion.json"
    raw = json.loads(path.read_text(encoding="utf-8"))
    out: dict[str, str] = {}
    for group, members in raw["phone_groups"].items():
        if not isinstance(members, list):
            continue
        for p in members:
            out[p] = group
    return out


if __name__ == "__main__":
    sys.exit(main())
