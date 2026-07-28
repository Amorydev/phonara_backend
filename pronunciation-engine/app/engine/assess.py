"""Điều phối: audio + text → NormalizedAssessmentResult (§3.2).

Thứ tự cố định:
  1. G2P                        → chuỗi chuẩn + word_index
  2. Forward pass               → log-probs, CHẠY MỘT LẦN
  3. GOP alignment-free         → điểm thô          ┐ hai nhánh
  4. CTC decode + NW            → chẩn đoán         ┘ độc lập
  5. Hợp nhất                   → nhãn cuối
  6. Tổng hợp                   → word, overall
"""

from __future__ import annotations

import time

import numpy as np
import torch

from ..config import settings
from ..schemas import (
    AssessmentResult,
    AudioInfo,
    Capability,
    OverallScore,
    PhonemeDiagnosis,
    PhonemeScore,
    TimingInfo,
    WordScore,
)
from . import aggregate, gop as gop_mod
from .alignment import align
from .diagnosis import collect_insertions, diagnose, greedy_decode
from .loader import Engine

CAPABILITIES: list[Capability] = [
    Capability.PHONE_ACCURACY,
    Capability.PHONE_DIAGNOSIS,
    Capability.WORD_ACCURACY,
    Capability.WORD_DIAGNOSIS,
    Capability.COMPLETENESS,
    # fluency và prosody CỐ Ý vắng mặt — v1 không đo được, và khai báo trung thực
    # quan trọng hơn danh sách dài (nguyên tắc 1).
]


def _ms(seconds: float) -> int:
    return int(seconds * 1000)


def run(
    eng: Engine,
    waveform: np.ndarray,
    reference_text: str,
    duration_ms: int,
) -> AssessmentResult:
    t_start = time.perf_counter()

    # ── 1. G2P ──────────────────────────────────────────────────────────────────
    utterance = eng.g2p(reference_text)
    canonical = utterance.phonemes
    word_index = utterance.word_index_of_phoneme
    vocab = eng.tokenizer.get_vocab()
    canonical_ids = [vocab[p] for p in canonical]

    # ── 2. Forward, một lần duy nhất ────────────────────────────────────────────
    t0 = time.perf_counter()
    with torch.no_grad():
        logits = eng.model(torch.from_numpy(waveform).unsqueeze(0)).logits[0]
        log_probs = torch.log_softmax(logits, dim=-1)
    t_forward = time.perf_counter() - t0

    # ── 3. GOP ──────────────────────────────────────────────────────────────────
    t0 = time.perf_counter()
    gop_raw = gop_mod.compute(
        log_probs,
        canonical_ids,
        eng.confusion_ids(canonical),
        blank_id=eng.blank_id,
        batch_size=settings.gop_batch_size,
    )
    t_gop = time.perf_counter() - t0

    # ── 4. Chẩn đoán (nhánh độc lập) ────────────────────────────────────────────
    t0 = time.perf_counter()
    runs = greedy_decode(log_probs, eng.id_to_symbol, eng.blank_id)
    decoded = [r.symbol for r in runs]

    def sub_cost(expected: str, said: str) -> float:
        # Cặp nằm trong tập nhầm lẫn của nhau bị phạt nhẹ hơn, để NW giải thích lệch bằng
        # MỘT substitution thay vì omission+insertion — nếu không, Fix Guide chỉ sai chỗ.
        return 0.6 if said in eng.confusion.confuse(expected) else 1.0

    pairs = align(canonical, decoded, sub_cost=sub_cost)
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
    t_diag = time.perf_counter() - t0

    # ── 5. Ghép thành PhonemeScore ──────────────────────────────────────────────
    phonemes: list[PhonemeScore] = []
    per_word_counter: dict[int, int] = {}
    for i, expected in enumerate(canonical):
        diagnosis, said, confidence = verdicts[i]
        w = word_index[i]
        idx_in_word = per_word_counter.get(w, 0)
        per_word_counter[w] = idx_in_word + 1

        # accuracy không có nghĩa cho âm không được phát ra — để None, và aggregate
        # cũng loại nó khỏi phép trung bình. gop_raw vẫn giữ để tính lại sau này.
        accuracy = (
            None
            if diagnosis is PhonemeDiagnosis.OMISSION
            else eng.calibrator.score(gop_raw[i], eng.confusion.group(expected))
        )
        phonemes.append(
            PhonemeScore(
                expected=expected,
                said=said,
                word_index=w,
                phoneme_index=idx_in_word,
                accuracy=accuracy,
                gop_raw=gop_raw[i],
                diagnosis=diagnosis,
                confidence=confidence,
            )
        )

    for symbol, confidence, after in collect_insertions(pairs, runs):
        w = word_index[after] if 0 <= after < len(word_index) else 0
        phonemes.append(
            PhonemeScore(
                expected="",
                said=symbol,
                word_index=w,
                phoneme_index=per_word_counter.get(w, 0),
                accuracy=None,
                gop_raw=0.0,
                diagnosis=PhonemeDiagnosis.INSERTION,
                confidence=confidence,
            )
        )

    # ── 6. Tổng hợp ─────────────────────────────────────────────────────────────
    words: list[WordScore] = []
    for word in utterance.words:
        owned = [p for p in phonemes if p.word_index == word.index]
        words.append(
            WordScore(
                word=word.text,
                word_index=word.index,
                accuracy=aggregate.mean_accuracy(owned),
                diagnosis=aggregate.word_diagnosis([p.diagnosis for p in owned]),
            )
        )

    return AssessmentResult(
        engine=settings.engine_name,
        model_version=eng.model_version,
        g2p_version=eng.g2p_version,
        algorithm_version=(
            f"{settings.algorithm_version}"
            f"+{eng.confusion.version}+{eng.merge_rules.version}"
        ),
        calibration_version=eng.calibrator.version,
        capabilities=CAPABILITIES,
        audio=AudioInfo(duration_ms=duration_ms, sample_rate=settings.sample_rate),
        timing_ms=TimingInfo(
            total=_ms(time.perf_counter() - t_start),
            forward=_ms(t_forward),
            gop=_ms(t_gop),
            diagnosis=_ms(t_diag),
        ),
        overall=OverallScore(
            accuracy=aggregate.mean_accuracy(phonemes),
            fluency=None,  # v1: cần thông tin thời lượng, xem §3.2 bước 6
            completeness=aggregate.completeness(phonemes),
            prosody=None,
        ),
        words=words,
        phonemes=phonemes,
    )
