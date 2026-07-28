"""So engine giữa các tiếng mẹ đẻ — cổng chặn hồi quy cho yêu cầu "app không chỉ cho người Việt".

    python eval/compare_l1.py

Đọc mọi `eval/results-l2arctic-*.json` do `l2arctic.py` sinh ra và in bảng đối chiếu.

VÌ SAO CẦN: engine không nhận tiếng mẹ đẻ làm đầu vào và không có artifact tách theo L1
(xem docstring `app/engine/confusion.py`). Đó là một QUYẾT ĐỊNH, và quyết định thì phải đo
chứ không tin suông. Nếu một thứ tiếng tụt hẳn so với phần còn lại thì giả định "một cấu
hình dùng chung cho mọi người học" đã hỏng, và phải xử lý trước khi mở thị trường đó.

Ngưỡng cảnh báo là **khoảng cách** giữa L1 tốt nhất và tệ nhất, không phải giá trị tuyệt
đối: người học ở các L1 khác nhau mắc lỗi nhiều ít khác nhau, nên AUC thấp có thể chỉ phản
ánh bài toán khó hơn. Chênh lệch lớn mới là dấu hiệu engine thiên vị.

    python eval/compare_l1.py --max-spread 0.15
"""

from __future__ import annotations

import argparse
import glob
import json
import sys
from pathlib import Path

# Chênh lệch AUC tối đa chấp nhận được giữa L1 tốt nhất và tệ nhất.
#
# 0,15 không phải con số thiêng. Nó đặt rộng hơn khoảng quan sát được để bắt SỰ SỤT ĐỔ
# chứ không bắt dao động thường: L1 khác nhau kéo theo tỷ lệ lỗi khác nhau (18%–26% trong
# L2-ARCTIC), và điều đó một mình đã đủ làm AUC xê dịch.
DEFAULT_MAX_SPREAD = 0.15


