package tts

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AzureProvider gọi Azure Neural TTS qua REST.
//
// Dùng REST chứ không SDK: một lời gọi HTTP không đáng để kéo cả SDK vào, và cũng giữ
// cho việc đổi sang nhà cung cấp khác chỉ là thay một struct.
type AzureProvider struct {
	key    string
	region string
	voice  string
	http   *http.Client
}

// NewAzure tạo provider. voice rỗng thì dùng giọng mặc định.
func NewAzure(key, region, voice string) *AzureProvider {
	if voice == "" {
		voice = DefaultVoice
	}
	return &AzureProvider{
		key:    key,
		region: region,
		voice:  voice,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *AzureProvider) Name() string { return "azure" }

// Synthesize gọi endpoint TTS và trả về MP3.
func (a *AzureProvider) Synthesize(ctx context.Context, text, locale string) (*Audio, error) {
	return a.SynthesizeVoice(ctx, text, locale, a.voice)
}

// SynthesizeVoice tổng hợp bằng giọng chỉ định.
func (a *AzureProvider) SynthesizeVoice(
	ctx context.Context, text, locale, voice string,
) (*Audio, error) {
	if a.key == "" || a.region == "" {
		return nil, ErrNoProvider
	}
	if locale == "" {
		locale = "en-US"
	}

	if voice == "" {
		voice = a.voice
	}
	body, err := buildSSML(locale, voice, text)
	if err != nil {
		return nil, fmt.Errorf("dựng SSML: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", a.region)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("dựng request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", a.key)
	req.Header.Set("Content-Type", "application/ssml+xml")
	// MP3 16 kHz: nhỏ, ExoPlayer phía client phát trực tiếp được, và 16 kHz là đủ cho
	// giọng nói — cao hơn chỉ tốn băng thông mà tai không phân biệt.
	req.Header.Set("X-Microsoft-OutputFormat", "audio-16khz-32kbitrate-mono-mp3")
	req.Header.Set("User-Agent", "phonara-backend")

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gọi Azure TTS: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes))
	if err != nil {
		return nil, fmt.Errorf("đọc audio: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Azure TTS trả HTTP %d: %s", resp.StatusCode, truncate(data, 200))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("Azure TTS trả audio rỗng")
	}

	return &Audio{
		Data:      data,
		MimeType:  "audio/mpeg",
		Extension: "mp3",
		Voice:     voice,
		Provider:  a.Name(),
	}, nil
}

// ssml là cấu trúc SSML tối thiểu mà Azure yêu cầu.
//
// Dựng bằng encoding/xml thay vì nối chuỗi: câu luyện tập đến từ CSDL và có thể chứa
// `&`, `<`, dấu nháy — nối chuỗi sẽ sinh XML hỏng và Azure trả 400 cho đúng những câu đó.
type ssml struct {
	XMLName xml.Name  `xml:"speak"`
	Version string    `xml:"version,attr"`
	Lang    string    `xml:"xml:lang,attr"`
	Voice   ssmlVoice `xml:"voice"`
}

type ssmlVoice struct {
	Name string `xml:"name,attr"`
	Text string `xml:",chardata"`
}

func buildSSML(locale, voice, text string) ([]byte, error) {
	return xml.Marshal(ssml{
		Version: "1.0",
		Lang:    locale,
		Voice:   ssmlVoice{Name: voice, Text: text},
	})
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

const (
	// DefaultVoice — giọng nữ Mỹ neural, rõ và trung tính. Đổi được qua AZURE_TTS_VOICE.
	DefaultVoice = "en-US-AriaNeural"

	maxAudioBytes = 8 << 20
)
