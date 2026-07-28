-- =============================================================================
-- Migration: 000007_assessment_engine_neutral
--
-- Chuyển schema chấm phát âm sang trung lập với engine. Trước đây các cột được
-- định hình theo PARawPayload của Azure; nay engine là self-host (alignment-free
-- CTC GOP) và có thể đổi lần nữa, nên schema phải mô tả BẢN CHẤT dữ liệu chứ
-- không mô tả một nhà cung cấp.
--
-- Ba thay đổi:
--   1. assessment_jobs — luồng đảo chiều: client upload audio, server chấm bất đồng bộ
--   2. Dấu vết phiên bản trên mỗi kết quả — không có nó, dữ liệu lịch sử vô nghĩa
--      sau lần đổi model/calibration đầu tiên
--   3. diagnosis tường minh thay cho suy diễn từ chuỗi rỗng
-- =============================================================================

-- ── 1. Job chấm bất đồng bộ ──────────────────────────────────────────────────
-- Inference tốn ~1s; giữ HTTP connection mở suốt thời gian đó làm cạn connection
-- pool khi có tải. Worker + poll cho retry, idempotency và backpressure.
CREATE TABLE assessment_jobs (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id      UUID        REFERENCES practice_sessions(id) ON DELETE CASCADE,

  status          TEXT        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','processing','done','failed')),

  audio_ref       TEXT        NOT NULL,
  reference_text  TEXT        NOT NULL,
  scoring_level   TEXT        NOT NULL DEFAULT 'medium'
                    CHECK (scoring_level IN ('easy','medium','hard')),

  content_item_id        UUID REFERENCES content_items(id) ON DELETE SET NULL,
  minimal_pair_id        UUID REFERENCES minimal_pairs(id) ON DELETE SET NULL,
  assessment_question_id UUID REFERENCES assessment_questions(id) ON DELETE SET NULL,

  -- Gửi lại cùng key trả về job cũ, không tạo job mới. Client mất mạng giữa chừng
  -- rồi thử lại sẽ không tạo bản ghi trùng.
  idempotency_key TEXT        UNIQUE,

  -- error_code lấy từ engine (audio_too_short, no_speech_detected, …). Phân biệt
  -- lỗi vĩnh viễn với lỗi tạm thời để worker biết có nên retry không.
  error_code      TEXT,
  error_message   TEXT,
  attempts        INT         NOT NULL DEFAULT 0,

  -- Kết quả thô của engine, giữ nguyên vẹn. Đây là đường duy nhất điều tra khi
  -- user khiếu nại "sao tôi đọc đúng mà bị chấm sai".
  raw_result      JSONB,

  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_assessment_jobs_user    ON assessment_jobs(user_id, created_at DESC);
CREATE INDEX idx_assessment_jobs_pending ON assessment_jobs(status) WHERE status IN ('pending','processing');
CREATE INDEX idx_assessment_jobs_session ON assessment_jobs(session_id) WHERE session_id IS NOT NULL;

-- ── 2. Dấu vết phiên bản trên kết quả ────────────────────────────────────────
-- Bốn trục version độc lập (xem PRONUNCIATION_ENGINE_PLAN.md §8). Biểu đồ tiến bộ
-- của user PHẢI lọc theo bộ này hoặc tính lại từ gop_raw — nếu không, một lần đổi
-- calibration sẽ tạo bước nhảy giả và user tưởng mình đột nhiên giỏi/dở đi.
ALTER TABLE practice_item_results
  ADD COLUMN engine                 TEXT,
  ADD COLUMN model_version          TEXT,
  ADD COLUMN g2p_version            TEXT,
  ADD COLUMN algorithm_version      TEXT,
  ADD COLUMN calibration_version    TEXT,
  -- Engine khai báo nó ĐO ĐƯỢC gì. Không có cột này, "fluency NULL" không phân
  -- biệt được với "engine không hỗ trợ fluency", và mọi thống kê trộn nhiều đời
  -- engine đều sai âm thầm.
  ADD COLUMN capabilities           TEXT[],
  ADD COLUMN assessment_question_id UUID REFERENCES assessment_questions(id) ON DELETE SET NULL,
  ADD COLUMN assessment_job_id      UUID REFERENCES assessment_jobs(id) ON DELETE SET NULL;

CREATE INDEX idx_pir_assessment_question ON practice_item_results(assessment_question_id)
  WHERE assessment_question_id IS NOT NULL;
CREATE INDEX idx_pir_engine ON practice_item_results(engine, model_version)
  WHERE engine IS NOT NULL;

-- ── 3. Chẩn đoán tường minh ──────────────────────────────────────────────────
-- said_phoneme đã nullable sẵn. Vấn đề là ngữ nghĩa: NULL từng bị code suy diễn
-- thành omission (session.go:273), nên "engine không biết" và "user nuốt âm" bị
-- gộp làm một. Cột diagnosis tách hai thứ đó.
ALTER TABLE phoneme_scores
  ADD COLUMN diagnosis  TEXT
    CHECK (diagnosis IN ('correct','substitution','omission','insertion','uncertain')),
  ADD COLUMN confidence REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
  -- GOP thô chưa calibrate, miền (−∞,+∞). Cho phép tính lại điểm khi đổi bảng
  -- calibration mà KHÔNG cần chạy lại inference trên audio cũ.
  ADD COLUMN gop_raw    REAL;

CREATE INDEX idx_phoneme_scores_diagnosis ON phoneme_scores(expected_phoneme, diagnosis)
  WHERE diagnosis IS NOT NULL;

-- accuracy phải nullable: một âm vị bị NUỐT không có "độ chính xác".
--
-- Schema cũ đặt NOT NULL vì Azure luôn trả một con số cho mọi âm vị, kể cả âm không
-- được phát ra. Điều đó buộc ta phải bịa ra 0 cho omission — mà 0 lại đi vào phép
-- trung bình accuracy và phạt user lần thứ hai cho cùng một lỗi (completeness đã phạt
-- rồi). Cho phép NULL để "không có gì để chấm" khác hẳn với "chấm được 0 điểm".
ALTER TABLE phoneme_scores ALTER COLUMN accuracy DROP NOT NULL;

-- is_omission giữ tạm để không phá query cũ. Bỏ ở migration sau khi không còn
-- chỗ nào đọc nó — diagnosis là nguồn sự thật kể từ đây.
COMMENT ON COLUMN phoneme_scores.is_omission IS
  'DEPRECATED — dùng diagnosis = ''omission''. Giữ tạm cho query cũ.';
