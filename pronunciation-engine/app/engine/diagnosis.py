"""Nhánh chẩn đoán: CTC decode → Needleman-Wunsch → hợp nhất với GOP (§3.2 bước 4–5).

Nhánh này độc lập hoàn toàn với nhánh điểm số (nguyên tắc 3). Nó trả lời "người học thực
sự nói gì", không phải "tốt đến đâu".

`argmin perturbation` của GOP KHÔNG được dùng làm bằng chứng cho `said` — nó chỉ nói
"chuỗi thay thế khớp âm thanh hơn chuỗi chuẩn", không nói người học đã phát âm phoneme đó.
Bằng chứng cho `said` đến từ CTC decode, là chuỗi thật sự được nhận dạng.
"""

from __future__ import annotations

from dataclasses import dataclass

import torch

from ..schemas import PhonemeDiagnosis
from .alignment import AlignedPair, Op


@dataclass(frozen=True, slots=True)
class DecodedRun:
    token_id: int
    symbol: str
    start_frame: int
    end_frame: int  # inclusive
    confidence: float


@dataclass(frozen=True, slots=True)
class MergeRules:
    version: str
    tau_uncertain: float
    tau_gop_low: float
    tau_gop_high: float
    # Ngưỡng "vắng mặt" cho quy tắc 5. Xem `_presence` và §3.2 bước 5.
    tau_absent: float


def greedy_decode(
    log_probs: torch.Tensor,  # [T, V]
    id_to_symbol: dict[int, str],
    blank_id: int,
) -> list[DecodedRun]:
    """CTC greedy decode, giữ lại vùng frame của từng phoneme.

    Vùng frame cần cho hai việc: tính confidence (trung bình posterior trên vùng đó) và
    định vị vùng blank cho omission.
    """
    probs = log_probs.exp()
    best_ids = log_probs.argmax(dim=-1).tolist()

    runs: list[DecodedRun] = []
    prev_id: int | None = None
    start = 0

    def flush(token_id: int, s: int, e: int) -> None:
        if token_id == blank_id:
            return
        conf = float(probs[s : e + 1, token_id].mean())
        runs.append(
            DecodedRun(
                token_id=token_id,
                symbol=id_to_symbol.get(token_id, "?"),
                start_frame=s,
                end_frame=e,
                confidence=conf,
            )
        )

    for t, token_id in enumerate(best_ids):
        if prev_id is None:
            prev_id, start = token_id, t
            continue
        if token_id != prev_id:
            flush(prev_id, start, t - 1)
            prev_id, start = token_id, t
    if prev_id is not None:
        flush(prev_id, start, len(best_ids) - 1)

    return runs


def _blank_confidence(
    log_probs: torch.Tensor,
    blank_id: int,
    lo: int,
    hi: int,
) -> float:
    """Posterior blank trung bình trên [lo, hi].

    Đây là confidence cho `omission`: không có phoneme nào được decode ở đó để lấy
    posterior, nên ta đo độ chắc chắn của "không có gì được nói ở đây". Blank cao là
    bằng chứng TỐT cho omission, không phải bất định.
    """
    n_frames = log_probs.size(0)
    lo = max(0, min(lo, n_frames - 1))
    hi = max(lo, min(hi, n_frames - 1))
    return float(log_probs[lo : hi + 1, blank_id].exp().mean())


def _presence(log_probs: torch.Tensor, token_id: int, lo: int, hi: int) -> float:
    """Posterior LỚN NHẤT của một phoneme trên vùng [lo, hi] — bằng chứng "có mặt".

    Greedy decode là argmax cứng: nó chỉ giữ lại phoneme thắng ở mỗi frame và vứt bỏ toàn
    bộ phân bố còn lại. Nhưng câu hỏi "người học có phát ra âm này không" cần đúng phần bị
    vứt: một âm nói yếu vẫn để lại khối xác suất đáng kể dù không bao giờ thắng argmax.

    Lấy `max` chứ không `mean`: một phoneme chỉ chiếm vài frame trong vùng được cấp, nên
    trung bình sẽ bị pha loãng theo độ dài vùng — vùng càng rộng điểm càng thấp, tức đại
    lượng sẽ phụ thuộc vào chuyện căn chuỗi thay vì vào âm thanh.
    """
    n_frames = log_probs.size(0)
    lo = max(0, min(lo, n_frames - 1))
    hi = max(lo, min(hi, n_frames - 1))
    return float(log_probs[lo : hi + 1, token_id].exp().max())


