-- ============================================================
-- Migration: 000002_assessment
-- Onboarding Pre-Assessment question bank (FR-ONB)
-- ============================================================

-- A versioned bundle of assessment questions. Designed to support
-- multiple sets and CEFR levels (A1, A2, B1…) so new placement or
-- pre-assessment bundles can be added without schema changes.
CREATE TABLE assessment_sets (
  id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  code         TEXT        NOT NULL,                       -- stable slug, e.g. 'pre_assessment_default'
  type         TEXT        NOT NULL DEFAULT 'pre_assessment'
                 CHECK (type IN ('pre_assessment','placement')),
  title        TEXT        NOT NULL,
  description  TEXT,
  cefr_level   TEXT        CHECK (cefr_level IN ('A1','A2','B1','B2','C1','C2')),  -- optional target band
  locale       TEXT        NOT NULL DEFAULT 'en-US',
  version      INT         NOT NULL DEFAULT 1,
  is_active    BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (code, version)
);
CREATE INDEX idx_assessment_sets_type ON assessment_sets(type) WHERE is_active;
CREATE TRIGGER trg_assessment_sets_updated BEFORE UPDATE ON assessment_sets
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One sentence the user reads aloud during the assessment.
CREATE TABLE assessment_questions (
  id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  set_id            UUID        NOT NULL REFERENCES assessment_sets(id) ON DELETE CASCADE,
  order_index       INT         NOT NULL,                 -- 1-based display order
  text              TEXT        NOT NULL,                 -- sentence to read
  phonetic          TEXT,                                 -- IPA transcription
  sample_audio_url  TEXT,                                 -- reference audio to listen
  expected_duration INT,                                  -- optional, seconds
  difficulty        INT         CHECK (difficulty BETWEEN 1 AND 5),  -- optional 1..5
  is_active         BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (set_id, order_index)
);
CREATE INDEX idx_assessment_questions_set ON assessment_questions(set_id, order_index)
  WHERE is_active;
