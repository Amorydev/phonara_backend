"""Đọc và xác thực audio.

Chỉ nhận **WAV PCM 16-bit, mono, 16 kHz** — đúng thứ Android tạo ra bằng `AudioRecord`
(§5). Ràng buộc chặt này bỏ được ffmpeg/librosa khỏi image, và loại luôn cả một lớp lỗi
"tưởng đã resample nhưng chưa".

Mọi lỗi ở đây là **vĩnh viễn** (4xx), không bao giờ đáng retry.
"""

from __future__ import annotations

import io
import wave

import numpy as np

from .config import settings
from .schemas import ErrorCode


class AudioError(ValueError):
    def __init__(self, code: ErrorCode, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def load_wav(data: bytes) -> tuple[np.ndarray, int]:
    """→ (waveform float32 trong [-1,1], sample_rate)."""
    try:
        with wave.open(io.BytesIO(data), "rb") as wav:
            channels = wav.getnchannels()
            width = wav.getsampwidth()
            rate = wav.getframerate()
            frames = wav.readframes(wav.getnframes())
    except wave.Error as exc:
        raise AudioError(ErrorCode.G2P_FAILED, f"không đọc được WAV: {exc}") from exc

    if width != 2:
        raise AudioError(
            ErrorCode.G2P_FAILED, f"cần PCM 16-bit, nhận {width * 8}-bit"
        )
    if channels != 1:
        raise AudioError(ErrorCode.G2P_FAILED, f"cần mono, nhận {channels} kênh")
    if rate != settings.sample_rate:
        raise AudioError(
            ErrorCode.G2P_FAILED,
            f"cần {settings.sample_rate} Hz, nhận {rate} Hz — "
            "Android phải ghi bằng AudioRecord ở 16 kHz (§5)",
        )

    waveform = np.frombuffer(frames, dtype=np.int16).astype(np.float32) / 32768.0
    return waveform, rate


def validate(waveform: np.ndarray, rate: int) -> int:
    """→ duration_ms. Ném AudioError với mã lỗi tương ứng §2.1."""
    duration_ms = int(1000 * len(waveform) / rate)

    if duration_ms < settings.min_duration_ms:
        raise AudioError(
            ErrorCode.AUDIO_TOO_SHORT,
            f"{duration_ms} ms < tối thiểu {settings.min_duration_ms} ms",
        )
    if duration_ms > settings.max_duration_ms:
        raise AudioError(
            ErrorCode.AUDIO_TOO_LONG,
            f"{duration_ms} ms > tối đa {settings.max_duration_ms} ms",
        )

    rms = float(np.sqrt(np.mean(waveform**2))) if len(waveform) else 0.0
    if rms < settings.silence_rms_threshold:
        # Chặn ở đây thay vì để model chấm một bản ghi im lặng thành "sai toàn bộ" —
        # đó là khác biệt giữa "bạn phát âm sai" và "chúng tôi không nghe thấy gì".
        raise AudioError(
            ErrorCode.NO_SPEECH_DETECTED,
            f"RMS {rms:.2e} dưới ngưỡng {settings.silence_rms_threshold:.2e}",
        )

    return duration_ms
