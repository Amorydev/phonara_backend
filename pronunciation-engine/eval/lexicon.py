"""Từ điển phát âm ngược: chuỗi phoneme → những từ tiếng Anh phát âm như vậy.

Dùng cho `compare_transcript_matching.py`. Câu hỏi nó phải trả lời: *"người học phát âm ra
chuỗi âm này — có từ tiếng Anh thật nào nghe giống vậy không?"* Nếu CÓ thì bộ nhận dạng
giọng nói có cơ hội trả về từ khác và cách chấm bằng so khớp phiên âm phát hiện được lỗi.
Nếu KHÔNG thì mọi bộ nhận dạng có mô hình ngôn ngữ sẽ trả về chính từ đích, và lỗi tàng
hình.

VÌ SAO DÙNG espeak-ng CHỨ KHÔNG PHẢI CMUdict: espeak là **đúng bộ G2P mà engine dùng**
(`app/engine/g2p.py`) và cũng là bộ sinh ra chuỗi chuẩn trong bộ đo. Đi qua CMUdict sẽ phải
thêm một lớp ánh xạ ARPAbet↔IPA nữa, và mỗi lớp ánh xạ là một nguồn sai lệch mới. Ở đây
không có lớp nào.

Danh sách từ lấy từ `/usr/share/dict/words` của hệ điều hành — không tải gì từ mạng.

    python eval/lexicon.py --words /usr/share/dict/words --out eval/lexicon-en.json
"""

from __future__ import annotations

import argparse
import json
import logging
import subprocess
import sys
from pathlib import Path

logging.basicConfig(level=logging.INFO, format="%(levelname)-7s %(message)s")
log = logging.getLogger("lexicon")

#: Ký hiệu bỏ đi trước khi so. Trọng âm và độ dài KHÔNG phân biệt được từ này với từ kia
#: trong tiếng Anh, mà lại là thứ người gán nhãn L2-ARCTIC ghi rất khác nhau.
_STRIP = str.maketrans("", "", "ˈˌːˑ ‿-")


def normalise(phones: str) -> str:
    """Chuẩn hoá một chuỗi IPA để tra từ điển.

    Chuẩn hoá MẠNH TAY là cố ý. Mọi ký hiệu bị gộp lại đều làm cho việc "tìm thấy một từ
    thật" DỄ hơn, tức là làm cách so khớp phiên âm trông TỐT hơn thực tế. Kết quả cuối cùng
    vì thế là cận dưới của số lỗi mà nó bỏ sót — chỗ nào nghi ngờ thì phần thắng thuộc về
    phía đối thủ.
    """
    out = phones.translate(_STRIP)
    # espeak viết nguyên âm đôi theo nhiều cách tuỳ ngữ cảnh; gộp về một dạng.
    for a, b in (("ɐ", "ʌ"), ("ᵻ", "ɪ"), ("ɚ", "ɜ"), ("ɝ", "ɜ"), ("ɹ", "r"), ("ɡ", "g")):
        out = out.replace(a, b)
    return out


def phonemise(words: list[str], voice: str = "en-us", batch: int = 2000) -> list[str]:
    """→ chuỗi IPA cho từng từ, cùng thứ tự. Chuỗi rỗng nghĩa là espeak bó tay."""
    out: list[str] = []
    for start in range(0, len(words), batch):
        chunk = words[start : start + batch]
        proc = subprocess.run(
            ["espeak-ng", "-q", "--ipa", "-v", voice],
            input="\n".join(chunk),
            capture_output=True,
            text=True,
            check=False,
        )
        lines = proc.stdout.split("\n")
        # espeak trả đúng một dòng cho mỗi dòng vào; lệch nghĩa là có từ chứa ký tự lạ.
        if len(lines) < len(chunk):
            lines += [""] * (len(chunk) - len(lines))
        out.extend(line.strip() for line in lines[: len(chunk)])
        log.info("đã xử lý %d/%d từ", min(start + batch, len(words)), len(words))
    return out


def build(words_path: Path, voice: str) -> dict[str, list[str]]:
    raw = [
        w.strip().lower()
        for w in words_path.read_text(encoding="utf-8", errors="ignore").splitlines()
        if w.strip() and w.strip().isalpha() and w.strip().isascii()
    ]
    words = sorted(set(raw))
    log.info("%d từ duy nhất từ %s", len(words), words_path)

    ipa = phonemise(words, voice)

    index: dict[str, list[str]] = {}
    for word, phones in zip(words, ipa):
        if not phones:
            continue
        key = normalise(phones)
        if not key:
            continue
        index.setdefault(key, []).append(word)

    log.info("%d chuỗi phát âm duy nhất", len(index))
    return index


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--words", type=Path, default=Path("/usr/share/dict/words"))
    ap.add_argument("--out", type=Path, default=Path("eval/lexicon-en.json"))
    ap.add_argument("--voice", default="en-us")
    args = ap.parse_args()

    if not args.words.exists():
        log.error("không thấy danh sách từ: %s", args.words)
        return 1

    index = build(args.words, args.voice)
    args.out.write_text(json.dumps(index, ensure_ascii=False), encoding="utf-8")
    log.info("ghi %s (%.1f MB)", args.out, args.out.stat().st_size / 1e6)

    # Kiểm nhanh: những cặp tối thiểu kinh điển phải tra ra đúng từ khác nhau.
    for probe in ("think", "sink", "three", "tree"):
        phones = normalise(phonemise([probe], args.voice)[0])
        log.info("  %-6s → %-8s → %s", probe, phones, index.get(phones, [])[:5])
    return 0


if __name__ == "__main__":
    sys.exit(main())
