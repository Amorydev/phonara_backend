"""Cấu hình runtime. Mọi giá trị đọc từ env để đổi được mà không build lại image."""

from __future__ import annotations

import os
from pathlib import Path

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

_ROOT = Path(__file__).resolve().parents[1]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="PE_", protected_namespaces=(), extra="ignore"
    )

    # ── model ───────────────────────────────────────────────────────────────────
    model_id: str = "facebook/wav2vec2-xlsr-53-espeak-cv-ft"
    # Pin theo commit SHA, không chỉ tên (§3.4). Xác nhận bởi R1.
    model_revision: str = "2c733782da5604684829819a5eb744c193fe9398"

    # ── phiên bản, đi kèm mọi kết quả (§8) ──────────────────────────────────────
    engine_name: str = "ctc_alignment_free"
    algorithm_version: str = "gop-af-1.0.0"

    # ── artifact ────────────────────────────────────────────────────────────────
    confusion_path: Path = _ROOT / "artifacts" / "confusion.json"
    calibration_path: Path = _ROOT / "artifacts" / "calibration.json"
    merge_rules_path: Path = _ROOT / "artifacts" / "merge_rules.json"

    # ── audio (§2.1 mã lỗi) ─────────────────────────────────────────────────────
    sample_rate: int = 16_000
    min_duration_ms: int = 300
    max_duration_ms: int = 30_000
    max_audio_bytes: int = 2 * 1024 * 1024
    max_reference_chars: int = 1_000
    silence_rms_threshold: float = 1e-4

    # ── serving (§3.4) ──────────────────────────────────────────────────────────
    max_concurrent_inference: int = 2
    torch_threads: int = Field(
        default=0, description="0 = tự tính theo (số core / max_concurrent_inference)"
    )
    gop_batch_size: int = Field(
        default=64, description="Chặn bộ nhớ khi câu dài sinh nhiều chuỗi nhiễu"
    )

    def resolved_torch_threads(self) -> int:
        if self.torch_threads > 0:
            return self.torch_threads
        cores = os.cpu_count() or 2
        # Chia theo semaphore để hai request đồng thời không tranh CPU của nhau (§3.4)
        return max(1, cores // max(1, self.max_concurrent_inference))


settings = Settings()