def _corr(x: list[float], y: list[float]) -> float:
    """Pearson thuần Python — script này cố ý không kéo theo numpy."""
    n = len(x)
    if n < 2:
        return float("nan")
    mx, my = sum(x) / n, sum(y) / n
    cov = sum((a - mx) * (b - my) for a, b in zip(x, y, strict=True))
    vx = sum((a - mx) ** 2 for a in x) ** 0.5
    vy = sum((b - my) ** 2 for b in y) ** 0.5
    return cov / (vx * vy) if vx and vy else float("nan")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", type=Path, default=Path("eval"))
    ap.add_argument("--max-spread", type=float, default=DEFAULT_MAX_SPREAD)
    args = ap.parse_args()

    results = []
    for path in sorted(glob.glob(str(args.dir / "results-l2arctic-*.json"))):
        data = json.loads(Path(path).read_text(encoding="utf-8"))
        if "error_detection_auc" not in data:
            continue
        results.append(data)

    if len(results) < 2:
        print(f"cần ≥2 kết quả trong {args.dir}/, mới có {len(results)}.")
        print("Chạy: for L in Vietnamese Chinese Korean Hindi Spanish Arabic; do \\")
        print("  python eval/l2arctic.py --parquet <parquet> --l1 $L \\")
        print("    --out eval/results-l2arctic-$L.json; done")
        return 1

    results.sort(key=lambda d: -d["error_detection_auc"])

    print("=" * 78)
    print("ENGINE GIỮA CÁC TIẾNG MẸ ĐẺ  (L2-ARCTIC, CC BY-NC 4.0 — chỉ để đo)")
    print("=" * 78)
    head = f"{'tiếng mẹ đẻ':13}{'âm vị':>7}{'AUC':>8}{'lỗi thật':>10}"
    head += f"{'P':>7}{'R':>7}{'om R':>7}{'said':>7}{'unc':>7}"
    print(head)
    print("-" * 78)
    for d in results:
        print(
            f"{d['l1']:13}{d['n_phonemes']:7d}{d['error_detection_auc']:8.4f}"
            f"{100 * d['truth_error_rate']:9.1f}%"
            f"{d['diagnosis']['precision']:7.3f}{d['diagnosis']['recall']:7.3f}"
            f"{d['omission']['recall']:7.3f}"
            f"{100 * d['said_accuracy']:6.0f}%{100 * d['uncertain_rate']:6.1f}%"
        )

    aucs = [d["error_detection_auc"] for d in results]
    spread = max(aucs) - min(aucs)
    best, worst = results[0]["l1"], results[-1]["l1"]

    print("\n" + "=" * 78)
    print("PHÂN RÃ THEO NHÓM ÂM — bắt thiên vị có cấu trúc")
    print("=" * 78)
    groups = sorted({g for d in results for g in d["per_group"]})
    print(f"{'':13}" + "".join(f"{g[:9]:>10}" for g in groups))
    for d in results:
        cells = "".join(
            f"{d['per_group'][g]['auc']:10.3f}" if g in d["per_group"] else f"{'-':>10}"
            for g in groups
        )
        print(f"{d['l1']:13}{cells}")

    # Nhóm âm nào lệch nhiều nhất giữa các L1 — chỗ đáng nghi nhất nếu có thiên vị.
    print(f"\n{'chênh lệch':13}", end="")
    for g in groups:
        vals = [d["per_group"][g]["auc"] for d in results if g in d["per_group"]]
        print(f"{max(vals) - min(vals):10.3f}" if len(vals) > 1 else f"{'-':>10}", end="")
    print()

    # ── precision so với tỷ lệ lỗi nền ──────────────────────────────────────────
    #
    # Precision chênh nhau rất nhiều giữa các L1, nhìn qua tưởng là thiên vị. Kiểm trước
    # khi kết luận: với ngưỡng cố định, tỷ lệ lỗi nền càng thấp thì cùng một số lần báo
    # động sẽ sinh ra càng nhiều báo nhầm. Đó là số học, không phải engine.
    #
    # Tương quan cao ở đây nghĩa là chênh lệch precision đã được giải thích hết bằng tỷ lệ
    # lỗi, và KHÔNG còn gì để quy cho thiên vị theo tiếng mẹ đẻ.
    err = [d["truth_error_rate"] for d in results]
    prec = [d["diagnosis"]["precision"] for d in results]

    print("\n" + "=" * 78)
    print("PRECISION CHÊNH NHAU — CÓ PHẢI THIÊN VỊ KHÔNG?")
    print("=" * 78)
    print(f"corr(tỷ lệ lỗi nền, precision) = {_corr(err, prec):+.3f}")
    print(f"corr(tỷ lệ lỗi nền, AUC)       = {_corr(err, aucs):+.3f}")
    print("Tương quan precision cao = chênh lệch precision là SỐ HỌC của tỷ lệ lỗi nền,")
    print("không phải engine kém với nhóm nào. Tương quan AUC thấp = AUC đo chất lượng xếp")
    print("hạng thật, không chỉ phản chiếu độ khó của bài toán.")
    print("\nHệ quả sản phẩm, KHÔNG liên quan tiếng mẹ đẻ: người học ÍT lỗi sẽ thấy nhiều báo")
    print("động sai hơn. Đó là chuyện trình độ. Cần gạt đúng là ngưỡng thích ứng theo lịch sử")
    print("của từng người — vốn đã trung lập với L1, không cần biết họ nói tiếng gì.")

    print("\n" + "=" * 78)
    print("KẾT LUẬN")
    print("=" * 78)
    print(f"khoảng AUC : {min(aucs):.4f} ({worst}) – {max(aucs):.4f} ({best})")
    print(f"chênh lệch : {spread:.4f}   (trần {args.max_spread:.2f})")

    if spread > args.max_spread:
        print(f"\n✗ {worst} tụt quá xa so với {best}.")
        print("  Giả định 'một cấu hình dùng chung cho mọi tiếng mẹ đẻ' KHÔNG còn đứng vững.")
        print("  Trước khi mở thị trường đó: kiểm bảng nhóm âm ở trên xem lệch tập trung ở")
        print("  lớp âm nào, rồi cân nhắc đổi model (§10.1) — KHÔNG nhân bản bảng nhầm lẫn")
        print("  theo từng thứ tiếng, §3.6.4 đã chứng minh lever đó vô hiệu.")
        return 1

    print(f"\n✓ chênh lệch trong ngưỡng — engine không thiên vị theo tiếng mẹ đẻ.")
    print("  Lưu ý khi đọc: AUC thấp KHÔNG tự nó nghĩa là engine kém với nhóm đó. Tỷ lệ lỗi")
    print("  thật khác nhau giữa các L1, mà lỗi càng thô thì càng dễ tách.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
