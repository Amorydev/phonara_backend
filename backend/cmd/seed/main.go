package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/config"
	storedb "github.com/phonara/backend/internal/store/db"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	pool, err := storedb.NewPool(ctx, cfg.DB)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	slog.Info("running seed...")

	if err := seedPreAssessment(ctx, pool); err != nil {
		slog.Error("seed pre-assessment", "err", err)
		os.Exit(1)
	}

	if err := seedDailyChallenges(ctx, pool); err != nil {
		slog.Error("seed daily challenges", "err", err)
		os.Exit(1)
	}

	if err := seedPracticeModes(ctx, pool); err != nil {
		slog.Error("seed practice modes", "err", err)
		os.Exit(1)
	}

	// TODO: implement seed runners for l1_error_tags, minimal_pairs, fix_guides
	slog.Info("seed complete")
}

// seedNS namespaces deterministic seed UUIDs so re-running upserts the same rows.
var seedNS = uuid.NewSHA1(uuid.NameSpaceURL, []byte("phonara.seed"))

// seedID derives a stable UUID for a given seed key.
func seedID(name string) uuid.UUID { return uuid.NewSHA1(seedNS, []byte(name)) }

// assessmentQuestion is a row of seed data for the pre-assessment set.
type assessmentQuestion struct {
	order            int
	text             string
	phonetic         string
	sampleAudioURL   string
	expectedDuration int
	difficulty       int
}

