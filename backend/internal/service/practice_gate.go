package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/pkg/apperrors"
)

// PracticeGate là các lớp chặn trước khi nhận một lượt chấm phát âm.
//
// Luồng cũ (/v1/speech/token) có 4 lớp này vì mỗi token là một lần trả tiền cho Azure.
// Luồng mới tự host engine nên KHÔNG mất tiền theo lượt — nhưng ràng buộc chỉ đổi chứ
// không biến mất:
//
//	cũ: tiền   → mỗi lượt tốn ~$0,004
//	mới: dung lượng → mỗi lượt chiếm ~1 giây CPU của engine, và engine chạy 1–2 luồng
//
// Không có gate thì một script có thể đẩy hàng nghìn job, làm nghẽn hàng đợi và khiến
// người dùng thật chờ vô hạn. Và mô hình freemium bị lách sạch.
type PracticeGate struct {
	db  *pgxpool.Pool
	rdb *goredis.Client
	cfg *config.Config
	// inspector đọc độ sâu hàng đợi asynq — thay cho "cost circuit breaker" của luồng cũ.
	inspector *asynq.Inspector
}

// NewPracticeGate tạo gate.
func NewPracticeGate(
	db *pgxpool.Pool, rdb *goredis.Client, cfg *config.Config, inspector *asynq.Inspector,
) *PracticeGate {
	return &PracticeGate{db: db, rdb: rdb, cfg: cfg, inspector: inspector}
}

// Allow kiểm mọi lớp và TIÊU một suất quota nếu qua.
//
// Tiêu quota ngay lúc nhận chứ không đợi chấm xong: một bản ghi hỏng vẫn đã chiếm worker
// và CPU engine. Ngoại lệ duy nhất là lỗi do chất lượng bản ghi — xem [PracticeGate.Refund].
func (g *PracticeGate) Allow(ctx context.Context, userID uuid.UUID, clientIP string) error {
	if err := g.rateLimit(ctx, "user", userID.String(), g.cfg.RateLimit.TokenPerUserPerMin); err != nil {
		return err
	}
	if clientIP != "" {
		if err := g.rateLimit(ctx, "ip", clientIP, g.cfg.RateLimit.TokenPerIPPerMin); err != nil {
			return err
		}
	}
	if err := g.checkQueueDepth(ctx); err != nil {
		return err
	}
	return g.consumeQuota(ctx, userID)
}

// rateLimit đếm theo cửa sổ một phút.
func (g *PracticeGate) rateLimit(ctx context.Context, kind, id string, limit int) error {
	if limit <= 0 {
		return nil
	}
	key := fmt.Sprintf("rl:assess:%s:%s", kind, id)
	count, err := g.rdb.Incr(ctx, key).Result()
	if err != nil {
		// Redis hỏng KHÔNG được chặn người dùng thật. Rate limit là lớp bảo vệ, không
		// phải điều kiện đúng đắn — mất nó thì ghi log rồi cho qua.
		slog.Warn("rate limit không kiểm được, cho qua", "kind", kind, "err", err)
		return nil
	}
	if count == 1 {
		g.rdb.Expire(ctx, key, time.Minute)
	}
	if int(count) > limit {
		return apperrors.New(429, "bạn gửi quá nhanh, thử lại sau ít giây", apperrors.ErrRateLimited)
	}
	return nil
}

// checkQueueDepth từ chối khi hàng đợi đã quá sâu.
//
// Thay cho "cost circuit breaker" của luồng cũ. Với engine tự host, tín hiệu quá tải
// không phải hoá đơn mà là độ trễ: nhận thêm job khi hàng đợi đã dài chỉ khiến MỌI người
// chờ lâu hơn. Từ chối sớm để client biết mà thử lại, tốt hơn là nhận rồi treo.
func (g *PracticeGate) checkQueueDepth(ctx context.Context) error {
	if g.inspector == nil || g.cfg.RateLimit.MaxQueueDepth <= 0 {
		return nil
	}
	info, err := g.inspector.GetQueueInfo("critical")
	if err != nil {
		slog.Warn("không đọc được độ sâu hàng đợi, cho qua", "err", err)
		return nil
	}
	depth := info.Pending + info.Active
	if depth > g.cfg.RateLimit.MaxQueueDepth {
		slog.Warn("hàng đợi quá tải, từ chối job mới", "depth", depth)
		return apperrors.New(503, "hệ thống đang bận, vui lòng thử lại sau",
			apperrors.ErrServiceUnavail)
	}
	return nil
}

