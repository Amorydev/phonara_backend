"""Tổng hợp phoneme → từ → toàn câu (§3.2 bước 6 và 7).

Thuần logic, test được không cần model.

Hai quyết định đã chốt trong plan, mã hoá ở đây:

  1. `accuracy` LOẠI omission và insertion, GIỮ uncertain.
     · omission: `completeness` đã đo lỗi đó rồi — tính vào cả hai là phạt kép cùng một
       lỗi. Ngoài ra GOP của một âm không được phát ra không mang nghĩa "chính xác đến đâu".
     · uncertain: bất định nằm ở NHÃN, không ở ĐIỂM. GOP vẫn tính được bình thường; loại
       nó đi là để nhánh chẩn đoán can thiệp vào nhánh điểm — nguyên tắc 3 cấm.
     · insertion: không có trong chuỗi chuẩn nên không có `expected` để chấm.

  2. Pool rỗng → `None`, KHÔNG phải 0. Nhờ vậy `accuracy` và `completeness` trực giao:
     phân biệt được "đọc đủ nhưng ngọng" với "bỏ chữ nhưng chỗ nào đọc thì đọc chuẩn".
"""

from __future__ import annotations

from ..schemas import PhonemeDiagnosis, PhonemeScore, WordDiagnosis

# Chẩn đoán bị loại khỏi phép trung bình accuracy
_EXCLUDED_FROM_ACCURACY: frozenset[PhonemeDiagnosis] = frozenset(
    {PhonemeDiagnosis.OMISSION, PhonemeDiagnosis.INSERTION}
)


def mean_accuracy(phonemes: list[PhonemeScore]) -> float | None:
    """Trung bình accuracy trên pool hợp lệ. Pool rỗng → None."""
    pool = [
        p.accuracy
        for p in phonemes
        if p.diagnosis not in _EXCLUDED_FROM_ACCURACY and p.accuracy is not None
    ]
    if not pool:
        return None
    return sum(pool) / len(pool)


def completeness(phonemes: list[PhonemeScore]) -> float | None:
    """(số phoneme chuẩn không bị omission / tổng phoneme chuẩn) × 100.

    Insertion không nằm trong chuỗi chuẩn nên không vào mẫu số.
    """
    canonical = [p for p in phonemes if p.diagnosis != PhonemeDiagnosis.INSERTION]
    if not canonical:
        return None
    spoken = sum(1 for p in canonical if p.diagnosis != PhonemeDiagnosis.OMISSION)
    return 100.0 * spoken / len(canonical)


def word_diagnosis(phoneme_diagnoses: list[PhonemeDiagnosis]) -> WordDiagnosis:
    """§3.2 bước 7 — precedence, điều kiện đầu tiên khớp thì dừng.

    #1 tách khỏi #2 vì "bỏ cả từ" là tín hiệu UX khác "nói từ đó nhưng sai"; gộp chung
    sẽ mất thông tin mà completeness ở mức overall vốn đã tách riêng.
    """
    if not phoneme_diagnoses:
        return WordDiagnosis.UNCERTAIN

    if all(d == PhonemeDiagnosis.OMISSION for d in phoneme_diagnoses):
        return WordDiagnosis.OMISSION

    if any(
        d
        in (
            PhonemeDiagnosis.SUBSTITUTION,
            PhonemeDiagnosis.INSERTION,
            PhonemeDiagnosis.OMISSION,
        )
        for d in phoneme_diagnoses
    ):
        return WordDiagnosis.MISPRONUNCIATION

    if any(d == PhonemeDiagnosis.UNCERTAIN for d in phoneme_diagnoses):
        return WordDiagnosis.UNCERTAIN

    return WordDiagnosis.CORRECT