def diagnose(
    pairs: list[AlignedPair],
    canonical: list[str],
    canonical_ids: list[int],
    runs: list[DecodedRun],
    gop: list[float],
    log_probs: torch.Tensor,
    blank_id: int,
    rules: MergeRules,
) -> list[tuple[PhonemeDiagnosis, str | None, float]]:
    """→ (diagnosis, said, confidence) cho từng phoneme chuẩn, cùng thứ tự `canonical`.

    Insertion không nằm trong chuỗi chuẩn nên không xuất hiện ở đây; caller lấy riêng
    qua `collect_insertions`.
    """
    out: list[tuple[PhonemeDiagnosis, str | None, float] | None] = [None] * len(canonical)

    # frame của phoneme chuẩn đã được căn — dùng để định vị vùng blank cho omission
    frame_of: dict[int, tuple[int, int]] = {}
    for p in pairs:
        if p.canon_index is not None and p.decoded_index is not None:
            run = runs[p.decoded_index]
            frame_of[p.canon_index] = (run.start_frame, run.end_frame)

    for pair in pairs:
        i = pair.canon_index
        if i is None:
            continue  # insertion

        if pair.op is Op.OMISSION:
            prev_end = max(
                (frame_of[j][1] for j in range(i) if j in frame_of), default=-1
            )
            next_start = min(
                (frame_of[j][0] for j in range(i + 1, len(canonical)) if j in frame_of),
                default=log_probs.size(0),
            )
            lo, hi = prev_end + 1, next_start - 1
            if lo > hi:  # hai âm liền kề, không còn khoảng trống
                lo = hi = max(0, min(prev_end, log_probs.size(0) - 1))
            conf = _blank_confidence(log_probs, blank_id, lo, hi)
            # Quy tắc 1 (§3.2 bước 5): omission MIỄN TRỪ kiểm tra confidence.
            # Bằng chứng nằm ở sự vắng mặt trong chuỗi decode, không ở độ tự tin của một
            # decode không tồn tại. Không loại trừ, omission thật sẽ bị hệ thống hoá thành
            # uncertain — làm yếu đúng thứ nhánh này sinh ra để phát hiện.
            out[i] = (PhonemeDiagnosis.OMISSION, None, conf)
            continue

        run = runs[pair.decoded_index]  # type: ignore[index]
        conf = run.confidence

        if conf < rules.tau_uncertain:
            out[i] = (PhonemeDiagnosis.UNCERTAIN, None, conf)
            continue

        if pair.op is Op.MATCH:
            # Quy tắc 3: âm đúng nhưng GOP thấp → vẫn `correct`, điểm tự nó đã thấp
            out[i] = (PhonemeDiagnosis.CORRECT, canonical[i], conf)
        else:  # substitution
            # Quy tắc 4: GOP cao mà align báo lệch → nhiều khả năng lỗi decode
            if gop[i] > rules.tau_gop_high:
                out[i] = (PhonemeDiagnosis.UNCERTAIN, None, conf)
            else:
                out[i] = (PhonemeDiagnosis.SUBSTITUTION, run.symbol, conf)

    verdicts = [o or (PhonemeDiagnosis.UNCERTAIN, None, 0.0) for o in out]

    # ── Quy tắc 5: `uncertain` + vắng mặt khỏi posterior → omission ──────────────
    #
    # Quy tắc 1 miễn trừ omission khỏi kiểm tra confidence, nhưng chỉ khi NW ĐÃ quyết định
    # đó là omission. Khi NW căn được âm chuẩn vào một run yếu, confidence thấp đẩy nó
    # thành `uncertain` — và người học nhận về "không rõ" thay vì "bạn nuốt mất âm này".
    #
    # Đo trên L2-ARCTIC (150 câu người Việt): trong 204 ca bỏ sót omission, **79 ca rơi
    # đúng vào nhánh này** — nhóm lớn nhất. Confidence trung bình khi bỏ sót là 0,588 so
    # với 0,942 khi bắt được.
    #
    # Quy tắc này mở rộng đúng nguyên tắc của quy tắc 1 chứ không phá nó: bằng chứng cho
    # omission là sự VẮNG MẶT, và posterior là thước đo vắng mặt tốt hơn chuỗi greedy
    # decode — vắng khỏi argmax chỉ nghĩa là thua một phoneme khác, còn vắng khỏi posterior
    # nghĩa là model không thấy âm đó ở đâu cả.
    #
    # Chiều ngược lại (hạ omission có presence cao xuống uncertain) đã thử và BỎ: mọi
    # ngưỡng đều làm cả precision lẫn recall tệ đi. Xem §3.2.1.
    for i, (diagnosis, _said, _conf) in enumerate(verdicts):
        if diagnosis is not PhonemeDiagnosis.UNCERTAIN:
            continue
        lo, hi = frame_of.get(i, (0, log_probs.size(0) - 1))
        presence = _presence(log_probs, canonical_ids[i], lo, hi)
        if presence < rules.tau_absent:
            # confidence = khối xác suất KHÔNG nằm trên âm mong đợi. Cùng ngữ nghĩa "độ
            # chắc chắn rằng âm này vắng mặt" với `_blank_confidence` ở nhánh trên, nhưng
            # đo bằng chính phoneme thay vì bằng blank — ở đây vùng frame không im lặng,
            # nó chứa một âm khác, nên blank sẽ báo thấp một cách sai lệch.
            verdicts[i] = (PhonemeDiagnosis.OMISSION, None, 1.0 - presence)

    return verdicts


def collect_insertions(
    pairs: list[AlignedPair], runs: list[DecodedRun]
) -> list[tuple[str, float, int]]:
    """→ (symbol, confidence, vị trí chuẩn đứng trước) cho từng âm thừa."""
    result: list[tuple[str, float, int]] = []
    last_canon = -1
    for pair in pairs:
        if pair.canon_index is not None:
            last_canon = pair.canon_index
        elif pair.decoded_index is not None:
            run = runs[pair.decoded_index]
            result.append((run.symbol, run.confidence, last_canon))
    return result
