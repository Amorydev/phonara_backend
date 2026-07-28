// Command export-benchmark xuất các lượt chấm đã xong thành bộ dữ liệu để người chấm tay.
//
// Đây là bước 7 của PRONUNCIATION_ENGINE_PLAN.md — cổng quyết định của cả dự án. Cho tới
// khi có số liệu trên giọng người Việt, ta KHÔNG biết engine có dùng được cho người dùng
// thật hay không: speechocean762 là người bản ngữ tiếng Quan Thoại.
//
// Dữ liệu thu qua chính app: mỗi assessment_job đã lưu audio, câu mẫu và kết quả engine.
// Lệnh này gom chúng lại kèm phiếu chấm trống cho người chấm điền.
//
//	go run ./cmd/export-benchmark -out ./benchmark-bundle
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/integration/storage"
	storedb "github.com/phonara/backend/internal/store/db"
)

// Utterance là một lượt đọc, kèm chuỗi âm vị mà engine đã chấm.
type Utterance struct {
	JobID         string `json:"job_id"`
	SpeakerID     string `json:"speaker_id"`
	ReferenceText string `json:"reference_text"`
	AudioFile     string `json:"audio_file"`

	// Engine* giữ nguyên kết quả máy để so với người chấm. KHÔNG hiển thị cho người
	// chấm — thấy điểm máy trước sẽ neo phán đoán của họ và làm hỏng phép đo.
	EnginePhonemes []EnginePhoneme `json:"engine_phonemes"`
}

// EnginePhoneme là một âm vị theo cách engine nhìn.
type EnginePhoneme struct {
	Index     int      `json:"index"`
	Expected  string   `json:"expected"`
	Said      *string  `json:"said"`
	WordIndex int      `json:"word_index"`
	Accuracy  *float64 `json:"accuracy"`
	GOPRaw    float64  `json:"gop_raw"`
	Diagnosis string   `json:"diagnosis"`
	Confid    float64  `json:"confidence"`
}

// LabelRow là phiếu chấm trống cho MỘT người chấm.
type LabelRow struct {
	JobID     string      `json:"job_id"`
	RaterID   string      `json:"rater_id"`
	Reference string      `json:"reference_text"`
	Phonemes  []LabelCell `json:"phonemes"`
}

// LabelCell là ô người chấm điền.
//
// Thang 0/1/2 giống speechocean762 để dùng chung mã phân tích, và vì ba mức là mức chi
// tiết mà người chấm còn giữ được nhất quán — năm mức thì hai người sẽ lệch nhau nhiều
// và đồng thuận tụt.
type LabelCell struct {
	Index    int    `json:"index"`
	Expected string `json:"expected"`
	// Score: 2 = đúng, 1 = nghe được nhưng có accent, 0 = sai. -1 = CHƯA CHẤM.
	Score int `json:"score"`
	// Said: âm người chấm nghe thấy, nếu Score < 2. Để trống nếu không xác định được.
	Said string `json:"said"`
}

