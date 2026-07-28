package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TTL của bộ đếm quota phải theo THỜI GIAN CÒN LẠI CỦA NGÀY, không phải 24h cứng.
//
// Dùng 24h cứng thì mốc reset trôi dần theo lần dùng đầu tiên: user luyện lúc 23:00 hôm
// nay sẽ được reset lúc 23:00 hôm sau, tức mất gần trọn một ngày. Người dùng sẽ thấy
// "hết lượt" vào những giờ ngẫu nhiên và không hiểu vì sao.
func TestQuotaTTLResetsAtMidnightUTC(t *testing.T) {
	d := untilEndOfDayUTC()
	if d <= 0 {
		t.Fatalf("TTL phải dương, nhận %v", d)
	}
	if d > 24*time.Hour {
		t.Errorf("TTL không được vượt một ngày, nhận %v", d)
	}

	now := time.Now().UTC()
	expected := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.UTC).Sub(now)
	if diff := d - expected; diff > time.Second || diff < -time.Second {
		t.Errorf("TTL = %v, kỳ vọng ~%v (tới cuối ngày UTC)", d, expected)
	}
}

// Chỉ lỗi do CHẤT LƯỢNG BẢN GHI mới được hoàn lượt (BR-SCORE-07).
//
// Hoàn cho lỗi hệ thống sẽ tạo lỗ hổng: script cố tình gây lỗi engine để luyện không
// giới hạn. Không hoàn cho lỗi bản ghi thì phạt người học vì micro kém.
func TestOnlyRecordingFailuresRefundQuota(t *testing.T) {
	refundable := map[string]bool{
		"audio_too_short":    true,
		"audio_too_long":     true,
		"no_speech_detected": true,

		"g2p_failed":       false,
		"model_overloaded": false,
		"internal":         false,
		"":                 false,
		"unknown_code":     false,
	}
	for code, want := range refundable {
		if got := ErrQuotaRefundable(code); got != want {
			t.Errorf("ErrQuotaRefundable(%q) = %v, muốn %v", code, got, want)
		}
	}
}

// Khoá quota phải TRÙNG định dạng của luồng cũ (/v1/speech/token).
//
// Tách khoá thì người dùng xen kẽ hai luồng sẽ được gấp đôi hạn mức — và lỗi đó chỉ lộ
// ra khi có người khai thác, không lộ khi test tính năng.
func TestQuotaKeyMatchesLegacyFormat(t *testing.T) {
	id := mustUUID("11111111-2222-3333-4444-555555555555")
	got := quotaKey(id)
	want := "quota:free:11111111-2222-3333-4444-555555555555:" +
		time.Now().UTC().Format("2006-01-02")
	if got != want {
		t.Errorf("quotaKey = %q, muốn %q", got, want)
	}
}

func mustUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return id
}
