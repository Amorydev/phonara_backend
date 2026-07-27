"""R1 — kiểm chứng vocab G2P khớp vocab tokenizer của model.

Đây là cổng đầu tiên của kế hoạch (PRONUNCIATION_ENGINE_PLAN.md §9, R1). Nếu chuỗi phoneme
do G2P sinh ra chứa ký hiệu không có trong vocab của model, mọi thứ phía sau hỏng **im
lặng**: không exception, chỉ là điểm số vô nghĩa.

Kiểm ba việc:
  A. Tokenizer của model tự phonemize 20 câu seed → có token nào là <unk> không
  B. Ranh giới từ có bảo toàn không (cần cho word_index ở §3.2 bước 1)
  C. Các âm mục tiêu của người Việt có nằm trong vocab không

Chạy: xem run.sh
"""

from __future__ import annotations

import json
import sys
from collections import Counter

from transformers import Wav2Vec2PhonemeCTCTokenizer

from sentences import SEED_SENTENCES, VI_TARGET_PHONEMES

MODEL_ID = "facebook/wav2vec2-xlsr-53-espeak-cv-ft"
# Pin theo commit SHA, không chỉ tên — §3.4. Đây là giá trị ghi vào model_version.
MODEL_REVISION = "2c733782da5604684829819a5eb744c193fe9398"

OK = "\033[32m✓\033[0m"
BAD = "\033[31m✗\033[0m"
WARN = "\033[33m!\033[0m"


def rule(title: str) -> None:
    print(f"\n{'─' * 78}\n{title}\n{'─' * 78}")


