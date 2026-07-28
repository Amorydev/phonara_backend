"""Bảng nhầm lẫn phoneme + nhóm âm.

Hợp nhất hai tầng (§3.1): ứng viên `tier_l1` được xếp lên đầu, rồi bù thêm từ
`tier_general` cho đủ `target_candidates`.

**Engine KHÔNG phụ thuộc tiếng mẹ đẻ của người học.** `tier_l1` từng mang tên `tier_vi`,
nhưng đo ra nó chỉ đổi **2/232 ứng viên cuối cùng (0,9%)**: với `target_candidates = 4`,
`tier_general` đã phủ gần hết những gì tầng kia muốn thêm, nên việc xếp lại thứ tự hầu như
không đổi top-4.

Cộng với §3.6.4 — mở tập ứng viên từ 4 lên 45 chỉ dịch PCC +0,0107, dưới cả biên độ nhiễu
lấy mẫu — kết luận là **bảng nhầm lẫn không phải cần gạt để đặc thù hoá theo L1**. Giữ cơ
chế hai tầng vì nó rẻ và có thể hữu ích khi đổi model, nhưng đừng trông đợi nó tạo khác
biệt, và đừng nhân bản bảng này theo từng thứ tiếng.

Ràng buộc R9 — số ứng viên phải đồng đều giữa các phoneme, nếu không GOP lệch có cấu trúc
theo lớp âm. `load()` cưỡng chế điều này và fail fast lúc nạp.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

OTHER_GROUP = "other"


class ConfusionTableError(RuntimeError):
    """Bảng nhầm lẫn không hợp lệ. Luôn ném lúc nạp, không bao giờ lúc inference."""


@dataclass(frozen=True, slots=True)
class ConfusionTable:
    version: str
    candidates: dict[str, tuple[str, ...]]
    group_of: dict[str, str]

    def confuse(self, phoneme: str) -> tuple[str, ...]:
        return self.candidates.get(phoneme, ())

    def group(self, phoneme: str) -> str:
        return self.group_of.get(phoneme, OTHER_GROUP)


def _merge(
    general: dict[str, list[str]],
    l1: dict[str, list[str]],
    target: int,
) -> dict[str, tuple[str, ...]]:
    merged: dict[str, tuple[str, ...]] = {}
    for phoneme in general:
        # tier_l1 trước: ứng viên từ kiến thức miền được ưu tiên. Đo ra chỉ đổi 0,9%
        # ứng viên cuối — xem docstring đầu file trước khi dựa vào tầng này.
        ordered: list[str] = []
        for src in (l1.get(phoneme, []), general.get(phoneme, [])):
            for cand in src:
                if cand != phoneme and cand not in ordered:
                    ordered.append(cand)
        merged[phoneme] = tuple(ordered[:target])
    return merged


def load(path: Path, vocab: dict[str, int]) -> ConfusionTable:
    """Nạp, xác thực với vocab, hợp nhất hai tầng.

    Mọi ký hiệu trong bảng phải nằm trong vocab của tokenizer. Một phoneme lạ ở đây sẽ
    không gây exception lúc inference — nó chỉ âm thầm cho ra likelihood vô nghĩa. Vì vậy
    phải bắt ngay lúc nạp.
    """
    raw = json.loads(path.read_text(encoding="utf-8"))

    general = {k: v for k, v in raw["tier_general"].items() if not k.startswith("_")}
    l1 = {k: v for k, v in raw["tier_l1"].items() if not k.startswith("_")}
    groups = {k: v for k, v in raw["phone_groups"].items() if not k.startswith("_")}
    target = int(raw["target_candidates"])

    # ── xác thực với vocab ──────────────────────────────────────────────────────
    referenced: set[str] = set()
    for table in (general, l1):
        for key, cands in table.items():
            referenced.add(key)
            referenced.update(cands)
    for members in groups.values():
        referenced.update(members)

    oov = sorted(p for p in referenced if p not in vocab)
    if oov:
        raise ConfusionTableError(
            f"{len(oov)} ký hiệu trong {path.name} không có trong vocab tokenizer: {oov}. "
            "Sửa bảng hoặc kiểm tra lại model — xem r1/README.md."
        )

    # tier_l1 không được giới thiệu phoneme lạ mà tier_general chưa biết
    unknown_l1 = sorted(set(l1) - set(general))
    if unknown_l1:
        raise ConfusionTableError(
            f"tier_l1 có khoá không tồn tại trong tier_general: {unknown_l1}. "
            "tier_general phải phủ mọi phoneme để đảm bảo ai cũng có ứng viên."
        )

    candidates = _merge(general, l1, target)

    # ── ràng buộc R9: số ứng viên đồng đều ──────────────────────────────────────
    sizes = {p: len(c) for p, c in candidates.items()}
    thin = sorted(p for p, n in sizes.items() if n < target)
    if thin:
        raise ConfusionTableError(
            f"{len(thin)} phoneme có ít hơn {target} ứng viên: {thin}. "
            "Chênh lệch số ứng viên tạo sai lệch cấu trúc trong GOP (R9)."
        )

    group_of: dict[str, str] = {}
    for group, members in groups.items():
        for phoneme in members:
            group_of[phoneme] = group

    ungrouped = sorted(set(candidates) - set(group_of))
    if ungrouped:
        raise ConfusionTableError(
            f"{len(ungrouped)} phoneme không thuộc nhóm âm nào: {ungrouped}. "
            "Calibration theo nhóm âm (§3.3) cần phủ toàn bộ."
        )

    return ConfusionTable(
        version=raw["version"],
        candidates=candidates,
        group_of=group_of,
    )