func main() {
	out := flag.String("out", "./benchmark-bundle", "thư mục xuất")
	limit := flag.Int("limit", 500, "số lượt tối đa")
	raters := flag.String("raters", "A,B", "danh sách người chấm, phân tách bằng dấu phẩy")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		fail("nạp config", err)
	}
	pool, err := storedb.NewPool(ctx, cfg.DB)
	if err != nil {
		fail("kết nối DB", err)
	}
	defer pool.Close()

	store, err := storage.New(ctx, storage.FactoryConfig{
		Driver:          cfg.S3.Driver,
		LocalRoot:       cfg.Storage.LocalRoot,
		Endpoint:        cfg.S3.Endpoint,
		AccessKey:       cfg.S3.AccessKey,
		SecretKey:       cfg.S3.SecretKey,
		Region:          cfg.S3.Region,
		SampleBucket:    cfg.S3.SampleBucket,
		RecordingBucket: cfg.S3.RecordingBucket,
	})
	if err != nil {
		fail("khởi tạo storage", err)
	}

	audioDir := filepath.Join(*out, "audio")
	if err := os.MkdirAll(audioDir, 0o750); err != nil {
		fail("tạo thư mục", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT id, user_id, reference_text, audio_ref, raw_result
		   FROM assessment_jobs
		  WHERE status = 'done' AND raw_result IS NOT NULL
		  ORDER BY created_at
		  LIMIT $1`, *limit)
	if err != nil {
		fail("truy vấn job", err)
	}
	defer rows.Close()

	var utterances []Utterance
	for rows.Next() {
		var jobID, userID, text, audioRef string
		var raw []byte
		if err := rows.Scan(&jobID, &userID, &text, &audioRef, &raw); err != nil {
			fail("scan job", err)
		}

		var result domain.NormalizedAssessmentResult
		if err := json.Unmarshal(raw, &result); err != nil {
			slog.Warn("bỏ qua job: raw_result hỏng", "job_id", jobID, "err", err)
			continue
		}

		audio, err := store.Get(ctx, audioRef)
		if err != nil {
			slog.Warn("bỏ qua job: không đọc được audio", "job_id", jobID, "err", err)
			continue
		}
		audioFile := jobID + ".wav"
		if err := os.WriteFile(filepath.Join(audioDir, audioFile), audio, 0o640); err != nil {
			fail("ghi audio", err)
		}

		u := Utterance{
			JobID:         jobID,
			SpeakerID:     userID,
			ReferenceText: text,
			AudioFile:     audioFile,
		}
		for i, p := range result.Phonemes {
			if p.Diagnosis == domain.DiagInsertion {
				continue // âm thừa không có trong chuỗi chuẩn, người chấm không đối chiếu được
			}
			u.EnginePhonemes = append(u.EnginePhonemes, EnginePhoneme{
				Index: i, Expected: p.Expected, Said: p.Said,
				WordIndex: p.WordIndex, Accuracy: p.Accuracy, GOPRaw: p.GOPRaw,
				Diagnosis: string(p.Diagnosis), Confid: p.Confidence,
			})
		}
		utterances = append(utterances, u)
	}

	if err := writeJSONL(filepath.Join(*out, "manifest.jsonl"), utterances); err != nil {
		fail("ghi manifest", err)
	}

	// Mỗi người chấm một file riêng: chấm ĐỘC LẬP là điều kiện để tính được đồng thuận.
	// Chung một file thì người sau nhìn thấy điểm người trước và phép đo mất ý nghĩa.
	for _, rater := range splitCSV(*raters) {
		var labels []LabelRow
		for _, u := range utterances {
			row := LabelRow{JobID: u.JobID, RaterID: rater, Reference: u.ReferenceText}
			for _, p := range u.EnginePhonemes {
				row.Phonemes = append(row.Phonemes, LabelCell{
					Index: p.Index, Expected: p.Expected, Score: -1,
				})
			}
			labels = append(labels, row)
		}
		path := filepath.Join(*out, fmt.Sprintf("labels_%s.jsonl", rater))
		if err := writeJSONL(path, labels); err != nil {
			fail("ghi phiếu chấm", err)
		}
		slog.Info("đã tạo phiếu chấm", "rater", rater, "file", path)
	}

	slog.Info("xuất xong",
		"lượt", len(utterances),
		"âm_vị", countPhonemes(utterances),
		"thư_mục", *out)
	fmt.Println("\nBước tiếp theo: đọc " + filepath.Join(*out, "..", "BENCHMARK_PROTOCOL.md"))
}

func countPhonemes(us []Utterance) int {
	n := 0
	for _, u := range us {
		n += len(u.EnginePhonemes)
	}
	return n
}

func writeJSONL[T any](path string, items []T) error {
	f, err := os.Create(path) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	enc := json.NewEncoder(f)
	for _, it := range items {
		if err := enc.Encode(it); err != nil {
			return err
		}
	}
	return nil
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		if r != ' ' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func fail(what string, err error) {
	slog.Error(what, "err", err)
	os.Exit(1)
}
