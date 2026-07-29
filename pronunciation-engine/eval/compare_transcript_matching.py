"""GOP âm học vs. so khớp phiên âm — đo trên cùng 600 câu người Việt của L2-ARCTIC.

    python eval/compare_transcript_matching.py --parquet /data.parquet --lexicon eval/lexicon-en.json

═══════════════════════════════════════════════════════════════════════════════
CÂU HỎI
═══════════════════════════════════════════════════════════════════════════════

Các app luyện nói chạy trên máy (EngAloud là ví dụ đã mổ xẻ) chấm phát âm như sau:
bộ nhận dạng giọng nói trả về CHỮ → so từng từ với câu mẫu → từ nào lệch thì tra từ điển
phát âm, đối chiếu chuỗi phoneme, chỉ ra âm khác nhau.

Cách đó có một trần cứng: **lỗi chỉ lộ ra khi bộ nhận dạng trả về một TỪ KHÁC.** Mô hình
ngôn ngữ của ASR được thiết kế để chống chịu giọng — nó chủ động sửa phát âm lệch về từ
đúng. Lỗi nào không đổi được từ thì tàng hình, bất kể ASR tốt đến đâu.

Script này đo cái trần đó bằng số, và đặt cạnh engine GOP trên cùng một tập nhãn người gán.

═══════════════════════════════════════════════════════════════════════════════
VÌ SAO KHÔNG CHẠY MỘT ASR THẬT
═══════════════════════════════════════════════════════════════════════════════

Chạy Whisper hay Vosk rồi công bố số sẽ mở ra đúng một câu vặn không trả lời được: *"bộ
nhận dạng đó có đại diện cho cái chạy trên điện thoại không?"* Không ai chứng minh được, và
kết quả sẽ phụ thuộc vào lựa chọn đó nhiều hơn phụ thuộc vào điều đang muốn đo.

Thay vào đó, ta đo **CẬN TRÊN** của mọi hệ so khớp phiên âm, bằng cách cho nó những điều
kiện tốt hơn bất kỳ ASR thật nào:

  1. Giả định ASR phiên âm HOÀN HẢO đúng những gì người học đã phát ra — lấy thẳng chuỗi
     phoneme mà người gán nhãn L2-ARCTIC nghe được. Không lỗi nhận dạng, không nhiễu.
  2. Giả định nó phát hiện được lỗi NGAY KHI tồn tại bất kỳ từ tiếng Anh nào phát âm giống
     chuỗi đã phát ra — tra trong từ điển 228.000 cách phát âm dựng từ `/usr/share/dict/words`.
  3. Chuẩn hoá mạnh tay khi tra (bỏ trọng âm, bỏ độ dài) — mọi ký hiệu bị gộp đều làm việc
     "tìm thấy một từ thật" DỄ hơn.

Cả ba đều nghiêng về phía đối thủ. Con số bỏ sót in ra vì thế là **cận dưới**: ASR thật chỉ
có thể bỏ sót nhiều hơn, không thể ít hơn.

═══════════════════════════════════════════════════════════════════════════════
SO Ở MỨC TỪ, KHÔNG PHẢI MỨC ÂM VỊ
═══════════════════════════════════════════════════════════════════════════════

So khớp phiên âm không có khái niệm "âm vị này sai bao nhiêu" — nó chỉ biết từ đúng hay
lệch. Ép nó vào thang âm vị rồi so AUC là dựng bù nhìn. Cả hai hệ vì thế được hỏi đúng một
câu mà cả hai đều trả lời được: **"từ này có lỗi phát âm không?"**

Và báo cáo cả precision lẫn recall. Một hệ báo "sai hết" đạt recall 100% — chỉ nhìn recall
là tự lừa mình.
"""

from __future__ import annotations

import argparse
import json
import logging
import sys
from collections import Counter
from pathlib import Path

import pyarrow.parquet as pq
import torch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.engine import gop as gop_mod  # noqa: E402
from app.engine import loader  # noqa: E402
from app.engine.alignment import Op, align  # noqa: E402
from app.engine.diagnosis import diagnose, greedy_decode  # noqa: E402
from app.schemas import PhonemeDiagnosis  # noqa: E402

sys.path.insert(0, str(Path(__file__).resolve().parent))

from l2arctic import ground_truth, prf, read_wav, segment  # noqa: E402
from lexicon import normalise  # noqa: E402

logging.basicConfig(level=logging.INFO, format="%(levelname)-7s %(message)s")
log = logging.getLogger("compare")

#: Nhãn engine coi là "có vấn đề". UNCERTAIN cố ý KHÔNG nằm đây: engine dùng nó để nói
#: "không đủ tin cậy để kết luận", và tính nó thành phát hiện sẽ thổi phồng recall bằng
#: đúng những trường hợp nó tự nhận là không biết.
FLAGGED = frozenset(
    {PhonemeDiagnosis.SUBSTITUTION, PhonemeDiagnosis.OMISSION}
)


