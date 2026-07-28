"""FastAPI — /health và /v1/assess (§3.4).

Service này KHÔNG xác thực, KHÔNG rate-limit, KHÔNG chạm DB. Nó là hàm thuần:
audio + text → kết quả. Go backend là biên giới tin cậy duy nhất (nguyên tắc 6), và
container này chỉ nghe trên mạng nội bộ Docker.
"""

from __future__ import annotations

import asyncio
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, File, Form, UploadFile
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool

from .audio import AudioError, load_wav, validate
from .config import settings
from .engine import assess, loader
from .engine.g2p import G2PError
from .schemas import (
    HTTP_STATUS,
    RETRYABLE,
    AssessmentResult,
    ErrorCode,
    ErrorResponse,
    HealthResponse,
)

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s %(levelname)-7s %(name)s | %(message)s"
)
log = logging.getLogger("pronunciation-engine")

# Giới hạn inference đồng thời. CHỈ có ý nghĩa vì inference chạy trong threadpool —
# một lời gọi PyTorch blocking đặt thẳng trong `async def` sẽ chặn event loop và khi đó
# không có đồng thời thật để mà giới hạn (§3.4).
_slots = asyncio.Semaphore(settings.max_concurrent_inference)


@asynccontextmanager
async def lifespan(_: FastAPI):
    loader.load()
    await run_in_threadpool(loader.warm_up)
    yield


app = FastAPI(title="Phonara Pronunciation Engine", version="1.0.0", lifespan=lifespan)


def _error(code: ErrorCode, message: str) -> JSONResponse:
    return JSONResponse(
        status_code=HTTP_STATUS[code],
        content=ErrorResponse(
            code=code, message=message, retryable=code in RETRYABLE
        ).model_dump(),
    )


@app.get("/health", response_model=HealthResponse)
async def health() -> JSONResponse:
    """`ok` chỉ khi model đã nạp VÀ warm-up xong (§3.4).

    Trả ok sớm hơn sẽ khiến orchestrator đẩy traffic vào lúc request đầu còn chậm bất
    thường, và trông như service lỗi.
    """
    loaded = loader.is_loaded()
    eng = loader.get() if loaded else None
    ready = bool(eng and eng.warmed_up)
    body = HealthResponse(
        status="ok" if ready else "starting",
        model_loaded=loaded,
        warmed_up=ready,
        model_version=eng.model_version if eng else "",
        g2p_version=eng.g2p_version if eng else "",
        algorithm_version=settings.algorithm_version,
        calibration_version=eng.calibrator.version if eng else "",
    )
    return JSONResponse(status_code=200 if ready else 503, content=body.model_dump())


@app.post("/v1/assess", response_model=AssessmentResult)
async def assess_endpoint(
    audio: UploadFile = File(...),
    reference_text: str = Form(...),
    locale: str = Form("en-US"),
    request_id: str = Form(""),
) -> JSONResponse:
    eng = loader.get()

    try:
        raw_audio = await audio.read(settings.max_audio_bytes + 1)
        if len(raw_audio) > settings.max_audio_bytes:
            raise AudioError(
                ErrorCode.AUDIO_TOO_LONG,
                f"audio vượt {settings.max_audio_bytes} byte",
            )
        if not reference_text or len(reference_text) > settings.max_reference_chars:
            raise AudioError(
                ErrorCode.G2P_FAILED,
                f"reference_text phải có 1–{settings.max_reference_chars} ký tự",
            )
        waveform, rate = load_wav(raw_audio)
        duration_ms = validate(waveform, rate)
    except AudioError as exc:
        log.info("[%s] audio bị từ chối: %s", request_id, exc.message)
        return _error(exc.code, exc.message)

    if _slots.locked():
        # Đẩy ngược áp lực thay vì xếp hàng vô hạn — worker sẽ retry vì 503 là retryable
        return _error(
            ErrorCode.MODEL_OVERLOADED,
            f"quá {settings.max_concurrent_inference} inference đồng thời",
        )

    async with _slots:
        try:
            result = await run_in_threadpool(
                assess.run, eng, waveform, reference_text, duration_ms
            )
        except G2PError as exc:
            log.info("[%s] g2p thất bại: %s", request_id, exc)
            return _error(ErrorCode.G2P_FAILED, str(exc))
        except Exception as exc:  # noqa: BLE001
            log.exception("[%s] inference lỗi", request_id)
            return _error(ErrorCode.INTERNAL, str(exc))

    log.info(
        "[%s] xong trong %d ms (forward %d, gop %d, diag %d) | %d phoneme",
        request_id,
        result.timing_ms.total,
        result.timing_ms.forward,
        result.timing_ms.gop,
        result.timing_ms.diagnosis,
        len(result.phonemes),
    )
    return JSONResponse(status_code=200, content=result.model_dump())
