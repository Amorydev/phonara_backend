-- =============================================================================
-- Migration: 000009_split_assessment_sets
--
-- Tách bộ câu đánh giá thành hai, vì onboarding và benchmark có yêu cầu NGƯỢC NHAU:
--
--   onboarding : phải NGẮN — 23 câu là ~5 phút đọc, tỉ lệ bỏ ngang cao
--   benchmark  : phải PHỦ ĐỦ — mỗi âm mục tiêu ≥30 lần (§6.1)
--
-- Trước đây một bộ phục vụ cả hai, nên tối ưu cho bên này là làm hỏng bên kia.
--
-- Cột is_default thay cho việc dựa vào version DESC làm thứ tự ưu tiên: dựa version thì
-- một ngày nào đó ai đó bump version bộ benchmark lên và nó lặng lẽ thành bộ onboarding.
-- Ưu tiên phải tường minh.
-- =============================================================================

ALTER TABLE assessment_sets
  ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT FALSE;

-- Chỉ MỘT bộ được làm mặc định cho mỗi type. Ràng buộc ở CSDL chứ không ở code:
-- hai bộ cùng is_default thì truy vấn trả kết quả không xác định, và lỗi đó chỉ lộ ra
-- ngẫu nhiên theo thứ tự hàng.
CREATE UNIQUE INDEX idx_assessment_sets_one_default
  ON assessment_sets(type) WHERE is_default;

-- Bộ hiện có trở thành mặc định cho onboarding.
UPDATE assessment_sets SET is_default = TRUE
 WHERE code = 'pre_assessment_default' AND type = 'pre_assessment';
