"""Bảng nhầm lẫn phoneme + nhóm âm.

Hợp nhất hai tầng (§3.1): ứng viên tier_vi được ưu tiên lên đầu vì đó là nơi kiến thức
miền tạo khác biệt, rồi bù thêm từ tier_general cho đủ `target_candidates`.

Ràng buộc R9 — số ứng viên phải đồng đều giữa các phoneme, nếu không GOP lệch có cấu trúc
theo lớp âm. `validate()` cưỡng chế điều này và fail fast lúc nạp.
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
    vi: dict[str, list[str]],
    target: int,
) -> dict[str, tuple[str, ...]]:
    merged: dict[str, tuple[str, ...]] = {}
    for phoneme in general:
        # tier_vi trước — lỗi người Việt là ứng viên đáng so nhất
        ordered: list[str] = []
        for src in (vi.get(phoneme, []), general.get(phoneme, [])):
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
    vi = {k: v for k, v in raw["tier_vi"].items() if not k.startswith("_")}
    groups = {k: v for k, v in raw["phone_groups"].items() if not k.startswith("_")}
    target = int(raw["target_candidates"])

    # ── xác thực với vocab ──────────────────────────────────────────────────────
    referenced: set[str] = set()
    for table in (general, vi):
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

    # tier_vi không được giới thiệu phoneme lạ mà tier_general chưa biết
    unknown_vi = sorted(set(vi) - set(general))
    if unknown_vi:
        raise ConfusionTableError(
            f"tier_vi có khoá không tồn tại trong tier_general: {unknown_vi}. "
            "tier_general phải phủ mọi phoneme để đảm bảo ai cũng có ứng viên."
        )

    candidates = _merge(general, vi, target)

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
