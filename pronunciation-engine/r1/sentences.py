"""20 câu pre-assessment, copy nguyên văn từ backend/cmd/seed/main.go.

Nếu seed đổi thì file này phải đổi theo — R1 mất giá trị nếu kiểm trên tập câu khác
với tập câu người dùng thật sự đọc.
"""

SEED_SENTENCES: list[str] = [
    "The weather is nice today.",
    "I think this is great.",
    "She sells seashells by the seashore.",
    "Could you please repeat that question?",
    "Our team launched the product last month.",
    "Thank you very much for your help.",
    "The thirty-three thirsty travelers thrived.",
    "We really enjoy learning new languages.",
    "Please leave the blue glass on the table.",
    "Victor wore a very warm vest.",
    "The rice is on the right shelf.",
    "I packed my bag and walked to class.",
    "George chose an orange jacket.",
    "This ship will leave in fifteen minutes.",
    "Put the full spoon near the blue bowl.",
    "The black cat jumped over the cup.",
    "Three friends finished their work early.",
    "She watched six short videos yesterday.",
    "Would you like to order some fresh fruit?",
    "The world needs better communication.",
    # Ba câu phủ âm /ʒ/, thêm sau khi inventory.py phát hiện 20 câu trên không có âm này.
    "Usually I measure my own progress.",
    "The decision was a pleasure to make.",
    "She has a clear vision for the garage.",
]

# Âm tiếng Anh khó với người học L2 nói chung (§6.1). Danh sách này ban đầu chọn theo lỗi
# của người Việt, nhưng phần lớn trùng với âm khó của mọi tiếng mẹ đẻ — `θ ð` gần như vắng
# mặt ngoài tiếng Anh, `ʒ` hiếm, `ɹ` và `l` là cặp gây khó cho hàng loạt L1 khác nhau.
#
# Engine không phụ thuộc tiếng mẹ đẻ (xem docstring của app/engine/confusion.py), nên đây
# là danh sách âm cần PHỦ trong câu kiểm, không phải hồ sơ lỗi của một nhóm người học.
#
# R1 kiểm riêng xem chúng có nằm trong vocab không — nếu một âm mục tiêu là OOV thì toàn bộ
# tính năng Fix Guide cho âm đó vô nghĩa.
TARGET_PHONEMES: list[str] = [
    "θ", "ð", "ʃ", "ʒ", "tʃ", "dʒ", "z", "v", "s", "l", "n", "ɹ", "f",
]