// consumeQuota kiểm và tăng bộ đếm freemium theo ngày.
//
// Dùng CHUNG khoá Redis với luồng cũ (`quota:free:<user>:<ngày>`) — nếu tách khoá thì
// người dùng có thể xen kẽ hai luồng để được gấp đôi hạn mức.
func (g *PracticeGate) consumeQuota(ctx context.Context, userID uuid.UUID) error {
	var plan string
	err := g.db.QueryRow(ctx,
		`SELECT plan FROM subscriptions WHERE user_id = $1 AND status = 'active'`,
		userID).Scan(&plan)
	if err == nil && plan != "free" {
		return nil // gói trả phí không giới hạn
	}

	key := quotaKey(userID)
	count, err := g.rdb.Incr(ctx, key).Result()
	if err != nil {
		slog.Warn("quota không kiểm được, cho qua", "err", err)
		return nil
	}
	if count == 1 {
		// Hết ngày UTC thì reset. Đặt TTL theo thời gian còn lại của ngày chứ không
		// phải 24h cứng, nếu không mốc reset sẽ trôi dần theo lần dùng đầu tiên.
		g.rdb.Expire(ctx, key, untilEndOfDayUTC())
	}
	if int(count) > g.cfg.Freemium.DailyLimit {
		return apperrors.New(402,
			"bạn đã dùng hết lượt luyện tập hôm nay, nâng cấp Premium để tiếp tục",
			apperrors.ErrQuotaExceeded)
	}
	return nil
}

// Refund hoàn lại một suất quota.
//
// Gọi khi job hỏng vì CHẤT LƯỢNG BẢN GHI (quá ngắn, không có tiếng) — theo quy tắc
// nghiệp vụ BR-SCORE-07: bản ghi hỏng không bị trừ điểm, và cũng không nên bị trừ lượt.
// Người học đã không nhận được gì thì không có lý do gì mất lượt.
//
// KHÔNG hoàn cho lỗi hệ thống đã retry thành công, và không hoàn quá 0.
func (g *PracticeGate) Refund(ctx context.Context, userID uuid.UUID) {
	var plan string
	err := g.db.QueryRow(ctx,
		`SELECT plan FROM subscriptions WHERE user_id = $1 AND status = 'active'`,
		userID).Scan(&plan)
	if err == nil && plan != "free" {
		return
	}

	key := quotaKey(userID)
	count, err := g.rdb.Decr(ctx, key).Result()
	if err != nil {
		slog.Warn("hoàn quota thất bại", "user_id", userID, "err", err)
		return
	}
	if count < 0 {
		g.rdb.Set(ctx, key, 0, untilEndOfDayUTC())
	}
}

// QuotaUsage returns the counter used by the admission gate. Reporting quota
// from a different database table makes the API show available turns even
// after the gate has started rejecting the user.
func (g *PracticeGate) QuotaUsage(ctx context.Context, userID uuid.UUID) (int, error) {
	count, err := g.rdb.Get(ctx, quotaKey(userID)).Int()
	if err == goredis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("đọc quota: %w", err)
	}
	return count, nil
}

func quotaKey(userID uuid.UUID) string {
	return fmt.Sprintf("quota:free:%s:%s", userID, time.Now().UTC().Format("2006-01-02"))
}

func untilEndOfDayUTC() time.Duration {
	now := time.Now().UTC()
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.UTC)
	if d := endOfDay.Sub(now); d > 0 {
		return d
	}
	return time.Minute
}

// ErrQuotaRefundable cho biết lỗi này đáng hoàn lượt.
func ErrQuotaRefundable(errorCode string) bool {
	switch errorCode {
	case "audio_too_short", "audio_too_long", "no_speech_detected":
		return true
	default:
		return false
	}
}
