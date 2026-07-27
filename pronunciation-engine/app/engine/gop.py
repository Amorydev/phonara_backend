"""GOP alignment-free bằng CTC (§3.2 bước 3).

    L(S)  = log P(S | audio)                     # −CTC loss
    GOPᵢ  = L(P) − max L(P với pᵢ bị nhiễu)

    nhiễu tại vị trí i (KHÔNG bao gồm chính pᵢ):
      · thay pᵢ bằng từng c ∈ Confuse(pᵢ)
      · xóa pᵢ

MIỀN GIÁ TRỊ LÀ TOÀN TRỤC THỰC — không bị chặn trên bởi 0.

Vì tập nhiễu loại chính pᵢ, không có gì buộc `max L(perturbed) ≥ L(P)`. `GOPᵢ > 0` nghĩa
là chuỗi chuẩn khớp âm thanh hơn *mọi* phương án nhiễu đã thử → phát âm tốt. Đây là tín
hiệu mong muốn, không phải trường hợp biên. (Khác GOP cổ điển Witt & Young, nơi max lấy
trên toàn bộ tập phone kể cả phone đúng nên miền luôn `(−∞,0]`.)

Bảng nhầm lẫn giới hạn giữ số phép tính ở ~5n thay vì ~Vn với V=392. Phần đắt là forward
pass, đã chạy một lần trước khi vào đây.
"""

from __future__ import annotations

import torch
import torch.nn.functional as F


def _batched_loglik(
    log_probs: torch.Tensor,  # [T, V], đã log_softmax
    sequences: list[list[int]],
    blank_id: int,
    batch_size: int,
) -> torch.Tensor:
    """log P(S | audio) cho từng chuỗi. → [len(sequences)]"""
    if not sequences:
        return torch.empty(0)

    n_frames = log_probs.size(0)
    out: list[torch.Tensor] = []

    for start in range(0, len(sequences), batch_size):
        chunk = sequences[start : start + batch_size]
        n = len(chunk)
        max_len = max((len(s) for s in chunk), default=0)

        targets = torch.zeros((n, max(max_len, 1)), dtype=torch.long)
        target_lengths = torch.zeros(n, dtype=torch.long)
        for i, seq in enumerate(chunk):
            if seq:
                targets[i, : len(seq)] = torch.tensor(seq, dtype=torch.long)
            target_lengths[i] = len(seq)

        # expand là view, không copy — [T, V] → [T, N, V]
        batched = log_probs.unsqueeze(1).expand(n_frames, n, log_probs.size(1))
        input_lengths = torch.full((n,), n_frames, dtype=torch.long)

        # reduction='none' trả NLL thô cho từng mẫu → log-likelihood = −NLL
        nll = F.ctc_loss(
            batched,
            targets,
            input_lengths,
            target_lengths,
            blank=blank_id,
            reduction="none",
            zero_infinity=True,
        )
        out.append(-nll)

    return torch.cat(out)


def compute(
    log_probs: torch.Tensor,
    canonical_ids: list[int],
    confusion_ids: list[list[int]],
    blank_id: int,
    batch_size: int = 64,
) -> list[float]:
    """→ GOP thô cho từng phoneme trong `canonical_ids`.

    `confusion_ids[i]` là các id ứng viên thay thế cho vị trí i.
    """
    n = len(canonical_ids)
    if n == 0:
        return []

    perturbed: list[list[int]] = []
    owner: list[int] = []  # perturbed[k] thuộc vị trí owner[k]

    for i in range(n):
        for cand in confusion_ids[i]:
            if cand == canonical_ids[i]:
                continue  # loại chính pᵢ — đây là điều làm miền GOP thành toàn trục thực
            seq = list(canonical_ids)
            seq[i] = cand
            perturbed.append(seq)
            owner.append(i)
        perturbed.append(canonical_ids[:i] + canonical_ids[i + 1 :])  # xóa
        owner.append(i)

    with torch.no_grad():
        canonical_ll = _batched_loglik(log_probs, [canonical_ids], blank_id, batch_size)[0]
        perturbed_ll = _batched_loglik(log_probs, perturbed, blank_id, batch_size)

    best = [float("-inf")] * n
    for k, pos in enumerate(owner):
        value = float(perturbed_ll[k])
        if value > best[pos]:
            best[pos] = value

    base = float(canonical_ll)
    # Vị trí không có phương án nhiễu nào (không nên xảy ra: luôn có phương án xóa) → 0.0
    return [base - b if b != float("-inf") else 0.0 for b in best]
