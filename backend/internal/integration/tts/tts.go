// Package tts sinh audio mẫu cho câu luyện tập.
//
// KHÔNG dùng espeak-ng dù nó đã có sẵn trong image pronunciation-engine và miễn phí.
// Lý do có số liệu: espeak là bộ tổng hợp formant, và khi cho chính model nhận dạng âm vị
// nghe lại thì
//
//	espeak nói "three cats" → model nghe "f ɹ iː k æ t"
//	espeak nói "tree cats"  → model nghe "θ ɹ iː k æ t z"
//
// tức /θ/ của nó nghe như /f/, còn cụm /tɹ/ lại nghe thành /θɹ/. Với một ứng dụng luyện
// phát âm, phát audio mẫu như vậy là **dạy sai** — tệ hơn hẳn việc không có audio.
//
// Nguồn chấp nhận được: Azure Neural TTS (20 câu ≈ 800 ký tự, nằm gọn trong free tier
// 500.000 ký tự/tháng) hoặc giọng người thu thật.
package tts

import (
	"context"
	"errors"
)

// ErrNoProvider khi chưa cấu hình nhà cung cấp TTS nào.
//
// Gặp lỗi này thì để sample_audio_url **rỗng**, tuyệt đối không bịa ra một URL. URL chết
// tệ hơn URL trống: client không phân biệt được nếu không tốn một vòng mạng ~3,6 giây,
// rồi mới biết là hỏng.
var ErrNoProvider = errors.New("chưa cấu hình nhà cung cấp TTS")

// Audio là kết quả tổng hợp.
type Audio struct {
	Data      []byte
	MimeType  string
	Extension string
	// Voice và Provider được lưu cùng bản ghi để biết audio cũ sinh bằng giọng nào —
	// đổi giọng giữa chừng mà không truy được sẽ tạo ra bộ mẫu không đồng nhất.
	Voice    string
	Provider string
}

// Provider tổng hợp giọng nói từ văn bản.
type Provider interface {
	// Synthesize trả audio cho text. Locale dạng BCP-47 ("en-US").
	Synthesize(ctx context.Context, text, locale string) (*Audio, error)

	// SynthesizeVoice như Synthesize nhưng chỉ định giọng cụ thể.
	//
	// Cần thiết vì một câu phải sinh được cả giọng Mỹ lẫn giọng Anh — không thể cố định
	// một giọng cho cả provider.
	SynthesizeVoice(ctx context.Context, text, locale, voice string) (*Audio, error)

	// Name để ghi vào bản ghi và log.
	Name() string
}

// NoopProvider dùng khi chưa cấu hình gì. Luôn trả [ErrNoProvider].
//
// Tồn tại để worker không cần kiểm nil ở mọi chỗ, và để trạng thái "chưa có TTS" là một
// đường đi tường minh chứ không phải nhánh quên xử lý.
type NoopProvider struct{}

func (NoopProvider) Synthesize(context.Context, string, string) (*Audio, error) {
	return nil, ErrNoProvider
}

func (NoopProvider) SynthesizeVoice(
	context.Context, string, string, string,
) (*Audio, error) {
	return nil, ErrNoProvider
}

func (NoopProvider) Name() string { return "none" }
