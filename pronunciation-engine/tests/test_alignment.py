from app.engine.alignment import Op, align


def ops(canonical, decoded, sub_cost=None):
    kwargs = {"sub_cost": sub_cost} if sub_cost else {}
    return [p.op for p in align(canonical, decoded, **kwargs)]


def test_identical_sequences_are_all_match():
    assert ops(["θ", "ɹ", "iː"], ["θ", "ɹ", "iː"]) == [Op.MATCH] * 3


def test_substitution_is_detected():
    # "three" đọc thành "tree" — lỗi θ→t kinh điển của người Việt
    pairs = align(["θ", "ɹ", "iː"], ["t", "ɹ", "iː"])
    assert [p.op for p in pairs] == [Op.SUBSTITUTION, Op.MATCH, Op.MATCH]
    assert pairs[0].canon_index == 0
    assert pairs[0].decoded_index == 0


def test_omission_when_phoneme_missing_from_decoded():
    # nuốt phụ âm cuối: "cats" → "cat"
    pairs = align(["k", "æ", "t", "s"], ["k", "æ", "t"])
    assert [p.op for p in pairs] == [Op.MATCH, Op.MATCH, Op.MATCH, Op.OMISSION]
    assert pairs[-1].decoded_index is None
    assert pairs[-1].canon_index == 3


def test_insertion_when_extra_phoneme_decoded():
    # chèn nguyên âm phá cụm phụ âm: "blue" → "bəlue"
    pairs = align(["b", "l", "uː"], ["b", "ə", "l", "uː"])
    assert Op.INSERTION in [p.op for p in pairs]
    ins = next(p for p in pairs if p.op is Op.INSERTION)
    assert ins.canon_index is None
    assert ins.decoded_index == 1


def test_every_canonical_index_appears_exactly_once():
    canonical = ["θ", "ɜː", "t", "iː", "θ", "ɹ", "iː"]
    decoded = ["t", "ɜː", "t", "i", "s", "ɹ", "iː", "ə"]
    pairs = align(canonical, decoded)
    seen = [p.canon_index for p in pairs if p.canon_index is not None]
    assert sorted(seen) == list(range(len(canonical)))


def test_empty_decoded_gives_all_omissions():
    # không nói gì cả
    assert ops(["k", "æ", "t"], []) == [Op.OMISSION] * 3


def test_empty_canonical_gives_all_insertions():
    assert ops([], ["k", "æ"]) == [Op.INSERTION] * 2


def test_near_substitution_preferred_over_omission_plus_insertion():
    """Cặp gần nhau về ngữ âm phải được giải thích bằng 1 substitution.

    Nếu không, NW sẽ tách thành omission + insertion và bảng miscue sẽ báo hai lỗi cho
    một lỗi thật — làm Fix Guide chỉ sai chỗ.
    """

    def near(a: str, b: str) -> float:
        return 0.6 if {a, b} == {"s", "ʃ"} else 1.0

    pairs = align(["s", "iː"], ["ʃ", "iː"], sub_cost=near)
    assert [p.op for p in pairs] == [Op.SUBSTITUTION, Op.MATCH]