// ptrInt returns a pointer for an optional int, treating 0 as "not set".
func ptrInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// seedPreAssessment inserts the default onboarding pre-assessment set and its
// questions. It is idempotent: re-running upserts the set (keyed by code+version)
// and each question (keyed by set_id+order_index).
func seedPreAssessment(ctx context.Context, pool *pgxpool.Pool) error {
	const (
		setCode    = "pre_assessment_default"
		setVersion = 1
		audioBase  = "https://cdn.phonara.app/assessment/pre/"
	)

	var setID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO assessment_sets (code, type, title, description, locale, version)
		 VALUES ($1, 'pre_assessment', $2, $3, 'en-US', $4)
		 ON CONFLICT (code, version) DO UPDATE
		   SET title = EXCLUDED.title,
		       description = EXCLUDED.description,
		       is_active = TRUE
		 RETURNING id`,
		setCode,
		"Pre-Assessment",
		"Đánh giá trình độ phát âm ban đầu trong onboarding.",
		setVersion,
	).Scan(&setID)
	if err != nil {
		return err
	}

	questions := []assessmentQuestion{
		{1, "The weather is nice today.", "/ðə ˈwɛðər ɪz naɪs təˈdeɪ/", audioBase + "q1.mp3", 4, 1},
		{2, "I think this is great.", "/aɪ θɪŋk ðɪs ɪz ɡreɪt/", audioBase + "q2.mp3", 4, 2},
		{3, "She sells seashells by the seashore.", "/ʃi sɛlz ˈsiˌʃɛlz baɪ ðə ˈsiˌʃɔr/", audioBase + "q3.mp3", 6, 3},
		{4, "Could you please repeat that question?", "/kʊd ju pliz rɪˈpit ðæt ˈkwɛstʃən/", audioBase + "q4.mp3", 5, 2},
		{5, "Our team launched the product last month.", "/aʊər tim lɔntʃt ðə ˈprɑdəkt læst mʌnθ/", audioBase + "q5.mp3", 6, 3},
		{6, "Thank you very much for your help.", "/θæŋk ju ˈvɛri mʌtʃ fɔr jʊr hɛlp/", audioBase + "q6.mp3", 4, 2},
		{7, "The thirty-three thirsty travelers thrived.", "/ðə ˈθɜrti θri ˈθɜrsti ˈtrævələrz θraɪvd/", audioBase + "q7.mp3", 7, 5},
		{8, "We really enjoy learning new languages.", "/wi ˈrɪəli ɪnˈdʒɔɪ ˈlɜrnɪŋ nu ˈlæŋɡwɪdʒɪz/", "", 5, 2},
		{9, "Please leave the blue glass on the table.", "/pliz liv ðə blu ɡlæs ɑn ðə ˈteɪbəl/", "", 5, 2},
		{10, "Victor wore a very warm vest.", "/ˈvɪktər wɔr ə ˈvɛri wɔrm vɛst/", "", 5, 3},
		{11, "The rice is on the right shelf.", "/ðə raɪs ɪz ɑn ðə raɪt ʃɛlf/", "", 4, 2},
		{12, "I packed my bag and walked to class.", "/aɪ pækt maɪ bæɡ ænd wɔkt tə klæs/", "", 5, 3},
		{13, "George chose an orange jacket.", "/dʒɔrdʒ tʃoʊz ən ˈɔrɪndʒ ˈdʒækɪt/", "", 5, 3},
		{14, "This ship will leave in fifteen minutes.", "/ðɪs ʃɪp wɪl liv ɪn ˌfɪfˈtin ˈmɪnɪts/", "", 5, 3},
		{15, "Put the full spoon near the blue bowl.", "/pʊt ðə fʊl spun nɪr ðə blu boʊl/", "", 5, 3},
		{16, "The black cat jumped over the cup.", "/ðə blæk kæt dʒʌmpt ˈoʊvər ðə kʌp/", "", 5, 3},
		{17, "Three friends finished their work early.", "/θri frɛndz ˈfɪnɪʃt ðɛr wɜrk ˈɜrli/", "", 6, 4},
		{18, "She watched six short videos yesterday.", "/ʃi wɑtʃt sɪks ʃɔrt ˈvɪdioʊz ˈjɛstərdeɪ/", "", 6, 4},
		{19, "Would you like to order some fresh fruit?", "/wʊd ju laɪk tə ˈɔrdər sʌm frɛʃ frut/", "", 6, 4},
		{20, "The world needs better communication.", "/ðə wɜrld nidz ˈbɛtər kəˌmjunəˈkeɪʃən/", "", 6, 4},
	}

	for _, q := range questions {
		_, err := pool.Exec(ctx,
			`INSERT INTO assessment_questions
			   (set_id, order_index, text, phonetic, sample_audio_url, expected_duration, difficulty)
			 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)
			 ON CONFLICT (set_id, order_index) DO UPDATE
			   SET text = EXCLUDED.text,
			       phonetic = EXCLUDED.phonetic,
			       sample_audio_url = EXCLUDED.sample_audio_url,
			       expected_duration = EXCLUDED.expected_duration,
			       difficulty = EXCLUDED.difficulty,
			       is_active = TRUE`,
			setID, q.order, q.text, q.phonetic, q.sampleAudioURL,
			ptrInt(q.expectedDuration), ptrInt(q.difficulty),
		)
		if err != nil {
			return err
		}
	}

	slog.Info("seeded pre-assessment", "set_id", setID, "questions", len(questions))
	return nil
}

// seedContentItem is one word/sentence row for the content library.
type seedContentItem struct {
	key        string
	typ        string // word | sentence
	text       string
	ipa        string
	audioUS    string
	difficulty int
	focus      []string
}

// seedDailyChallenges seeds a small content library, one shadowing passage, and
// daily challenge bundles for yesterday/today/tomorrow (UTC) so the feature is
// demonstrable across user timezones. Idempotent via deterministic UUIDs.
func seedDailyChallenges(ctx context.Context, pool *pgxpool.Pool) error {
	const audioBase = "https://cdn.phonara.app/content/"

	// 1. Content library (words + sentences) used as challenge items.
	content := []seedContentItem{
		{"word_comfortable", "word", "comfortable", "/ˈkʌmftəbəl/", audioBase + "comfortable.mp3", 3, []string{"ə"}},
		{"sent_restaurant", "sentence", "Could you recommend a good restaurant?", "/kʊd ju ˌrɛkəˈmɛnd ə ɡʊd ˈrɛstrɒnt/", audioBase + "recommend_restaurant.mp3", 2, []string{"r"}},
		{"sent_meeting", "sentence", "I'd like to schedule a meeting tomorrow.", "/aɪd laɪk tə ˈskɛdʒul ə ˈmitɪŋ təˈmɒroʊ/", audioBase + "schedule_meeting.mp3", 3, []string{"dʒ"}},
	}
	contentID := make(map[string]uuid.UUID, len(content))
	for _, c := range content {
		id := seedID("content:" + c.key)
		contentID[c.key] = id
		focus := c.focus
		if focus == nil {
			focus = []string{}
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO content_items (id, type, text, ipa, audio_url_us, difficulty, focus_phonemes, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			 ON CONFLICT (id) DO UPDATE
			   SET type = EXCLUDED.type, text = EXCLUDED.text, ipa = EXCLUDED.ipa,
			       audio_url_us = EXCLUDED.audio_url_us, difficulty = EXCLUDED.difficulty,
			       focus_phonemes = EXCLUDED.focus_phonemes, is_active = TRUE`,
			id, c.typ, c.text, c.ipa, c.audioUS, c.difficulty, focus)
		if err != nil {
			return err
		}
	}

	// 2. One shadowing passage + its sentences.
	passageID := seedID("passage:morning_routine")
	passageSentences := []struct {
		order int
		text  string
		ipa   string
		audio string
	}{
		{1, "Every morning I wake up at six.", "/ˈɛvri ˈmɔrnɪŋ aɪ weɪk ʌp æt sɪks/", audioBase + "morning1.mp3"},
		{2, "I make a cup of coffee and read the news.", "/aɪ meɪk ə kʌp əv ˈkɔfi ænd rid ðə nuz/", audioBase + "morning2.mp3"},
		{3, "Then I get ready and head to work.", "/ðɛn aɪ ɡɛt ˈrɛdi ænd hɛd tə wɜrk/", audioBase + "morning3.mp3"},
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO shadowing_passages (id, title, source, topic, difficulty, sentence_count, is_active)
		 VALUES ($1, $2, 'curated', $3, $4, $5, TRUE)
		 ON CONFLICT (id) DO UPDATE
		   SET title = EXCLUDED.title, topic = EXCLUDED.topic, difficulty = EXCLUDED.difficulty,
		       sentence_count = EXCLUDED.sentence_count, is_active = TRUE`,
		passageID, "Morning Routine", "daily_life", 2, len(passageSentences))
	if err != nil {
		return err
	}
	for _, s := range passageSentences {
		_, err := pool.Exec(ctx,
			`INSERT INTO passage_sentences (id, passage_id, order_index, text, ipa, native_audio_url)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (passage_id, order_index) DO UPDATE
			   SET text = EXCLUDED.text, ipa = EXCLUDED.ipa, native_audio_url = EXCLUDED.native_audio_url`,
			seedID(fmt.Sprintf("passage:morning_routine:s%d", s.order)), passageID, s.order, s.text, s.ipa, s.audio)
		if err != nil {
			return err
		}
	}

	// 3. Daily challenge bundles for D-1, D, D+1 (UTC) — covers every timezone.
	type item struct {
		kind       string
		contentKey string // set for word/sentence
		passage    bool   // true for the passage item
	}
	bundle := []item{
		{kind: "word", contentKey: "word_comfortable"},
		{kind: "sentence", contentKey: "sent_restaurant"},
		{kind: "sentence", contentKey: "sent_meeting"},
		{kind: "passage", passage: true},
	}

	for _, offset := range []int{-1, 0, 1} {
		date := time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
		challengeID := seedID("daily:" + date)
		_, err := pool.Exec(ctx,
			`INSERT INTO daily_challenges (id, date, title, description, category, banner_url, moderated)
			 VALUES ($1, $2, $3, $4, $5, $6, TRUE)
			 ON CONFLICT (id) DO UPDATE
			   SET date = EXCLUDED.date, title = EXCLUDED.title, description = EXCLUDED.description,
			       category = EXCLUDED.category, banner_url = EXCLUDED.banner_url, moderated = TRUE`,
			challengeID, date,
			"Thử thách phát âm hằng ngày",
			"Luyện một bộ từ, câu và đoạn ngắn để giữ streak.",
			"pronunciation",
			"https://cdn.phonara.app/daily/banner.png",
		)
		if err != nil {
			return err
		}

		for i, it := range bundle {
			order := i + 1
			var cID, pID *uuid.UUID
			if it.passage {
				p := passageID
				pID = &p
			} else {
				c := contentID[it.contentKey]
				cID = &c
			}
			_, err := pool.Exec(ctx,
				`INSERT INTO daily_challenge_items (id, challenge_id, order_index, kind, content_item_id, passage_id)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (challenge_id, order_index) DO UPDATE
				   SET kind = EXCLUDED.kind, content_item_id = EXCLUDED.content_item_id,
				       passage_id = EXCLUDED.passage_id`,
				seedID(fmt.Sprintf("daily:%s:item%d", date, order)), challengeID, order, it.kind, cID, pID)
			if err != nil {
				return err
			}
		}
		slog.Info("seeded daily challenge", "date", date, "items", len(bundle))
	}

	return nil
}

// seedPracticeModes seeds the 6 Home practice-mode cards. Idempotent on key.
func seedPracticeModes(ctx context.Context, pool *pgxpool.Pool) error {
	type mode struct {
		key, title, subtitle, icon, route string
		premium                           bool
	}
	modes := []mode{
		{"word", "Phát âm từ", "Luyện phát âm từng từ", "ic_word", "/practice/word", false},
		{"sentence", "Phát âm câu", "Luyện phát âm theo câu", "ic_sentence", "/practice/sentence", false},
		{"minimal_pair", "Cặp âm dễ nhầm", "Phân biệt các âm gần giống", "ic_minimal_pair", "/minimal-pairs", false},
		{"shadowing", "Shadowing", "Nói nhại theo đoạn mẫu", "ic_shadowing", "/shadowing", false},
		{"flashcard", "Từ vựng (flashcard)", "Học từ vựng với thẻ ghi nhớ", "ic_flashcard", "/vocabulary", false},
		{"profile", "Hồ sơ phát âm", "Xem điểm mạnh/yếu phát âm", "ic_profile", "/coach/profile", false},
	}

	for i, m := range modes {
		_, err := pool.Exec(ctx,
			`INSERT INTO practice_modes (key, title_vi, subtitle_vi, icon, route, is_premium, order_index, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			 ON CONFLICT (key) DO UPDATE
			   SET title_vi = EXCLUDED.title_vi, subtitle_vi = EXCLUDED.subtitle_vi,
			       icon = EXCLUDED.icon, route = EXCLUDED.route, is_premium = EXCLUDED.is_premium,
			       order_index = EXCLUDED.order_index, is_active = TRUE`,
			m.key, m.title, m.subtitle, m.icon, m.route, m.premium, i+1)
		if err != nil {
			return err
		}
	}

	slog.Info("seeded practice modes", "count", len(modes))
	return nil
}
