import json
from pathlib import Path

import pytest

from app.engine import calibrate

ARTIFACT = Path(__file__).resolve().parents[1] / "artifacts" / "calibration.json"


@pytest.fixture(scope="module")
def cal():
    return calibrate.load(ARTIFACT)


def test_no_clipping_of_positive_gop(cal):
    """TEST BẮT BUỘC (§3.5).

    GOP alignment-free KHÔNG bị chặn trên bởi 0 — tập nhiễu loại chính pᵢ nên GOP dương
    là tín hiệu phát âm tốt, không phải trường hợp biên. Nếu ai đó clip đầu vào theo miền
    `(−∞,0]` của GOP cổ điển, điểm của đúng những ca phát âm TỐT NHẤT sẽ bị bóp về một
    giá trị chung — và hỏng im lặng.

    Test này bắt đúng kịch bản đó: điểm phải tiếp tục tăng đơn điệu khi GOP dương lớn dần.
    """
    scores = [cal.score(g, "fricative") for g in (0.0, 0.5, 1.0, 2.0, 5.0, 10.0)]
    assert scores == sorted(scores), "điểm phải đơn điệu tăng theo GOP"
    assert len(set(scores)) == len(scores), "có dấu hiệu clip: nhiều GOP dương cho cùng điểm"
    assert scores[-1] > scores[0], "GOP dương lớn phải cho điểm cao hơn GOP = 0"


def test_negative_gop_is_accepted(cal):
    for g in (-0.5, -3.0, -50.0):
        assert 0.0 <= cal.score(g, "vowel") <= 100.0


def test_extreme_values_do_not_overflow(cal):
    assert cal.score(-1e9, "stop") == pytest.approx(0.0, abs=1e-6)
    assert cal.score(1e9, "stop") == pytest.approx(100.0, abs=1e-6)


def test_output_always_in_range(cal):
    for g in (-1e6, -10.0, 0.0, 10.0, 1e6):
        for group in ("stop", "fricative", "vowel", "other", "khong-ton-tai"):
            assert 0.0 <= cal.score(g, group) <= 100.0


def test_unknown_group_falls_back_to_other(cal):
    assert cal.score(1.23, "nhom-la") == cal.score(1.23, "other")


def test_gop_zero_is_midpoint_with_default_params(cal):
    # a=1, b=0 → sigmoid(0) = 0.5. Ranh giới "chuỗi chuẩn ngang bằng phương án nhiễu".
    assert cal.score(0.0, "vowel") == pytest.approx(50.0)


def test_missing_other_group_is_rejected(tmp_path):
    bad = tmp_path / "bad.json"
    bad.write_text(
        json.dumps({"version": "x", "formula": "logistic", "groups": {"vowel": {"a": 1, "b": 0}}}),
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="other"):
        calibrate.load(bad)


def test_placeholder_version_is_flagged(cal):
    """Bản calibration hiện tại chưa fit trên dữ liệu nào.

    Test này CỐ Ý fail khi ai đó bỏ hậu tố PLACEHOLDER mà chưa fit thật — đổi nó thành
    assert ngược lại sau khi hoàn thành giai đoạn 4.
    """
    assert "PLACEHOLDER" in cal.version, (
        "Nếu đã fit calibration thật thì cập nhật test này. "
        "Nếu chưa, đừng bỏ hậu tố PLACEHOLDER."
    )
