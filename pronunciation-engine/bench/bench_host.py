"""Đo tốc độ engine TRÊN CHÍNH MÁY SẼ CHẠY PRODUCTION.

    docker run --rm -v "$PWD":/w -w /w phonara-engine python bench/bench_host.py

VÌ SAO PHẢI ĐO TẠI CHỖ, KHÔNG SUY TỪ MÁY KHÁC:

  1. Số RTF 0,249 trong tài liệu đo trên Apple Silicon. Trên VPS 2 vCPU thực tế là 1,68–2,51
     — chênh 7–10 lần. Suy từ máy dev đã dẫn tới một điều kiện thoát giai đoạn 1 được đánh
     dấu "đạt" trong khi thực tế trượt.

  2. Lượng tử hoá INT8 phụ thuộc KIẾN TRÚC. Phần tăng tốc đến từ `fbgemm`, mà `fbgemm` chỉ
     có trên x86. Đo trên máy ARM cho 0,94× (chậm hơn!) trong khi trên x86 tài liệu chung
     báo 2–3×. Hai con số không nói về cùng một thứ.

Script này in ra kiến trúc và backend đang dùng cùng với thời gian, để không ai đọc nhầm số
của máy này thành số của máy khác.
"""

from __future__ import annotations

import platform
import sys
import time
import wave
from pathlib import Path

import numpy as np
import torch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.engine import loader  # noqa: E402

# Audio 7 giây — cùng độ dài với phép đo tham chiếu trong README, để so được.
DURATION_S = 7
SAMPLE_RATE = 16_000


def make_audio(path: Path) -> None:
    """Sinh audio thử. Nội dung không quan trọng: thời gian chạy phụ thuộc ĐỘ DÀI, không
    phụ thuộc người nói gì."""
    import math
    import random

    with wave.open(str(path), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SAMPLE_RATE)
        frames = bytearray()
        for i in range(SAMPLE_RATE * DURATION_S):
            v = int(3000 * math.sin(2 * math.pi * 180 * i / SAMPLE_RATE)) + random.randint(-400, 400)
            frames += int(max(-32768, min(32767, v))).to_bytes(2, "little", signed=True)
        w.writeframes(bytes(frames))


def bench(model, x: torch.Tensor, runs: int = 5) -> tuple[float, float]:
    """→ (nhanh nhất, trung bình) tính bằng ms. Bỏ lần đầu vì nó luôn chậm bất thường."""
    with torch.no_grad():
        model(x)  # ấm — lần gọi đầu sau khi rỗi chậm gấp ~4 lần, đo vào là sai
        times = []
        for _ in range(runs):
            t0 = time.perf_counter()
            model(x)
            times.append((time.perf_counter() - t0) * 1000)
    return min(times), sum(times) / len(times)


def main() -> int:
    threads = int(sys.argv[1]) if len(sys.argv) > 1 else 0
    if threads > 0:
        torch.set_num_threads(threads)

    print("=" * 66)
    print("MÔI TRƯỜNG")
    print("=" * 66)
    print(f"  kiến trúc            : {platform.machine()}")
    print(f"  số core hệ điều hành : {torch.get_num_threads()} luồng torch")
    print(f"  backend lượng tử hoá : {torch.backends.quantized.engine}")
    print(f"  backend khả dụng     : {torch.backends.quantized.supported_engines}")
    if "fbgemm" not in torch.backends.quantized.supported_engines:
        print("  ⚠️  KHÔNG có fbgemm — số INT8 dưới đây KHÔNG suy ra được cho máy x86")

    audio = Path("/tmp/_bench.wav")
    if not audio.exists():
        make_audio(audio)
    with wave.open(str(audio), "rb") as w:
        raw = w.readframes(w.getnframes())
    wav = np.frombuffer(raw, dtype=np.int16).astype(np.float32) / 32768.0
    x = torch.from_numpy(wav).unsqueeze(0)

    eng = loader.load()

    print(f"\n{'=' * 66}\nTỐC ĐỘ — audio {DURATION_S}s\n{'=' * 66}")
    lo32, avg32 = bench(eng.model, x)
    rtf32 = avg32 / (DURATION_S * 1000)
    print(f"  FP32 : nhanh nhất={lo32:7.0f} ms   trung bình={avg32:7.0f} ms   RTF={rtf32:.3f}")

    int8 = torch.quantization.quantize_dynamic(eng.model, {torch.nn.Linear}, dtype=torch.qint8)
    lo8, avg8 = bench(int8, x)
    rtf8 = avg8 / (DURATION_S * 1000)
    print(f"  INT8 : nhanh nhất={lo8:7.0f} ms   trung bình={avg8:7.0f} ms   RTF={rtf8:.3f}")
    print(f"\n  INT8 nhanh hơn FP32: {avg32 / avg8:.2f}×")

    print(f"\n{'=' * 66}\nĐỌC KẾT QUẢ\n{'=' * 66}")
    print("  RTF < 0,6  → câu 5 giây xong dưới 3 s, đạt điều kiện 3 của §3.6")
    print("  RTF > 1,0  → xử lý lâu hơn cả độ dài audio")
    print(f"  Thông lượng FP32 ước tính: {1000 / avg32 * 60:.0f} lượt/phút mỗi luồng xử lý")
    print("\n  INT8 nhanh hơn ĐÁNG KỂ thì phải đo lại ĐỘ CHÍNH XÁC trước khi dùng:")
    print("    python eval/l2arctic.py --parquet <parquet> --l1 Vietnamese")
    print("    so AUC với 0,8314 (bản FP32 hiện tại)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
