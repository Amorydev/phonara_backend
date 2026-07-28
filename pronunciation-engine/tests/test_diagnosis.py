"""Quy tắc hợp nhất — trọng tâm là quy tắc 5 (`uncertain` + vắng mặt → omission).

Dựng log_probs bằng tay thay vì chạy model: quy tắc hợp nhất là logic thuần, và test nó
qua model thật sẽ biến một lỗi luật thành một lỗi "model đoán khác hôm nay".
"""

from __future__ import annotations

import pytest
import torch

from app.engine.alignment import AlignedPair, Op
from app.engine.diagnosis import DecodedRun, MergeRules, _presence, diagnose
from app.schemas import PhonemeDiagnosis

BLANK = 0
# vocab giả: 0=blank, 1=θ, 2=t, 3=s
N_VOCAB = 4

RULES = MergeRules(
    version="test",
    tau_uncertain=0.5,
    tau_gop_low=-1.0,
    tau_gop_high=1.0,
    tau_absent=0.01,
)


def log_probs_from(rows: list[dict[int, float]]) -> torch.Tensor:
    """Mỗi hàng là {token_id: xác suất}; phần còn lại chia đều cho các token khác."""
    probs = torch.zeros(len(rows), N_VOCAB)
    for t, row in enumerate(rows):
        for token_id, p in row.items():
            probs[t, token_id] = p
        remaining = 1.0 - sum(row.values())
        blanks = [i for i in range(N_VOCAB) if i not in row]
        for i in blanks:
            probs[t, i] = remaining / len(blanks)
    return probs.log()


def test_presence_uses_max_not_mean():
    # Âm chỉ xuất hiện rõ ở MỘT frame trong vùng ba frame. `mean` sẽ pha loãng nó xuống
    # ~0,3 và làm đại lượng phụ thuộc độ dài vùng thay vì phụ thuộc âm thanh.
    lp = log_probs_from([{1: 0.01}, {1: 0.9}, {1: 0.01}])
    assert _presence(lp, 1, 0, 2) == pytest.approx(0.9, abs=1e-6)


def test_presence_clamps_out_of_range_frames():
    lp = log_probs_from([{1: 0.4}, {1: 0.6}])
    assert _presence(lp, 1, -5, 99) == pytest.approx(0.6, abs=1e-6)


def test_uncertain_becomes_omission_when_phoneme_absent_from_posterior():
    """Quy tắc 5 — nhánh mà 79/204 ca bỏ sót omission rơi vào."""
    # NW căn θ vào một run yếu (conf 0,3 < tau_uncertain) và posterior của θ gần như 0.
    lp = log_probs_from([{2: 0.6, 1: 0.001}, {2: 0.6, 1: 0.001}])
    pairs = [AlignedPair(op=Op.MATCH, canon_index=0, decoded_index=0)]
    runs = [DecodedRun(token_id=2, symbol="t", start_frame=0, end_frame=1, confidence=0.3)]

    out = diagnose(pairs, ["θ"], [1], runs, [0.0], lp, BLANK, RULES)

    assert out[0][0] is PhonemeDiagnosis.OMISSION
    assert out[0][1] is None
    assert out[0][2] > 0.99  # confidence = 1 − presence


def test_uncertain_stays_uncertain_when_phoneme_present_in_posterior():
    """Đối trọng: âm có mặt trong posterior thì 'không rõ' vẫn là 'không rõ'.

    Không có test này thì quy tắc 5 có thể biến MỌI `uncertain` thành omission mà vẫn
    xanh — và đó chính là cách precision tụt.
    """
    lp = log_probs_from([{2: 0.5, 1: 0.35}, {2: 0.5, 1: 0.35}])
    pairs = [AlignedPair(op=Op.MATCH, canon_index=0, decoded_index=0)]
    runs = [DecodedRun(token_id=2, symbol="t", start_frame=0, end_frame=1, confidence=0.3)]

    out = diagnose(pairs, ["θ"], [1], runs, [0.0], lp, BLANK, RULES)

    assert out[0][0] is PhonemeDiagnosis.UNCERTAIN


def test_confident_correct_is_never_downgraded_to_omission():
    """Quy tắc 5 chỉ được chạm vào `uncertain`.

    Âm đã tự tin đúng mà bị đổi thành omission là kiểu lỗi tệ nhất với người học: báo
    "bạn nuốt âm" đúng lúc họ vừa phát âm chuẩn.
    """
    lp = log_probs_from([{1: 0.95}, {1: 0.95}])
    pairs = [AlignedPair(op=Op.MATCH, canon_index=0, decoded_index=0)]
    runs = [DecodedRun(token_id=1, symbol="θ", start_frame=0, end_frame=1, confidence=0.95)]

    out = diagnose(pairs, ["θ"], [1], runs, [0.0], lp, BLANK, RULES)

    assert out[0][0] is PhonemeDiagnosis.CORRECT


def test_nw_gap_omission_is_unaffected_by_presence():
    """Quy tắc 1 giữ nguyên: NW đã báo vắng mặt thì không kiểm confidence, cũng không
    kiểm presence. Chiều 'hạ omission có presence cao' đã thử và bỏ (§3.2.1)."""
    lp = log_probs_from([{1: 0.9}, {1: 0.9}])
    pairs = [AlignedPair(op=Op.OMISSION, canon_index=0, decoded_index=None)]

    out = diagnose(pairs, ["θ"], [1], [], [0.0], lp, BLANK, RULES)

    assert out[0][0] is PhonemeDiagnosis.OMISSION


def test_substitution_survives_when_confident():
    lp = log_probs_from([{2: 0.9, 1: 0.02}, {2: 0.9, 1: 0.02}])
    pairs = [AlignedPair(op=Op.SUBSTITUTION, canon_index=0, decoded_index=0)]
    runs = [DecodedRun(token_id=2, symbol="t", start_frame=0, end_frame=1, confidence=0.9)]

    out = diagnose(pairs, ["θ"], [1], runs, [-2.0], lp, BLANK, RULES)

    assert out[0][0] is PhonemeDiagnosis.SUBSTITUTION
    assert out[0][1] == "t"