def word_of_canonical(
    canonical: list[str], espeak_phones: list[str], espeak_word_index: list[int]
) -> list[int | None]:
    """→ chỉ số từ cho từng âm vị trong chuỗi chuẩn của DATASET.

    Chuỗi chuẩn của dataset và chuỗi espeak là hai cách phiên âm cùng một câu, độ dài có
    thể lệch. Căn hai chuỗi rồi mượn `word_index` của espeak — đây là cách duy nhất quy
    được nhãn của người gán về từng từ, vì cột `g2p`/`ipa` là chuỗi LIỀN không có ranh
    giới từ.
    """
    out: list[int | None] = [None] * len(canonical)
    for pair in align(canonical, espeak_phones):
        if pair.canon_index is None or pair.decoded_index is None:
            continue
        out[pair.canon_index] = espeak_word_index[pair.decoded_index]
    # Âm vị không căn được thì thừa hưởng từ của âm vị liền trước — vẫn tốt hơn là vứt bỏ
    # một nhãn thật.
    last: int | None = None
    for i, w in enumerate(out):
        if w is None:
            out[i] = last
        else:
            last = w
    return out


def main() -> int:  # noqa: PLR0915 — báo cáo tuần tự
    ap = argparse.ArgumentParser()
    ap.add_argument("--parquet", required=True, type=Path)
    ap.add_argument("--lexicon", type=Path, default=Path("eval/lexicon-en.json"))
    ap.add_argument("--l1", default="Vietnamese")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--out", type=Path, default=Path("eval/results-compare-vi.json"))
    args = ap.parse_args()

    lexicon: dict[str, list[str]] = json.loads(args.lexicon.read_text(encoding="utf-8"))
    log.info("từ điển: %d chuỗi phát âm", len(lexicon))

    table = pq.read_table(
        args.parquet,
        columns=["audio", "text", "g2p", "ipa", "speaker_code", "speaker_native_language"],
    )
    rows = [r for r in table.to_pylist() if r["speaker_native_language"] == args.l1]
    if args.limit:
        rows = rows[: args.limit]
    log.info("%d câu của người nói tiếng mẹ đẻ %s", len(rows), args.l1)

    eng = loader.load()

    # Bốn tập hợp ở mức TỪ, dùng chung một chỉ số (câu, từ).
    truth: set[tuple[int, int]] = set()
    engine_flag: set[tuple[int, int]] = set()
    match_flag: set[tuple[int, int]] = set()
    all_words: set[tuple[int, int]] = set()

    missed_by_matching: Counter[str] = Counter()
    caught_by_matching: Counter[str] = Counter()
    skipped = 0

    for utt_id, row in enumerate(rows):
        canonical, _, unk_c = segment(row["g2p"])
        perceived, _, _ = segment(row["ipa"], perceived=True)
        if not canonical or not perceived or unk_c:
            skipped += 1
            continue

        try:
            utterance = eng.g2p(row["text"])
        except Exception:  # noqa: BLE001 — một câu hỏng không được làm chết cả lượt đo
            log.warning("g2p lỗi trên %r", row["text"])
            skipped += 1
            continue

        word_of = word_of_canonical(
            canonical, utterance.phonemes, utterance.word_index_of_phoneme
        )

        # ── nhãn người gán, quy về mức từ ────────────────────────────────────
        labels = ground_truth(canonical, perceived)
        said_of_word: dict[int, list[str]] = {}
        for i, (op, said) in enumerate(labels):
            w = word_of[i]
            if w is None:
                continue
            all_words.add((utt_id, w))
            if op is not Op.MATCH:
                truth.add((utt_id, w))
            # Chuỗi người gán NGHE THẤY cho từ này — đầu vào của ASR hoàn hảo giả định.
            if op is not Op.OMISSION and said is not None:
                said_of_word.setdefault(w, []).append(said)
            elif op is Op.MATCH:
                said_of_word.setdefault(w, []).append(canonical[i])

        # ── hệ B: cận trên của so khớp phiên âm ──────────────────────────────
        for word in utterance.words:
            key = (utt_id, word.index)
            if key not in all_words:
                continue
            heard = normalise("".join(said_of_word.get(word.index, [])))
            target = word.text.lower()
            hits = lexicon.get(heard, [])

            # Ba nhánh, và chỉ MỘT nhánh cho ra phát hiện:
            #
            #   hits rỗng        → chuỗi phát ra không phải từ tiếng Anh nào. Mọi ASR có mô
            #                      hình ngôn ngữ sẽ chọn từ khả dĩ nhất trong ngữ cảnh, tức
            #                      chính từ mẫu. Lỗi TÀNG HÌNH.
            #   target ∈ hits    → chuỗi phát ra vẫn là cách phát âm hợp lệ của từ đích
            #                      (hoặc của từ đồng âm với nó). ASR trả đúng từ mẫu. Im lặng.
            #                      Nhánh này cũng là thứ giữ cho từ ĐÚNG không bị báo nhầm —
            #                      "to/too/two" phát âm chuẩn không được tính là lỗi.
            #   còn lại          → ASR trả về từ khác. PHÁT HIỆN ĐƯỢC.
            detected = bool(hits) and target not in hits
            if detected:
                match_flag.add(key)
            if key in truth:
                (caught_by_matching if detected else missed_by_matching)[target] += 1

        # ── hệ A: engine GOP ─────────────────────────────────────────────────
        waveform, _ = read_wav(row["audio"]["bytes"])
        with torch.no_grad():
            audio = torch.from_numpy(waveform).unsqueeze(0).to(eng.device)
            log_probs = torch.log_softmax(eng.model(audio).logits[0], dim=-1).cpu()

        vocab = eng.tokenizer.get_vocab()
        espeak_ids = [vocab[p] for p in utterance.phonemes]
        gop_raw = gop_mod.compute(
            log_probs,
            espeak_ids,
            eng.confusion_ids(utterance.phonemes),
            blank_id=eng.blank_id,
        )
        runs = greedy_decode(log_probs, eng.id_to_symbol, eng.blank_id)
        pairs = align(
            utterance.phonemes,
            [r.symbol for r in runs],
            sub_cost=lambda e, s: 0.6 if s in eng.confusion.confuse(e) else 1.0,
        )
        verdicts = diagnose(
            pairs,
            utterance.phonemes,
            espeak_ids,
            runs,
            gop_raw,
            log_probs,
            eng.blank_id,
            eng.merge_rules,
        )
        for i, (diagnosis, _, _) in enumerate(verdicts):
            if diagnosis in FLAGGED:
                engine_flag.add((utt_id, utterance.word_index_of_phoneme[i]))

        if (utt_id + 1) % 50 == 0:
            log.info("… %d/%d câu", utt_id + 1, len(rows))

    # ── báo cáo ──────────────────────────────────────────────────────────────
    def score(pred: set[tuple[int, int]]) -> tuple[float, float, float, int, int, int]:
        tp = len(pred & truth)
        fp = len(pred - truth)
        fn = len(truth - pred)
        p, r, f = prf(tp, fp, fn)
        return p, r, f, tp, fp, fn

    e_p, e_r, e_f, e_tp, e_fp, e_fn = score(engine_flag)
    m_p, m_r, m_f, m_tp, m_fp, m_fn = score(match_flag)

    print(f"\n{'=' * 74}")
    print(f"TỪ CÓ LỖI PHÁT ÂM — {len(rows)} câu, {len(all_words)} từ, "
          f"{len(truth)} từ có lỗi theo người gán ({len(truth) / max(1, len(all_words)):.1%})")
    print("=" * 74)
    print(f"{'hệ':<34}{'precision':>11}{'recall':>10}{'F1':>8}{'bắt được':>11}{'bỏ sót':>9}")
    print("-" * 74)
    print(f"{'Engine GOP (âm học)':<34}{e_p:>11.3f}{e_r:>10.3f}{e_f:>8.3f}{e_tp:>11}{e_fn:>9}")
    print(f"{'So khớp phiên âm (CẬN TRÊN)':<34}{m_p:>11.3f}{m_r:>10.3f}{m_f:>8.3f}{m_tp:>11}{m_fn:>9}")

    blind = len(truth - match_flag)
    both = len(truth & match_flag & engine_flag)
    only_engine = len((truth & engine_flag) - match_flag)
    print(f"\n{'=' * 74}\nVÙNG MÙ CỦA SO KHỚP PHIÊN ÂM\n{'=' * 74}")
    print(f"  từ có lỗi mà KHÔNG hệ so khớp nào thấy được : {blind} / {len(truth)} "
          f"({blind / max(1, len(truth)):.1%})")
    print(f"  trong số đó, engine GOP bắt được            : {only_engine} "
          f"({only_engine / max(1, blind):.1%} vùng mù)")
    print(f"  cả hai cùng bắt được                        : {both}")

    print("\n  20 từ bị so khớp phiên âm bỏ sót nhiều nhất:")
    for word, n in missed_by_matching.most_common(20):
        print(f"    {word:<18} {n:>4}")

    result = {
        "l1": args.l1,
        "n_utterances": len(rows),
        "n_words": len(all_words),
        "n_words_with_error": len(truth),
        "skipped": skipped,
        "engine_gop": {"precision": e_p, "recall": e_r, "f1": e_f,
                       "tp": e_tp, "fp": e_fp, "fn": e_fn},
        "transcript_matching_ceiling": {"precision": m_p, "recall": m_r, "f1": m_f,
                                        "tp": m_tp, "fp": m_fp, "fn": m_fn},
        "blind_spot": {"total": blind, "caught_by_gop": only_engine,
                       "caught_by_both": both},
        "missed_words_top": missed_by_matching.most_common(50),
        "caught_words_top": caught_by_matching.most_common(50),
    }
    args.out.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"\nghi {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
