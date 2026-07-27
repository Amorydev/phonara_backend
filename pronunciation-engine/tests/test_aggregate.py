import pytest

from app.engine.aggregate import completeness, mean_accuracy, word_diagnosis
from app.schemas import PhonemeDiagnosis as D
from app.schemas import PhonemeScore, WordDiagnosis as W


def ph(diagnosis: D, accuracy: float | None = 50.0, expected: str = "t") -> PhonemeScore:
    return PhonemeScore(
        expected=expected,
        said=None,
        word_index=0,
        phoneme_index=0,
        accuracy=accuracy,
        gop_raw=0.0,
        diagnosis=diagnosis,
        confidence=0.9,
    )


# ── mean_accuracy ───────────────────────────────────────────────────────────────


def test_omission_excluded_from_accuracy():
    """completeness đã phạt omission rồi — tính vào accuracy nữa là phạt kép."""
    got = mean_accuracy([ph(D.CORRECT, 90.0), ph(D.OMISSION, 5.0)])
    assert got == pytest.approx(90.0)


def test_insertion_excluded_from_accuracy():
    got = mean_accuracy([ph(D.CORRECT, 80.0), ph(D.INSERTION, 10.0)])
    assert got == pytest.approx(80.0)


def test_uncertain_included_in_accuracy():
    """Bất định nằm ở NHÃN, không ở ĐIỂM — nguyên tắc 3."""
    got = mean_accuracy([ph(D.CORRECT, 100.0), ph(D.UNCERTAIN, 50.0)])
    assert got == pytest.approx(75.0)


def test_empty_pool_returns_none_not_zero():
    """Đọc sót toàn bộ → accuracy None. Trả 0 sẽ nói dối rằng đã chấm và bị điểm liệt."""
    assert mean_accuracy([ph(D.OMISSION), ph(D.OMISSION)]) is None


def test_no_phonemes_at_all_returns_none():
    assert mean_accuracy([]) is None


# ── completeness ────────────────────────────────────────────────────────────────


def test_completeness_counts_only_canonical_phonemes():
    # 3 chuẩn (1 bị sót) + 1 insertion → insertion không vào mẫu số
    got = completeness([ph(D.CORRECT), ph(D.SUBSTITUTION), ph(D.OMISSION), ph(D.INSERTION)])
    assert got == pytest.approx(100 * 2 / 3)


def test_completeness_full_when_nothing_omitted():
    assert completeness([ph(D.CORRECT), ph(D.UNCERTAIN)]) == pytest.approx(100.0)


def test_completeness_zero_when_all_omitted():
    assert completeness([ph(D.OMISSION), ph(D.OMISSION)]) == pytest.approx(0.0)


def test_accuracy_and_completeness_are_orthogonal():
    """Đọc đủ nhưng ngọng ≠ bỏ chữ nhưng chỗ nào đọc thì chuẩn."""
    sloppy = [ph(D.SUBSTITUTION, 30.0), ph(D.SUBSTITUTION, 30.0)]
    skipper = [ph(D.CORRECT, 95.0), ph(D.OMISSION, 0.0)]

    assert completeness(sloppy) == pytest.approx(100.0)
    assert mean_accuracy(sloppy) == pytest.approx(30.0)

    assert completeness(skipper) == pytest.approx(50.0)
    assert mean_accuracy(skipper) == pytest.approx(95.0)


# ── word_diagnosis (§3.2 bước 7) ────────────────────────────────────────────────


def test_all_omission_is_word_omission():
    assert word_diagnosis([D.OMISSION, D.OMISSION]) is W.OMISSION


def test_partial_omission_is_mispronunciation_not_omission():
    """Bỏ CẢ TỪ là tín hiệu UX khác nói-từ-đó-nhưng-sai — không được gộp."""
    assert word_diagnosis([D.CORRECT, D.OMISSION]) is W.MISPRONUNCIATION


def test_any_substitution_is_mispronunciation():
    assert word_diagnosis([D.CORRECT, D.SUBSTITUTION, D.CORRECT]) is W.MISPRONUNCIATION


def test_insertion_is_mispronunciation():
    assert word_diagnosis([D.CORRECT, D.INSERTION]) is W.MISPRONUNCIATION


def test_substitution_beats_uncertain():
    assert word_diagnosis([D.UNCERTAIN, D.SUBSTITUTION]) is W.MISPRONUNCIATION


def test_uncertain_only_when_no_hard_error():
    assert word_diagnosis([D.CORRECT, D.UNCERTAIN]) is W.UNCERTAIN


def test_all_correct():
    assert word_diagnosis([D.CORRECT, D.CORRECT]) is W.CORRECT


def test_empty_word_is_uncertain():
    assert word_diagnosis([]) is W.UNCERTAIN
