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
]

# Âm mục tiêu của người học Việt (§6.1). R1 kiểm riêng xem chúng có nằm trong vocab không
# — nếu một âm mục tiêu là OOV thì toàn bộ tính năng Fix Guide cho âm đó vô nghĩa.
VI_TARGET_PHONEMES: list[str] = [
    "θ", "ð", "ʃ", "ʒ", "tʃ", "dʒ", "z", "v", "s", "l", "n", "ɹ", "f",
]