def main() -> int:
    failures: list[str] = []

    rule("Nạp tokenizer")
    tok = Wav2Vec2PhonemeCTCTokenizer.from_pretrained(MODEL_ID, revision=MODEL_REVISION)
    vocab: dict[str, int] = tok.get_vocab()
    print(f"model           : {MODEL_ID}")
    print(f"revision        : {MODEL_REVISION}")
    print(f"vocab size      : {len(vocab)}")
    print(f"phonemizer      : {tok.phonemizer_backend} / {tok.phonemizer_lang}")
    print(f"word delimiter  : {tok.word_delimiter_token!r} "
          f"(trong vocab? {tok.word_delimiter_token in vocab})")

    # ── A. Phonemize 20 câu, tìm ký hiệu ngoài vocab ────────────────────────────
    rule("A. Phonemize 20 câu seed → tìm ký hiệu OOV")

    all_symbols: Counter[str] = Counter()
    oov: dict[str, list[str]] = {}
    per_sentence: list[tuple[str, str]] = []

    for sent in SEED_SENTENCES:
        phonemes = tok.phonemize(sent, phonemizer_lang=tok.phonemizer_lang)
        per_sentence.append((sent, phonemes))
        # phonemize() trả chuỗi các token cách nhau bằng space
        for sym in phonemes.split():
            if sym == tok.word_delimiter_token:
                continue
            all_symbols[sym] += 1
            if sym not in vocab:
                oov.setdefault(sym, []).append(sent)

    print(f"tổng token sinh ra : {sum(all_symbols.values())}")
    print(f"ký hiệu khác nhau  : {len(all_symbols)}")

    if oov:
        print(f"\n{BAD} CÓ {len(oov)} KÝ HIỆU NGOÀI VOCAB — R1 KHÔNG ĐẠT:")
        for sym, sents in sorted(oov.items(), key=lambda kv: -len(kv[1])):
            print(f"   {sym!r}  (U+{ord(sym[0]):04X}…)  xuất hiện ở {len(sents)} câu")
            print(f"      vd: {sents[0]}")
        failures.append(f"{len(oov)} ký hiệu OOV")
    else:
        print(f"\n{OK} 100% ký hiệu nằm trong vocab ({len(all_symbols)}/{len(all_symbols)})")

    print("\nMẫu 3 câu đầu:")
    for sent, ph in per_sentence[:3]:
        print(f"   {sent}\n     → {ph}")

    # ── B. Ranh giới từ ─────────────────────────────────────────────────────────
    rule("B. Ranh giới từ (cần cho word_index)")

    probe = "three cats"
    ph = tok.phonemize(probe, phonemizer_lang=tok.phonemizer_lang)
    delim = tok.word_delimiter_token
    has_delim = delim in ph.split()
    print(f"input  : {probe!r}")
    print(f"output : {ph!r}")

    if has_delim:
        # phonemize() luôn kết thúc bằng một delimiter thừa — phải bỏ trước khi tách,
        # nếu không từ cuối sẽ dính '|' vào đuôi. g2p.py phải làm đúng như đây.
        words = [w.strip() for w in ph.strip().rstrip(delim).strip().split(delim)]
        words = [w for w in words if w]
        print(f"\n{OK} có delimiter {delim!r} → tách được {len(words)} từ: {words}")
        if len(words) != len(probe.split()):
            print(f"{WARN} số từ tách ra ({len(words)}) khác số từ đầu vào ({len(probe.split())})")
            failures.append("số từ sau khi tách không khớp")
        # delimiter KHÔNG có trong vocab → phải loại khỏi chuỗi trước khi tính CTC loss
        print(f"{WARN} {delim!r} không nằm trong vocab → g2p.py phải strip nó khỏi chuỗi "
              f"phoneme trước khi đưa vào ctc_loss (§3.2 bước 3)")
    else:
        print(f"\n{BAD} KHÔNG có delimiter trong output → không suy được word_index")
        print("    Phải phonemize TỪNG TỪ riêng rồi tự ghép — xem khuyến nghị cuối.")
        failures.append("không bảo toàn ranh giới từ")

    # phương án dự phòng: phonemize từng từ một
    print("\nPhương án dự phòng — phonemize từng từ:")
    for w in probe.split():
        print(f"   {w!r:10} → {tok.phonemize(w, phonemizer_lang=tok.phonemizer_lang)!r}")

    # ── C. Âm mục tiêu người Việt ───────────────────────────────────────────────
    rule("C. Âm mục tiêu của người học Việt có trong vocab?")

    missing_targets = [p for p in VI_TARGET_PHONEMES if p not in vocab]
    for p in VI_TARGET_PHONEMES:
        mark = OK if p in vocab else BAD
        seen = all_symbols.get(p, 0)
        note = f"xuất hiện {seen} lần trong 20 câu seed" if seen else "KHÔNG xuất hiện trong seed"
        print(f"   {mark} {p!r:6} {note}")

    if missing_targets:
        print(f"\n{BAD} {len(missing_targets)} âm mục tiêu không có trong vocab: {missing_targets}")
        failures.append(f"{len(missing_targets)} âm mục tiêu OOV")
    else:
        print(f"\n{OK} toàn bộ {len(VI_TARGET_PHONEMES)} âm mục tiêu đều có trong vocab")

    # §6.1 yêu cầu ≥30 lần xuất hiện cho mỗi âm mục tiêu. Mỗi người đọc CẢ 20 câu, nên
    # số lần thực tế = (số lần trong 1 lượt) × (số người). Chỉ âm có 0 lần là gap thật —
    # không số người nào cứu được.
    SPEAKERS = 20
    FLOOR = 30
    print(f"\nPhủ âm cho benchmark §6.1 (ngưỡng ≥{FLOOR} lần, {SPEAKERS} người đọc cả 20 câu):")
    hard_gap, thin = [], []
    for p in VI_TARGET_PHONEMES:
        per_pass = all_symbols.get(p, 0)
        total = per_pass * SPEAKERS
        if per_pass == 0:
            hard_gap.append(p)
        elif total < FLOOR:
            thin.append((p, total))
        else:
            print(f"   {OK} {p!r:6} {per_pass:2}/lượt × {SPEAKERS} = {total:4} lần")

    for p, total in thin:
        print(f"   {WARN} {p!r:6} chỉ đạt {total} lần — cần thêm câu hoặc thêm người")
    for p in hard_gap:
        print(f"   {BAD} {p!r:6} 0 lần — KHÔNG câu seed nào chứa âm này")

    if hard_gap:
        print(f"\n{BAD} {len(hard_gap)} âm mục tiêu vắng mặt hoàn toàn khỏi bộ 20 câu: {hard_gap}")
        print("    Thêm người đọc KHÔNG cứu được — phải bổ sung câu vào assessment_questions.")
        print("    Gợi ý câu chứa /ʒ/: measure · vision · usually · decision · television")
        # đây là gap nội dung, không phải lỗi vocab → cảnh báo, không đánh trượt R1

    # ── Kết luận ────────────────────────────────────────────────────────────────
    rule("KẾT LUẬN R1")

    freq = json.dumps(
        {k: v for k, v in all_symbols.most_common(15)}, ensure_ascii=False, indent=2
    )
    print(f"15 âm xuất hiện nhiều nhất:\n{freq}\n")

    if failures:
        print(f"{BAD} R1 KHÔNG ĐẠT — {len(failures)} vấn đề:")
        for f in failures:
            print(f"     · {f}")
        return 1

    print(f"{OK} R1 ĐẠT — G2P và vocab model khớp hoàn toàn.")
    print(f"    model_version = xlsr53-espeak@{MODEL_REVISION[:12]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
