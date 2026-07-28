package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AwardBadges trao mọi huy hiệu người học đã đạt nhưng chưa nhận.
//
// Trước đây `user_badges` KHÔNG có đường ghi nào — `GET /v1/badges` trả toàn bộ huy hiệu ở
// mục "chưa mở khoá" vĩnh viễn, kể cả khi người học đã vượt xa mọi mốc.
//
// Tính bằng MỘT câu lệnh thay vì đọc hết huy hiệu rồi so trong Go: điều kiện đạt phụ thuộc
// dữ liệu đang nằm sẵn trong CSDL, nên để Postgres so là vừa ít vòng gọi vừa không có khe
// hở giữa lúc đọc và lúc ghi.
//
// `ON CONFLICT DO NOTHING` khiến hàm này idempotent: gọi lại không đổi `earned_at`, nên
// ngày nhận huy hiệu không bị nhảy mỗi lần luyện.
func AwardBadges(ctx context.Context, db *pgxpool.Pool, userID uuid.UUID) error {
	// Mỗi CTE tính MỘT chỉ số. Người chưa có dữ liệu ở một chỉ số nào đó thì COALESCE cho
	// 0 — không có dòng nghĩa là chưa đạt, không phải lỗi.
	const query = `
WITH stat AS (
  SELECT
    COALESCE((SELECT current_streak FROM streak_records WHERE user_id = $1), 0)      AS streak,
    COALESCE((SELECT count(*) FROM practice_item_results pir
                JOIN practice_sessions s ON pir.session_id = s.id
               WHERE s.user_id = $1), 0)                                             AS items_done,
    COALESCE((SELECT count(*) FROM phoneme_mastery pm
                JOIN error_profiles ep ON pm.error_profile_id = ep.id
               WHERE ep.user_id = $1 AND pm.status = 'good'), 0)                     AS phoneme_mastered,
    COALESCE((SELECT count(*) FROM pair_mastery pr
                JOIN error_profiles ep ON pr.error_profile_id = ep.id
               WHERE ep.user_id = $1 AND pr.status = 'good'), 0)                     AS pairs_mastered
)
INSERT INTO user_badges (user_id, badge_id)
SELECT $1, b.id
  FROM badges b, stat
 WHERE b.is_active
   AND b.criteria_value <= CASE b.criteria_type
         WHEN 'streak'           THEN stat.streak
         WHEN 'items_done'       THEN stat.items_done
         WHEN 'phoneme_mastered' THEN stat.phoneme_mastered
         WHEN 'pairs_mastered'   THEN stat.pairs_mastered
         -- Loại tiêu chí lạ KHÔNG được trao. Thêm loại mới vào CHECK của bảng mà quên sửa
         -- đây thì huy hiệu đó im lặng không bao giờ mở — trả -1 để nó không bao giờ ≤ mốc.
         ELSE -1
       END
ON CONFLICT (user_id, badge_id) DO NOTHING`

	tag, err := db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("trao huy hiệu: %w", err)
	}
	if n := tag.RowsAffected(); n > 0 {
		slog.Info("đã trao huy hiệu", "user_id", userID, "số_lượng", n)
	}
	return nil
}
