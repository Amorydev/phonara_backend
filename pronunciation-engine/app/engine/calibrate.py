"""GOP thô → điểm 0–100 (§3.3).

MIỀN ĐẦU VÀO LÀ TOÀN TRỤC THỰC.

GOP alignment-free ở §3.2 bước 3 loại chính pᵢ khỏi tập nhiễu, nên không có gì buộc
`max L(perturbed) ≥ L(P)`. GOP dương là **tín hiệu phát âm tốt**, không phải trường hợp
biên hiếm. Clip đầu vào vào một khoảng có chặn trên sẽ bóp méo điểm của đúng những ca
phát âm tốt nhất — và hỏng im lặng, vì không có gì báo lỗi.

(Khác GOP cổ điển Witt & Young, nơi max lấy trên toàn bộ tập phone kể cả phone đúng nên
miền luôn `(−∞,0]`. Tính chất đó KHÔNG áp dụng ở đây.)
"""

from __future__ import annotations

import json
import math
from dataclasses import dataclass
from pathlib import Path

from .confusion import OTHER_GROUP


@dataclass(frozen=True, slots=True)
class _Params:
    a: float
    b: float


@dataclass(frozen=True, slots=True)
class Calibrator:
    version: str
    params: dict[str, _Params]

    def score(self, gop_raw: float, group: str) -> float:
        """→ điểm 0–100. Không clip `gop_raw`; chỉ logistic mới bó giá trị về [0,100]."""
        p = self.params.get(group) or self.params[OTHER_GROUP]
        z = p.a * gop_raw + p.b
        # logistic ổn định số học ở hai đuôi
        if z >= 0:
            sigmoid = 1.0 / (1.0 + math.exp(-z))
        else:
            e = math.exp(z)
            sigmoid = e / (1.0 + e)
        return 100.0 * sigmoid


def load(path: Path) -> Calibrator:
    raw = json.loads(path.read_text(encoding="utf-8"))
    if raw.get("formula") != "logistic":
        raise ValueError(f"formula chưa hỗ trợ: {raw.get('formula')!r}")

    params = {
        name: _Params(a=float(cfg["a"]), b=float(cfg["b"]))
        for name, cfg in raw["groups"].items()
        if not name.startswith("_")
    }
    if OTHER_GROUP not in params:
        raise ValueError(
            f"calibration thiếu nhóm {OTHER_GROUP!r} — cần làm fallback cho phoneme lạ"
        )
    return Calibrator(version=raw["version"], params=params)
