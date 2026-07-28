"""Liệt kê inventory phoneme thật của espeak en-us, đối chiếu với confusion table.

Sinh ra sau khi phát hiện `juː` không tồn tại (espeak trả `j uː` hai token) và `əl`, `ᵻ`
tồn tại nhưng vắng khỏi bảng. Đoán inventory bằng kiến thức IPA là không đủ — phải hỏi
chính espeak.

Phoneme vắng khỏi confusion table KHÔNG gây lỗi lúc chạy: nó chỉ nhận tập ứng viên rỗng,
tức GOP chỉ còn so với phương án xóa. Chất lượng tụt âm thầm ở đúng những âm đó.
"""

from __future__ import annotations

import json
import sys
from collections import Counter
from pathlib import Path

from transformers import Wav2Vec2PhonemeCTCTokenizer

from sentences import SEED_SENTENCES

MODEL_ID = "facebook/wav2vec2-xlsr-53-espeak-cv-ft"
MODEL_REVISION = "2c733782da5604684829819a5eb744c193fe9398"

# Từ chọn để phủ phonotactics tiếng Anh: cụm phụ âm, nguyên âm đôi, âm cuối, âm hiếm,
# hậu tố rút gọn — những chỗ espeak hay sinh ký hiệu bất ngờ.
PROBE_WORDS = """
you usually new music beautiful few vision measure decision pleasure garage
strength twelfths sixths clothes months asked texts prompts glimpsed
bird word heard fur her early journey world girl
care hair there their where bear share
here near beer clear cheer year fear
sure poor tour cure pure endure
fire hire tired higher buyer liar choir
hour flower power tower our sour
little bottle metal pedal camel tunnel rhythm prism chasm
button cotton mountain certain kitten written
about again ago away asleep across among
happy money city very early sorry
question nation vision fusion suggestion
judge church watch bridge fudge orange
think this thumb those thirty father breath breathe
zoo rose buzz phase easy busy
yes young yellow use useful unit
one won once wolf woman women
schedule schema chaos chorus chemistry
car start hard park farm large
more four door floor before
science quiet diet giant trial
"""


def main() -> int:
    tok = Wav2Vec2PhonemeCTCTokenizer.from_pretrained(MODEL_ID, revision=MODEL_REVISION)
    vocab = tok.get_vocab()
    delim = tok.word_delimiter_token

    def phonemes_of(text: str) -> list[str]:
        raw = tok.phonemize(text, phonemizer_lang="en-us").strip()
        while raw.endswith(delim):
            raw = raw[: -len(delim)].strip()
        return [p for p in raw.split() if p != delim]

    seen: Counter[str] = Counter()
    for sent in SEED_SENTENCES:
        seen.update(phonemes_of(sent))
    for word in PROBE_WORDS.split():
        seen.update(phonemes_of(word))

    oov = sorted(p for p in seen if p not in vocab)
    print(f"inventory quan sát được : {len(seen)} ký hiệu")
    print(f"ngoài vocab model       : {oov or 'không có'}")

    table_path = Path(__file__).resolve().parents[1] / "artifacts" / "confusion.json"
    table = json.loads(table_path.read_text(encoding="utf-8"))
    general = {k for k in table["tier_general"] if not k.startswith("_")}
    grouped = {
        p
        for members in table["phone_groups"].values()
        if isinstance(members, list)
        for p in members
    }

    missing_conf = sorted(set(seen) - general)
    missing_group = sorted(set(seen) - grouped)
    unused = sorted(general - set(seen))

    print(f"\n{'─' * 70}")
    print(f"THIẾU trong tier_general ({len(missing_conf)}):")
    for p in missing_conf:
        print(f"   {p!r:8} xuất hiện {seen[p]:3} lần")
    print(f"\nTHIẾU trong phone_groups ({len(missing_group)}): {missing_group}")
    print(f"\nCÓ trong bảng nhưng không quan sát thấy ({len(unused)}): {unused}")
    print(f"{'─' * 70}")

    if missing_conf or missing_group or oov:
        print("\n✗ confusion.json chưa phủ hết inventory thật")
        return 1
    print("\n✓ confusion.json phủ toàn bộ inventory quan sát được")
    return 0


if __name__ == "__main__":
    sys.exit(main())
