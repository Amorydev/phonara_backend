"""Text → chuỗi phoneme chuẩn, tách theo từ (§3.2 bước 1).

Dùng `Wav2Vec2PhonemeCTCTokenizer.phonemize()` của **chính model**, không gọi `phonemizer`
trực tiếp. `tokenizer_config.json` khai báo sẵn `backend=espeak`, `lang=en-us`, nên cấu
hình G2P khớp với lúc huấn luyện **theo cấu tạo** — sai lệch không còn là thứ có thể xảy
ra, thay vì là thứ phải canh mỗi lần đổi thư viện.

Cụ thể: vocab model KHÔNG chứa dấu trọng âm (`ˈ`, `ˌ`). Gọi `phonemizer` thẳng với
`with_stress=True` — mặc định trong nhiều ví dụ — sẽ sinh token ngoài vocab ở MỌI câu mà
không có gì báo lỗi. R1 sinh ra để chặn đúng kịch bản đó.

Hai cạm bẫy do R1 phát hiện, cả hai đều xử lý ở đây:
  1. `|` được sinh ra nhưng KHÔNG có trong vocab → giữ để suy word_index, strip trước
     khi đưa vào ctc_loss.
  2. `phonemize()` luôn thừa một `|` ở cuối → rstrip trước khi tách từ.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass

from transformers import Wav2Vec2PhonemeCTCTokenizer

log = logging.getLogger(__name__)


class G2PError(RuntimeError):
    """Không phiên âm được → ErrorCode.G2P_FAILED, không retry."""


@dataclass(frozen=True, slots=True)
class Word:
    text: str
    index: int
    phonemes: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class Utterance:
    words: tuple[Word, ...]

    @property
    def phonemes(self) -> list[str]:
        """Chuỗi phẳng, đã bỏ delimiter — dạng dùng cho ctc_loss."""
        return [p for w in self.words for p in w.phonemes]

    @property
    def word_index_of_phoneme(self) -> list[int]:
        """word_index cho từng phoneme trong chuỗi phẳng."""
        return [w.index for w in self.words for _ in w.phonemes]


def _split_words(phonemized: str, delimiter: str) -> list[list[str]]:
    # rstrip delimiter thừa ở cuối, nếu không từ cuối sẽ dính '|' vào đuôi (R1)
    body = phonemized.strip()
    while body.endswith(delimiter):
        body = body[: -len(delimiter)].strip()
    if not body:
        return []
    return [chunk.split() for chunk in body.split(delimiter) if chunk.split()]


class G2P:
    def __init__(self, tokenizer: Wav2Vec2PhonemeCTCTokenizer) -> None:
        self._tok = tokenizer
        self._delim = tokenizer.word_delimiter_token
        self._lang = tokenizer.phonemizer_lang
        self._vocab = tokenizer.get_vocab()

    def _phonemize(self, text: str) -> str:
        return self._tok.phonemize(text, phonemizer_lang=self._lang)

    def __call__(self, text: str) -> Utterance:
        tokens = text.split()
        if not tokens:
            raise G2PError("reference_text rỗng")

        groups = _split_words(self._phonemize(text), self._delim)

        # Phiên âm cả câu giữ được ngữ cảnh, nhưng nếu số nhóm không khớp số từ thì
        # word_index sẽ lệch — im lặng và rất khó truy. Rơi về phiên âm từng từ, đánh đổi
        # ngữ cảnh lấy sự đúng đắn của word_index.
        if len(groups) != len(tokens):
            log.warning(
                "g2p: số nhóm (%d) khác số từ (%d) cho %r — dùng phiên âm từng từ",
                len(groups),
                len(tokens),
                text,
            )
            groups = []
            for tok in tokens:
                per_word = _split_words(self._phonemize(tok), self._delim)
                groups.append([p for grp in per_word for p in grp])

        words: list[Word] = []
        for i, (tok, phones) in enumerate(zip(tokens, groups, strict=True)):
            oov = [p for p in phones if p not in self._vocab]
            if oov:
                # Không bao giờ nên xảy ra sau R1; nếu xảy ra là model/espeak đã đổi.
                raise G2PError(
                    f"từ {tok!r} sinh phoneme ngoài vocab: {oov}. "
                    "Chạy lại r1/ — model_version hoặc g2p_version có thể đã đổi."
                )
            words.append(Word(text=tok, index=i, phonemes=tuple(phones)))

        if not any(w.phonemes for w in words):
            raise G2PError(f"không phiên âm được câu nào từ {text!r}")

        return Utterance(words=tuple(words))
