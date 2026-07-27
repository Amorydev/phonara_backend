"""Contract §2.1 — NormalizedAssessmentResult.

Đây là hợp đồng ba bên (Python ↔ Go ↔ Android). Mọi thay đổi ở đây là breaking change.

Nguyên tắc 1 & 2 của plan được mã hoá thẳng vào kiểu dữ liệu:
  · trường không đo được là `None`, không phải 0 → mọi Optional đều cố ý
  · `said` là `str | None`; `None` nghĩa là *chưa xác định*, không phải omission
"""

from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field

# Contract §2.1 dùng `model_version`, đụng namespace bảo lưu `model_` của pydantic.
# Tên trường thuộc hợp đồng ba bên nên không đổi được — tắt cảnh báo thay vì đổi tên.
_CONTRACT = ConfigDict(protected_namespaces=())


class PhonemeDiagnosis(StrEnum):
    CORRECT = "correct"
    SUBSTITUTION = "substitution"
    OMISSION = "omission"
    INSERTION = "insertion"
    UNCERTAIN = "uncertain"


class WordDiagnosis(StrEnum):
    CORRECT = "correct"
    MISPRONUNCIATION = "mispronunciation"
    OMISSION = "omission"
    UNCERTAIN = "uncertain"


class Capability(StrEnum):
    """Engine khai báo nó đo được gì — nguyên tắc 1.

    Go backend dùng danh sách này để phân biệt "trường vắng mặt" với "trường bằng 0".
    Không có nó, mọi thống kê trộn nhiều engine đều sai âm thầm.
    """

    PHONE_ACCURACY = "phone_accuracy"
    PHONE_DIAGNOSIS = "phone_diagnosis"
    WORD_ACCURACY = "word_accuracy"
    WORD_DIAGNOSIS = "word_diagnosis"
    COMPLETENESS = "completeness"
    FLUENCY = "fluency"
    PROSODY = "prosody"


class PhonemeScore(BaseModel):
    expected: str
    said: str | None = Field(
        default=None,
        description="Âm thực sự nghe được. None = chưa xác định, KHÔNG phải omission.",
    )
    word_index: int
    phoneme_index: int
    accuracy: float | None = Field(
        default=None, description="0–100 sau calibration. None khi không tính được."
    )
    gop_raw: float = Field(
        description="GOP thô chưa calibrate, miền (−∞,+∞). Cho phép tính lại điểm "
        "khi đổi calibration mà không cần chạy lại inference (§3.3)."
    )
    diagnosis: PhonemeDiagnosis
    confidence: float = Field(ge=0.0, le=1.0)


class WordScore(BaseModel):
    word: str
    word_index: int
    accuracy: float | None = None
    diagnosis: WordDiagnosis


class OverallScore(BaseModel):
    accuracy: float | None = Field(
        default=None,
        description="Trung bình accuracy các phoneme KHÔNG phải omission/insertion. "
        "None khi pool rỗng (đọc sót toàn bộ) — không phải 0.",
    )
    fluency: float | None = None
    completeness: float | None = None
    prosody: float | None = None


class AudioInfo(BaseModel):
    duration_ms: int
    sample_rate: int


class TimingInfo(BaseModel):
    total: int
    forward: int
    gop: int
    diagnosis: int


class AssessmentResult(BaseModel):
    model_config = _CONTRACT
    engine: str
    model_version: str
    g2p_version: str
    algorithm_version: str
    calibration_version: str
    capabilities: list[Capability]
    audio: AudioInfo
    timing_ms: TimingInfo
    overall: OverallScore
    words: list[WordScore]
    phonemes: list[PhonemeScore]


class ErrorCode(StrEnum):
    """§2.1 — worker cần biết lỗi nào đáng retry.

    4xx = vĩnh viễn, không bao giờ retry. 5xx = tạm thời, retry được.
    """

    AUDIO_TOO_SHORT = "audio_too_short"
    AUDIO_TOO_LONG = "audio_too_long"
    NO_SPEECH_DETECTED = "no_speech_detected"
    G2P_FAILED = "g2p_failed"
    MODEL_OVERLOADED = "model_overloaded"
    INTERNAL = "internal"


RETRYABLE: frozenset[ErrorCode] = frozenset(
    {ErrorCode.MODEL_OVERLOADED, ErrorCode.INTERNAL}
)

HTTP_STATUS: dict[ErrorCode, int] = {
    ErrorCode.AUDIO_TOO_SHORT: 422,
    ErrorCode.AUDIO_TOO_LONG: 422,
    ErrorCode.NO_SPEECH_DETECTED: 422,
    ErrorCode.G2P_FAILED: 422,
    ErrorCode.MODEL_OVERLOADED: 503,
    ErrorCode.INTERNAL: 500,
}


class ErrorResponse(BaseModel):
    code: ErrorCode
    message: str
    retryable: bool


class HealthResponse(BaseModel):
    model_config = _CONTRACT
    status: str
    model_loaded: bool
    warmed_up: bool
    model_version: str
    g2p_version: str
    algorithm_version: str
    calibration_version: str
