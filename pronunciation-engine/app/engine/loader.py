"""Nạp model một lần, giữ singleton (§3.4).

Một uvicorn worker duy nhất — nhiều worker nghĩa là nhiều bản sao model ~1.2 GB trong RAM.
Đồng thời được kiểm soát bằng semaphore + threadpool ở tầng HTTP, không bằng process.
"""

from __future__ import annotations

import json
import logging
import subprocess
import time
from dataclasses import dataclass, field

import torch
from transformers import Wav2Vec2ForCTC, Wav2Vec2PhonemeCTCTokenizer

from ..config import settings
from . import calibrate, confusion
from .diagnosis import MergeRules
from .g2p import G2P

log = logging.getLogger(__name__)


def _espeak_version() -> str:
    """g2p_version (§8) — nằm trong base image, không trong code hay artifact.

    Đây là trục version dễ sót nhất: một lần `docker build` lại có thể đổi chuỗi phoneme
    và kéo theo mọi thứ phía sau mà không để lại dấu vết nào (R10).
    """
    try:
        out = subprocess.run(
            ["espeak-ng", "--version"], capture_output=True, text=True, timeout=5
        ).stdout
        # "eSpeak NG text-to-speech: 1.52.0  Data at: ..."
        for part in out.split():
            if part[:1].isdigit():
                return f"espeak-ng-{part}"
    except Exception:  # noqa: BLE001 — thiếu version không được làm chết service
        log.warning("không đọc được phiên bản espeak-ng", exc_info=True)
    return "espeak-ng-unknown"


@dataclass
class Engine:
    model: Wav2Vec2ForCTC
    tokenizer: Wav2Vec2PhonemeCTCTokenizer
    g2p: G2P
    confusion: confusion.ConfusionTable
    calibrator: calibrate.Calibrator
    merge_rules: MergeRules
    blank_id: int
    id_to_symbol: dict[int, str]
    model_version: str
    g2p_version: str
    warmed_up: bool = False

    _warned_no_candidates: set[str] = field(default_factory=set)

    def confusion_ids(self, phonemes: list[str]) -> list[list[int]]:
        """Ứng viên nhiễu cho từng phoneme.

        Phoneme vắng khỏi bảng KHÔNG gây lỗi — nó chỉ nhận tập rỗng, tức GOP chỉ còn so
        với phương án xóa. Chất lượng tụt âm thầm ở đúng âm đó, nên phải kêu lên. Đây là
        cách `juː` (không tồn tại) và `əl` (tồn tại nhưng thiếu) lọt qua vòng đầu.
        """
        vocab = self.tokenizer.get_vocab()
        out: list[list[int]] = []
        for p in phonemes:
            cands = [vocab[c] for c in self.confusion.confuse(p) if c in vocab]
            if not cands and p not in self._warned_no_candidates:
                self._warned_no_candidates.add(p)
                log.warning(
                    "phoneme %r không có ứng viên nhiễu — GOP chỉ so với phương án xóa. "
                    "Chạy r1/inventory.py rồi bổ sung vào confusion_vi.json",
                    p,
                )
            out.append(cands)
        return out


_engine: Engine | None = None


def get() -> Engine:
    if _engine is None:
        raise RuntimeError("engine chưa nạp — lifespan chưa chạy?")
    return _engine


def is_loaded() -> bool:
    return _engine is not None


def load() -> Engine:
    """Nạp model + artifact. Ném exception nếu bất kỳ artifact nào không hợp lệ.

    Fail fast lúc khởi động, không bao giờ lúc inference — một bảng nhầm lẫn sai sẽ không
    gây exception khi chấm, nó chỉ âm thầm cho ra likelihood vô nghĩa.
    """
    global _engine
    t0 = time.perf_counter()

    threads = settings.resolved_torch_threads()
    torch.set_num_threads(threads)
    log.info("torch threads = %d", threads)

    tokenizer = Wav2Vec2PhonemeCTCTokenizer.from_pretrained(
        settings.model_id, revision=settings.model_revision
    )
    model = Wav2Vec2ForCTC.from_pretrained(
        settings.model_id, revision=settings.model_revision
    )
    model.eval()

    vocab = tokenizer.get_vocab()
    table = confusion.load(settings.confusion_path, vocab)
    calibrator = calibrate.load(settings.calibration_path)

    raw_rules = json.loads(settings.merge_rules_path.read_text(encoding="utf-8"))
    rules = MergeRules(
        version=raw_rules["version"],
        tau_uncertain=float(raw_rules["tau_uncertain"]),
        tau_gop_low=float(raw_rules["tau_gop_low"]),
        tau_gop_high=float(raw_rules["tau_gop_high"]),
    )

    _engine = Engine(
        model=model,
        tokenizer=tokenizer,
        g2p=G2P(tokenizer),
        confusion=table,
        calibrator=calibrator,
        merge_rules=rules,
        blank_id=model.config.pad_token_id,
        id_to_symbol={i: s for s, i in vocab.items()},
        model_version=f"xlsr53-espeak@{settings.model_revision}",
        g2p_version=_espeak_version(),
    )

    log.info(
        "engine nạp xong trong %.1fs | vocab=%d | confusion=%s | calibration=%s | merge=%s",
        time.perf_counter() - t0,
        len(vocab),
        table.version,
        calibrator.version,
        rules.version,
    )
    if "PLACEHOLDER" in calibrator.version:
        log.warning(
            "calibration đang dùng bản PLACEHOLDER chưa fit trên dữ liệu nào — "
            "điểm số CHƯA có ý nghĩa. Xem §3.3 và giai đoạn 4."
        )
    return _engine


def warm_up() -> None:
    """Lần inference đầu luôn chậm bất thường (§3.4). Trả giá đó lúc khởi động."""
    eng = get()
    t0 = time.perf_counter()
    with torch.no_grad():
        eng.model(torch.zeros(1, settings.sample_rate)).logits
    eng.warmed_up = True
    log.info("warm-up xong trong %.1fs", time.perf_counter() - t0)
