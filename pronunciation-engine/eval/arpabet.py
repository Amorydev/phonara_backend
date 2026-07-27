"""ARPAbet (speechocean762) → ký hiệu IPA của espeak/vocab model.

speechocean762 gán nhãn bằng ARPAbet (`AA0`, `IH1`, `NG`), còn G2P của ta sinh IPA espeak
(`ɑː`, `ɪ`, `ŋ`). Không có mapping thì không đối chiếu được điểm người chấm với GOP.

QUYẾT ĐỊNH THIẾT KẾ: dùng chuỗi phone CỦA DATASET làm chuỗi chuẩn, không chạy G2P trên
`text`. Lý do: người chấm cho điểm trên đúng chuỗi phone đó. Nếu ta chạy G2P riêng, chuỗi
sinh ra có thể dài/ngắn khác đi và không còn tương ứng 1:1 với nhãn — mọi con số tương
quan sau đó sẽ vô nghĩa.

Chữ số trọng âm mang thông tin thật ở hai chỗ, không được strip vô tội vạ:
  · AH0 = schwa (ə) nhưng AH1/AH2 = ʌ
  · ER0 = ɚ  nhưng ER1/ER2 = ɜː
Vocab model có đủ cả bốn ký hiệu nên phân biệt được.
"""

from __future__ import annotations

DELETION = "<DEL>"
UNKNOWN = "<unk>"

# 39 phone ARPAbet chuẩn — đúng bộ mà speechocean762 dùng (đã kiểm bằng thống kê).
_BASE: dict[str, str] = {
    # nguyên âm
    "AA": "ɑː",
    "AE": "æ",
    "AH": "ʌ",  # AH0 xử lý riêng bên dưới
    "AO": "ɔː",
    "AW": "aʊ",
    "AY": "aɪ",
    "EH": "ɛ",
    "ER": "ɜː",  # ER0 xử lý riêng bên dưới
    "EY": "eɪ",
    "IH": "ɪ",
    "IY": "iː",
    "OW": "oʊ",
    "OY": "ɔɪ",
    "UH": "ʊ",
    "UW": "uː",
    # phụ âm
    "B": "b",
    "CH": "tʃ",
    "D": "d",
    "DH": "ð",
    "F": "f",
    "G": "ɡ",
    "HH": "h",
    "JH": "dʒ",
    "K": "k",
    "L": "l",
    "M": "m",
    "N": "n",
    "NG": "ŋ",
    "P": "p",
    "R": "ɹ",
    "S": "s",
    "SH": "ʃ",
    "T": "t",
    "TH": "θ",
    "V": "v",
    "W": "w",
    "Y": "j",
    "Z": "z",
    "ZH": "ʒ",
}

# Trọng âm đổi hẳn phẩm chất nguyên âm, không chỉ đổi độ nhấn
_STRESS_SENSITIVE: dict[str, str] = {
    "AH0": "ə",
    "ER0": "ɚ",
}


class ArpabetMappingError(RuntimeError):
    pass


def to_ipa(arpa: str) -> str | None:
    """→ ký hiệu IPA, hoặc None nếu là token đặc biệt (`<DEL>`, `<unk>`)."""
    token = arpa.strip().upper()
    if token in (DELETION, UNKNOWN.upper(), ""):
        return None
    if token in _STRESS_SENSITIVE:
        return _STRESS_SENSITIVE[token]
    base = token.rstrip("012")
    ipa = _BASE.get(base)
    if ipa is None:
        raise ArpabetMappingError(f"phone ARPAbet lạ: {arpa!r}")
    return ipa


def validate(vocab: dict[str, int]) -> None:
    """Mọi ký hiệu đích phải nằm trong vocab model. Fail fast lúc nạp, như confusion.py.

    Một ký hiệu lạ ở đây không gây exception khi chạy — nó chỉ làm chuỗi chuẩn sai và mọi
    con số tương quan trở nên vô nghĩa mà không có gì báo.
    """
    targets = set(_BASE.values()) | set(_STRESS_SENSITIVE.values())
    missing = sorted(p for p in targets if p not in vocab)
    if missing:
        raise ArpabetMappingError(
            f"{len(missing)} ký hiệu đích không có trong vocab model: {missing}"
        )
