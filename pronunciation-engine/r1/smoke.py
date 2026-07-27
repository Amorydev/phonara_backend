"""Smoke test /v1/assess — chạy BÊN TRONG container engine.

Dùng espeak-ng (đã có sẵn trong image) để tổng hợp audio. Đây là giọng máy, không phải
người Việt, nên KHÔNG dùng để đánh giá chất lượng — chỉ để chứng minh đường ống chạy
thông và contract đúng hình. Chất lượng là việc của giai đoạn 4.

    docker exec phonara-engine-dev python /srv/r1/smoke.py
"""

from __future__ import annotations

import io
import json
import subprocess
import sys
import urllib.request
import wave

import numpy as np

URL = "http://localhost:8000/v1/assess"
TARGET_RATE = 16_000


def synth(text: str, speed: int = 150) -> bytes:
    """espeak-ng → WAV 16 kHz mono 16-bit."""
    raw = subprocess.run(
        ["espeak-ng", "-v", "en-us", "-s", str(speed), "--stdout", text],
        capture_output=True,
        check=True,
    ).stdout

    with wave.open(io.BytesIO(raw), "rb") as w:
        rate = w.getframerate()
        samples = np.frombuffer(w.readframes(w.getnframes()), dtype=np.int16)

    if rate != TARGET_RATE:
        # Nội suy tuyến tính — đủ cho smoke test, KHÔNG đủ cho benchmark.
        n_out = int(len(samples) * TARGET_RATE / rate)
        samples = np.interp(
            np.linspace(0, len(samples) - 1, n_out),
            np.arange(len(samples)),
            samples.astype(np.float32),
        ).astype(np.int16)

    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(TARGET_RATE)
        w.writeframes(samples.tobytes())
    return buf.getvalue()


def silence(ms: int) -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(TARGET_RATE)
        w.writeframes(np.zeros(TARGET_RATE * ms // 1000, dtype=np.int16).tobytes())
    return buf.getvalue()


def post(wav: bytes, reference_text: str) -> tuple[int, dict]:
    boundary = "----phonara"
    parts: list[bytes] = []
    for name, value in (("reference_text", reference_text), ("request_id", "smoke")):
        parts.append(
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"\r\n\r\n"
            f"{value}\r\n".encode()
        )
    parts.append(
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"audio\"; "
        f"filename=\"a.wav\"\r\nContent-Type: audio/wav\r\n\r\n".encode()
    )
    parts.append(wav)
    parts.append(f"\r\n--{boundary}--\r\n".encode())

    req = urllib.request.Request(
        URL,
        data=b"".join(parts),
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as exc:
        return exc.code, json.loads(exc.read())


def show(result: dict) -> None:
    o = result["overall"]
    print(f"  overall  accuracy={o['accuracy']} completeness={o['completeness']} "
          f"fluency={o['fluency']} prosody={o['prosody']}")
    print(f"  timing   {result['timing_ms']}")
    print(f"  caps     {result['capabilities']}")
    print("  words   ", [(w["word"], w["diagnosis"],
                          None if w["accuracy"] is None else round(w["accuracy"], 1))
                         for w in result["words"]])
    print("  phonemes:")
    for p in result["phonemes"]:
        acc = "—" if p["accuracy"] is None else f"{p['accuracy']:5.1f}"
        said = p["said"] if p["said"] is not None else "null"
        print(f"     {p['expected'] or '∅':4} → {said:5}  acc={acc}  "
              f"gop={p['gop_raw']:+7.2f}  {p['diagnosis']:13} conf={p['confidence']:.2f}")


def main() -> int:
    failures = 0

    print("\n=== 1. Câu khớp: đọc đúng 'three cats' ===")
    status, body = post(synth("three cats"), "three cats")
    print(f"HTTP {status}")
    if status == 200:
        show(body)
    else:
        print(" ", body)
        failures += 1

    print("\n=== 2. Sai cố ý: audio nói 'tree cats', đề bài 'three cats' ===")
    print("    (kỳ vọng: θ bị đánh substitution hoặc điểm thấp hơn ca 1)")
    status, body = post(synth("tree cats"), "three cats")
    print(f"HTTP {status}")
    if status == 200:
        show(body)
    else:
        print(" ", body)
        failures += 1

    print("\n=== 3. Đường lỗi: im lặng ===")
    status, body = post(silence(2000), "three cats")
    print(f"HTTP {status} → {body}")
    if not (status == 422 and body.get("code") == "no_speech_detected"):
        print("  ✗ kỳ vọng 422 no_speech_detected")
        failures += 1

    print("\n=== 4. Đường lỗi: quá ngắn ===")
    status, body = post(synth("hi", speed=450)[:2000], "hi")
    print(f"HTTP {status} → {body}")
    if status != 422:
        print("  ✗ kỳ vọng 422")
        failures += 1

    print("\n=== 5. Câu dài (đo độ trễ, §3.6 điều kiện 3) ===")
    sentence = "She sells seashells by the seashore."
    status, body = post(synth(sentence), sentence)
    print(f"HTTP {status}")
    if status == 200:
        print(f"  timing {body['timing_ms']} | {len(body['phonemes'])} phoneme")
        print(f"  overall accuracy={body['overall']['accuracy']}")
    else:
        print(" ", body)
        failures += 1

    print(f"\n{'✓ smoke test đạt' if not failures else f'✗ {failures} ca hỏng'}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
