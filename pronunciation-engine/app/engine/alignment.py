"""Needleman-Wunsch căn chuỗi phoneme chuẩn với chuỗi decode được.

Thuần logic, không phụ thuộc model — vì vậy test được độc lập (§3.5).

Đây là nhánh **chẩn đoán**, tách hoàn toàn khỏi nhánh **điểm số** (GOP) theo nguyên tắc 3.
Nhánh này cho biết người học *thực sự nói gì*; GOP cho biết *tốt đến đâu*. Hai nhánh chỉ
gặp nhau ở bước hợp nhất.

Ưu điểm so với GOP alignment-free thuần: xử lý được **insertion**, thứ mà phương pháp
nhiễu-chuỗi trong paper không xét (chỉ substitution và deletion).
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from enum import StrEnum

GAP_COST = 1.0
SUB_COST_FAR = 1.0
SUB_COST_NEAR = 0.6  # nằm trong tập nhầm lẫn của nhau → lệch "hợp lý" hơn


class Op(StrEnum):
    MATCH = "match"
    SUBSTITUTION = "substitution"
    OMISSION = "omission"  # có trong canonical, vắng trong decoded
    INSERTION = "insertion"  # có trong decoded, vắng trong canonical


@dataclass(frozen=True, slots=True)
class AlignedPair:
    op: Op
    canon_index: int | None
    decoded_index: int | None


def _default_sub_cost(_a: str, _b: str) -> float:
    return SUB_COST_FAR


def align(
    canonical: list[str],
    decoded: list[str],
    sub_cost: Callable[[str, str], float] = _default_sub_cost,
) -> list[AlignedPair]:
    """Căn hai chuỗi, trả về danh sách thao tác theo thứ tự canonical.

    `sub_cost(expected, said)` cho phép nạp khoảng cách ngữ âm — cặp nằm trong tập nhầm
    lẫn của nhau bị phạt nhẹ hơn, nên NW ưu tiên giải thích lệch bằng substitution thay
    vì một cặp omission+insertion.
    """
    n, m = len(canonical), len(decoded)

    # dp[i][j] = chi phí tối thiểu căn canonical[:i] với decoded[:j]
    dp = [[0.0] * (m + 1) for _ in range(n + 1)]
    for i in range(1, n + 1):
        dp[i][0] = i * GAP_COST
    for j in range(1, m + 1):
        dp[0][j] = j * GAP_COST

    for i in range(1, n + 1):
        for j in range(1, m + 1):
            c, d = canonical[i - 1], decoded[j - 1]
            diag = dp[i - 1][j - 1] + (0.0 if c == d else sub_cost(c, d))
            dp[i][j] = min(
                diag,
                dp[i - 1][j] + GAP_COST,  # omission
                dp[i][j - 1] + GAP_COST,  # insertion
            )

    # truy vết ngược
    pairs: list[AlignedPair] = []
    i, j = n, m
    while i > 0 or j > 0:
        if i > 0 and j > 0:
            c, d = canonical[i - 1], decoded[j - 1]
            step = 0.0 if c == d else sub_cost(c, d)
            if abs(dp[i][j] - (dp[i - 1][j - 1] + step)) < 1e-9:
                pairs.append(
                    AlignedPair(
                        op=Op.MATCH if c == d else Op.SUBSTITUTION,
                        canon_index=i - 1,
                        decoded_index=j - 1,
                    )
                )
                i, j = i - 1, j - 1
                continue
        if i > 0 and abs(dp[i][j] - (dp[i - 1][j] + GAP_COST)) < 1e-9:
            pairs.append(AlignedPair(op=Op.OMISSION, canon_index=i - 1, decoded_index=None))
            i -= 1
            continue
        pairs.append(AlignedPair(op=Op.INSERTION, canon_index=None, decoded_index=j - 1))
        j -= 1

    pairs.reverse()
    return pairs
