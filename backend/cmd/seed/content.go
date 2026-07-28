package main

// Nội dung dạy phát âm: nhãn loại lỗi, hướng dẫn sửa theo âm vị, và cặp âm dễ nhầm.
//
// Ba bảng này trước đây được ĐỌC nhưng chưa bao giờ được GHI, nên `GET /v1/content/fix-guide`
// luôn rơi vào nhánh fallback `is_simplified` và `GET /v1/content/minimal-pairs` luôn rỗng.
//
// KHÓA CHỌN NỘI DUNG LÀ `phoneme`, KHÔNG PHẢI TIẾNG MẸ ĐẺ. `ContentService.GetFixGuide`
// lọc theo `phoneme = $1`, và âm vị đó đến từ `top_errors` — tức lỗi ĐO ĐƯỢC của chính
// người học. Xem §6.1.0 của PRONUNCIATION_ENGINE_PLAN.md: app không hỏi tiếng mẹ đẻ.
//
// Vì vậy ký hiệu âm vị ở đây phải khớp CHÍNH XÁC vocab espeak mà engine sinh ra — `ɡ` là
// U+0261 (chữ g một tầng của IPA) chứ không phải `g` bàn phím, `ɹ` chứ không phải `r`.
// Lệch một ký tự thì hướng dẫn im lặng không bao giờ được tìm thấy. `TestFixGuidePhonemes
// MatchEngineVocab` chặn đúng chuyện đó.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Nhãn loại lỗi ─────────────────────────────────────────────────────────────

// l1ErrorTag là một KIỂU lỗi phát âm, không phải một tiếng mẹ đẻ.
//
// Tên bảng `l1_error_tags` có từ trước và giữ nguyên để không phải migration, nhưng ngữ
// nghĩa là "kiểu lỗi thường gặp ở người học L2" — dùng để nhóm nội dung, không dùng để
// suy ra người học nói tiếng gì.
type l1ErrorTag struct {
	code, nameVI, descVI string
	importance           int
}

// importance dùng để xếp thứ tự ưu tiên khi một người mắc nhiều kiểu lỗi cùng lúc.
// Thang 1–10; cao hơn = ảnh hưởng khả năng NGHE HIỂU nhiều hơn, không phải "sai nhiều hơn".
var l1ErrorTags = []l1ErrorTag{
	{
		"final_consonant_deletion", "Nuốt phụ âm cuối",
		"Bỏ hẳn phụ âm ở cuối từ: “cat” thành “ca”, “find” thành “fine”. " +
			"Đây là lỗi làm người nghe hiểu sai từ nhiều nhất, vì phụ âm cuối tiếng Anh " +
			"mang thông tin ngữ pháp: số nhiều, thì quá khứ, sở hữu.", 10,
	},
	{
		"th_stopping", "Thay /θ/ và /ð/ bằng /t/, /d/ hoặc /s/",
		"“think” thành “tink” hoặc “sink”, “this” thành “dis”. Hai âm này gần như không " +
			"tồn tại ngoài tiếng Anh nên hầu hết người học đều mắc.", 8,
	},
	{
		"final_devoicing", "Mất rung ở phụ âm cuối",
		"“is” thành “iss”, “bed” thành “bet”, “bag” thành “back”. Nguyên âm trước phụ âm " +
			"hữu thanh cũng dài hơn, nên mất rung làm từ nghe ngắn và cụt.", 7,
	},
	{
		"consonant_cluster_reduction", "Giản lược cụm phụ âm",
		"“street” thành “sit”, “asked” thành “ask”. Người học chèn thêm nguyên âm hoặc bỏ " +
			"bớt phụ âm để cụm dễ đọc hơn.", 7,
	},
	{
		"vowel_length", "Không phân biệt nguyên âm dài và ngắn",
		"“sheep” và “ship”, “fool” và “full” nghe như nhau. Khác biệt nằm ở cả độ dài lẫn " +
			"độ căng của lưỡi, không chỉ độ dài.", 7,
	},
	{
		"r_l_confusion", "Lẫn /ɹ/ và /l/",
		"“rice” thành “lice”, “road” thành “load”. Hai âm khác nhau ở chỗ /l/ chạm lợi còn " +
			"/ɹ/ thì KHÔNG chạm vào đâu cả.", 6,
	},
	{
		"l_n_confusion", "Lẫn /l/ và /n/",
		"“light” thành “night”. Cả hai đều chạm lợi; khác nhau ở chỗ /n/ cho hơi thoát qua " +
			"mũi còn /l/ cho hơi thoát hai bên lưỡi.", 6,
	},
	{
		"sibilant_confusion", "Lẫn các âm xuýt /s/, /ʃ/, /tʃ/",
		"“sip”, “ship”, “chip” nghe như nhau. Khác biệt nằm ở vị trí lưỡi lùi dần và độ " +
			"tròn môi tăng dần.", 6,
	},
	{
		"v_b_confusion", "Thay /v/ bằng /b/",
		"“van” thành “ban”. /v/ dùng RĂNG chạm môi dưới; /b/ dùng hai môi. Nhiều người học " +
			"phát /v/ thành âm hai môi vì tiếng mẹ đẻ không có /v/.", 5,
	},
	{
		"affricate_confusion", "Lẫn /tʃ/ và /dʒ/",
		"“cheap” và “jeep”. Cùng vị trí lưỡi, chỉ khác có rung dây thanh hay không.", 5,
	},
	{
		"zh_missing", "Thiếu âm /ʒ/",
		"“measure” thành “me-sure” hoặc “me-dger”. Âm này hiếm trong tiếng Anh và gần như " +
			"không có trong nhiều tiếng khác, nên thường bị thay bằng /ʃ/ hoặc /dʒ/.", 4,
	},
}

func seedL1ErrorTags(ctx context.Context, pool *pgxpool.Pool) error {
	for _, tag := range l1ErrorTags {
		if _, err := pool.Exec(ctx,
			`INSERT INTO l1_error_tags (code, name_vi, description_vi, importance, version, is_active)
			 VALUES ($1, $2, $3, $4, 1, TRUE)
			 ON CONFLICT (code, version) DO UPDATE
			   SET name_vi = EXCLUDED.name_vi,
			       description_vi = EXCLUDED.description_vi,
			       importance = EXCLUDED.importance,
			       is_active = TRUE,
			       updated_at = now()`,
			tag.code, tag.nameVI, tag.descVI, tag.importance,
		); err != nil {
			return fmt.Errorf("nhãn lỗi %q: %w", tag.code, err)
		}
	}
	slog.Info("seeded l1 error tags", "count", len(l1ErrorTags))
	return nil
}

// ── Hướng dẫn sửa theo âm vị ──────────────────────────────────────────────────

type fixGuide struct {
	phoneme  string // ký hiệu espeak, phải khớp vocab engine
	tagCode  string // "" nếu không gắn nhãn lỗi nào
	tongueVI string
	examples []string
}

// fixGuides — hướng dẫn cho từng âm vị.
//
// `tongueVI` mô tả CÁCH ĐẶT LƯỠI VÀ MÔI, không phải "hãy phát âm cho đúng". Mỗi mục cố
// gắng nêu rõ lỗi thay thế phổ biến và điểm khác biệt cụ thể so với âm đó, vì người học
// không nghe ra khác biệt thì có lặp lại bao nhiêu lần cũng không sửa được.
var fixGuides = []fixGuide{
	{
		"θ", "th_stopping",
		"Đặt ĐẦU LƯỠI chạm nhẹ vào rìa răng cửa trên, hở một khe nhỏ rồi thổi hơi ra đều. " +
			"Dây thanh KHÔNG rung — đặt tay lên cổ họng phải thấy im. " +
			"Nếu lưỡi chạm lợi (phần thịt sau răng) thì ra /t/; nếu lưỡi không ra tới răng thì ra /s/.",
		[]string{"think", "three", "bath", "month", "healthy"},
	},
	{
		"ð", "th_stopping",
		"Vị trí lưỡi giống hệt /θ/ — đầu lưỡi chạm rìa răng cửa trên — nhưng dây thanh RUNG. " +
			"Đặt tay lên cổ họng, phải thấy rung suốt âm. " +
			"Đây là âm của những từ hay gặp nhất: the, this, that, they.",
		[]string{"this", "they", "mother", "breathe", "weather"},
	},
	{
		"ʃ", "sibilant_confusion",
		"Kéo lưỡi LÙI về sau so với /s/, nâng phần giữa lưỡi lên gần vòm miệng, và CHU MÔI " +
			"tròn nhẹ. Luồng hơi rộng và trầm hơn /s/. " +
			"Mẹo: phát /s/ rồi từ từ kéo lưỡi lùi, bạn sẽ nghe âm chuyển thành /ʃ/.",
		[]string{"ship", "she", "wash", "nation", "fish"},
	},
	{
		"ʒ", "zh_missing",
		"Vị trí giống hệt /ʃ/ nhưng dây thanh RUNG. " +
			"Âm này gần như không bao giờ đứng đầu từ trong tiếng Anh — nó nằm giữa hoặc " +
			"cuối từ. Đừng thêm /d/ vào trước, vì /d/ + /ʒ/ sẽ thành /dʒ/ (“measure” hoá “major”).",
		[]string{"measure", "vision", "usually", "decision", "garage"},
	},
	{
		"tʃ", "affricate_confusion",
		"Một âm duy nhất, không phải hai âm rời: chặn hơi bằng lưỡi ở vị trí /t/, rồi thả ra " +
			"thành /ʃ/ liền mạch. Dây thanh KHÔNG rung. " +
			"So với /ʃ/: /ʃ/ chảy liên tục, còn /tʃ/ có một cú bật ở đầu.",
		[]string{"chair", "cheap", "watch", "teacher", "much"},
	},
	{
		"dʒ", "affricate_confusion",
		"Giống /tʃ/ về vị trí và cách bật, nhưng dây thanh RUNG từ đầu. " +
			"Đây là âm của chữ “j” và chữ “g” mềm.",
		[]string{"jeep", "job", "bridge", "large", "manager"},
	},
	{
		"z", "final_devoicing",
		"Vị trí lưỡi giống /s/ — đầu lưỡi gần lợi, hơi đi qua khe hẹp — nhưng dây thanh RUNG. " +
			"Ở CUỐI TỪ rất hay bị mất rung thành /s/. Kiểm tra: nguyên âm trước /z/ phải dài " +
			"hơn nguyên âm trước /s/ (“buzz” dài hơn “bus”).",
		[]string{"zoo", "is", "buzz", "these", "rise"},
	},
	{
		"v", "v_b_confusion",
		"Đặt RĂNG CỬA TRÊN chạm nhẹ lên MÔI DƯỚI, rồi cho hơi lách qua và rung dây thanh. " +
			"Hai môi KHÔNG được chạm nhau — chạm nhau là ra /b/. " +
			"Giữ hơi chảy liên tục; /v/ kéo dài được, /b/ thì không.",
		[]string{"van", "very", "love", "give", "seven"},
	},
	{
		"f", "",
		"Răng cửa trên chạm môi dưới giống /v/, nhưng dây thanh KHÔNG rung — chỉ có hơi. " +
			"Ở cuối từ nhớ giữ hơi đủ dài, đừng cắt cụt.",
		[]string{"fan", "five", "life", "enough", "coffee"},
	},
	{
		"s", "sibilant_confusion",
		"Đầu lưỡi nâng gần lợi (không chạm), tạo khe rất hẹp cho hơi đi qua — âm cao và sắc. " +
			"Dây thanh không rung. " +
			"So với /ʃ/: lưỡi ở PHÍA TRƯỚC hơn và môi KHÔNG tròn.",
		[]string{"sip", "see", "bus", "class", "August"},
	},
	{
		"l", "l_n_confusion",
		"Đầu lưỡi chạm chắc vào lợi sau răng cửa trên, hơi thoát ra HAI BÊN lưỡi. " +
			"Ở CUỐI TỪ (“full”, “call”) lưỡi vẫn phải chạm lợi và phần sau lưỡi nâng lên — " +
			"đây là chỗ hay bị nuốt thành nguyên âm.",
		[]string{"light", "long", "full", "call", "believe"},
	},
	{
		"n", "l_n_confusion",
		"Đầu lưỡi chạm lợi giống /l/, nhưng hơi đi qua MŨI. " +
			"Cách tự kiểm: bịt mũi lại, nếu vẫn phát được thì đó là /l/ chứ không phải /n/.",
		[]string{"night", "no", "run", "green", "any"},
	},
	{
		"ɹ", "r_l_confusion",
		"Lưỡi cong lên hoặc co lại nhưng TUYỆT ĐỐI KHÔNG chạm vào vòm miệng, môi hơi tròn. " +
			"Không rung lưỡi và không nảy lưỡi — /ɹ/ tiếng Anh khác hẳn âm “r” rung của nhiều " +
			"ngôn ngữ khác. " +
			"So với /l/: /l/ CHẠM lợi, /ɹ/ KHÔNG chạm gì cả.",
		[]string{"rice", "red", "very", "sorry", "around"},
	},
	{
		"ŋ", "final_consonant_deletion",
		"Nâng phần SAU của lưỡi chạm vòm mềm (giống vị trí /k/), cho hơi qua mũi. " +
			"Đầu lưỡi để yên ở dưới, đừng chạm lợi — chạm lợi sẽ thành /n/. " +
			"Trong “sing” không có âm /ɡ/ ở cuối.",
		[]string{"sing", "long", "thing", "young", "bringing"},
	},
	{
		"t", "final_consonant_deletion",
		"Đầu lưỡi chặn hơi ở lợi rồi BẬT ra. " +
			"Ở cuối từ vẫn phải nghe thấy cú bật hoặc ít nhất một khoảng ngắt — nuốt hẳn sẽ " +
			"làm mất thì quá khứ (“walked” thành “walk”).",
		[]string{"cat", "want", "start", "walked", "night"},
	},
	{
		"d", "final_devoicing",
		"Giống /t/ về vị trí nhưng dây thanh RUNG. " +
			"Ở cuối từ hay bị mất rung thành /t/: “bed” thành “bet”. Nguyên âm trước /d/ dài " +
			"hơn trước /t/ — đó là manh mối chính người bản ngữ dùng để phân biệt.",
		[]string{"bed", "good", "read", "played", "hard"},
	},
	{
		"iː", "vowel_length",
		"Lưỡi đưa cao và ra TRƯỚC, môi kéo ngang như đang cười, giữ âm DÀI và CĂNG. " +
			"Khác /ɪ/ không chỉ ở độ dài mà cả độ căng: /iː/ căng, /ɪ/ lỏng và lưỡi thấp hơn.",
		[]string{"sheep", "feel", "each", "team", "believe"},
	},
	{
		"ɪ", "vowel_length",
		"Ngắn và LỎNG, lưỡi thấp hơn và lùi hơn /iː/. Môi không kéo ngang. " +
			"Đây là nguyên âm của “ship”, “fill”, “it” — nếu kéo dài và căng lên sẽ thành “sheep”.",
		[]string{"ship", "fill", "it", "big", "sister"},
	},
	{
		"æ", "",
		"Mở miệng RỘNG, hạ hàm dưới xuống, lưỡi thấp và ra trước. " +
			"Rộng hơn /ɛ/ trong “bed” khá nhiều — nếu hai từ “bad” và “bed” nghe giống nhau " +
			"thì miệng đang mở chưa đủ.",
		[]string{"bad", "cat", "man", "happy", "have"},
	},
	{
		"uː", "vowel_length",
		"Lưỡi lùi về sau và nâng cao, môi CHU TRÒN rõ, giữ dài. " +
			"Khác /ʊ/ ở độ căng và độ tròn môi: “fool” tròn môi hơn “full”.",
		[]string{"fool", "food", "blue", "school", "through"},
	},
	{
		"ʊ", "vowel_length",
		"Ngắn và lỏng, môi tròn nhẹ thôi. Lưỡi thấp hơn /uː/. " +
			"Nguyên âm của “full”, “book”, “put”.",
		[]string{"full", "book", "put", "good", "would"},
	},
	{
		"ɛ", "vowel_length",
		"Miệng mở vừa, lưỡi ở giữa và hơi ra trước — hẹp hơn /æ/ rõ rệt. " +
			"Nguyên âm của “bed”, “men”. Nếu “bed” và “bad” nghe giống nhau thì đang mở " +
			"miệng quá rộng cho /ɛ/ hoặc quá hẹp cho /æ/.",
		[]string{"bed", "men", "head", "said", "friend"},
	},
	{
		"ɡ", "final_devoicing",
		"Nâng phần SAU của lưỡi chặn hơi ở vòm mềm rồi bật ra, dây thanh RUNG. " +
			"Ở cuối từ hay bị mất rung thành /k/: “bag” thành “back”. Nguyên âm trước /ɡ/ " +
			"dài hơn trước /k/.",
		[]string{"bag", "go", "big", "again", "dog"},
	},
	{
		"k", "final_consonant_deletion",
		"Giống /ɡ/ về vị trí nhưng KHÔNG rung, và bật mạnh hơn. " +
			"Cuối từ vẫn phải nghe thấy cú bật — nuốt hẳn sẽ mất luôn từ (“back” thành “ba”).",
		[]string{"back", "cat", "school", "make", "ask"},
	},
	{
		"b", "final_devoicing",
		"Hai môi mím lại chặn hơi rồi bật ra, dây thanh RUNG. " +
			"Khác /v/: /b/ dùng HAI MÔI và có cú bật; /v/ dùng răng chạm môi và chảy liên tục.",
		[]string{"bad", "book", "cab", "job", "about"},
	},
	{
		"p", "final_consonant_deletion",
		"Hai môi mím lại rồi bật, KHÔNG rung. Đầu từ có luồng hơi bật mạnh — " +
			"để tờ giấy trước miệng, nói “pen” thì giấy phải rung.",
		[]string{"pen", "cap", "stop", "happy", "keep"},
	},
}

func seedFixGuides(ctx context.Context, pool *pgxpool.Pool) error {
	// Nạp id nhãn lỗi để gắn liên kết. Nhãn lỗi phải được seed TRƯỚC.
	tagIDs := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT code, id FROM l1_error_tags WHERE is_active`)
	if err != nil {
		return fmt.Errorf("nạp nhãn lỗi: %w", err)
	}
	for rows.Next() {
		var code, id string
		if err := rows.Scan(&code, &id); err != nil {
			rows.Close()
			return fmt.Errorf("scan nhãn lỗi: %w", err)
		}
		tagIDs[code] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("duyệt nhãn lỗi: %w", err)
	}

	for _, g := range fixGuides {
		if g.tagCode != "" {
			if _, ok := tagIDs[g.tagCode]; !ok {
				return fmt.Errorf("hướng dẫn %q trỏ tới nhãn lỗi %q không tồn tại", g.phoneme, g.tagCode)
			}
		}
		examples, err := json.Marshal(g.examples)
		if err != nil {
			return fmt.Errorf("marshal ví dụ cho %q: %w", g.phoneme, err)
		}

		var tagID *string
		if id, ok := tagIDs[g.tagCode]; ok {
			tagID = &id
		}

		// Không có UNIQUE trên `phoneme` nên upsert bằng khoá tự nhiên: xoá rồi chèn lại
		// trong một transaction. Chạy seed nhiều lần không được nhân bản hướng dẫn.
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("mở transaction: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM fix_guides WHERE phoneme = $1 AND version = 1`, g.phoneme,
		); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("xoá hướng dẫn cũ %q: %w", g.phoneme, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO fix_guides
			   (l1_tag_id, phoneme, tongue_position_vi, examples, version, is_active)
			 VALUES ($1, $2, $3, $4::jsonb, 1, TRUE)`,
			tagID, g.phoneme, g.tongueVI, string(examples),
		); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("chèn hướng dẫn %q: %w", g.phoneme, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit hướng dẫn %q: %w", g.phoneme, err)
		}
	}

	slog.Info("seeded fix guides", "count", len(fixGuides))
	return nil
}

// ── Cặp âm dễ nhầm ────────────────────────────────────────────────────────────

type minimalPair struct {
	wordA, wordB       string
	phonemeA, phonemeB string
	explainVI          string
	tagCode            string
	category           string
	difficulty         int
	isFree             bool
}

// minimalPairs — cặp từ chỉ khác nhau MỘT âm vị.
//
// Ràng buộc: hai từ phải khác nhau đúng một âm và giống nhau ở mọi âm còn lại. Cặp không
// thoả (ví dụ “measure” / “major” khác cả nguyên âm) sẽ dạy sai chỗ cần nghe.
// `TestMinimalPairsDifferInExactlyOnePhoneme` không kiểm được điều đó bằng chuỗi chữ viết,
// nên các cặp dưới đây được chọn thủ công theo phiên âm.
var minimalPairs = []minimalPair{
	// θ / ð — nhóm quan trọng nhất
	{"think", "sink", "θ", "s", "“think” đặt lưỡi giữa hai răng; “sink” lưỡi ở sau răng.", "th_stopping", "fricative", 1, true},
	{"thin", "tin", "θ", "t", "“thin” lưỡi chạm RĂNG và hơi chảy liên tục; “tin” lưỡi chạm LỢI và bật ra.", "th_stopping", "fricative", 1, true},
	{"three", "tree", "θ", "t", "Cặp kinh điển. “three” hơi phải chảy qua khe răng–lưỡi.", "th_stopping", "fricative", 2, true},
	{"they", "day", "ð", "d", "“they” lưỡi chạm răng, hơi chảy; “day” lưỡi chạm lợi, bật ra.", "th_stopping", "fricative", 1, true},
	{"breathe", "breed", "ð", "d", "Cuối từ: “breathe” hơi vẫn chảy, “breed” chặn rồi bật.", "th_stopping", "fricative", 3, false},

	// s / ʃ / tʃ
	{"sip", "ship", "s", "ʃ", "“ship” lưỡi lùi hơn và môi tròn hơn.", "sibilant_confusion", "fricative", 1, true},
	{"see", "she", "s", "ʃ", "Nghe độ trầm: /ʃ/ trầm hơn /s/.", "sibilant_confusion", "fricative", 1, true},
	{"ship", "chip", "ʃ", "tʃ", "“chip” có một cú BẬT ở đầu; “ship” chảy đều từ đầu.", "sibilant_confusion", "affricate", 2, false},
	{"share", "chair", "ʃ", "tʃ", "Cùng vị trí lưỡi, khác ở cú bật mở đầu.", "affricate_confusion", "affricate", 2, false},
	{"cheap", "jeep", "tʃ", "dʒ", "Chỉ khác ở dây thanh: “jeep” rung ngay từ đầu.", "affricate_confusion", "affricate", 2, false},

	// ʒ
	{"Confucian", "confusion", "ʃ", "ʒ", "“confusion” rung dây thanh ở âm giữa.", "zh_missing", "fricative", 3, false},
	{"leisure", "ledger", "ʒ", "dʒ", "“ledger” có cú bật /d/ trước; “leisure” chảy liên tục.", "zh_missing", "affricate", 3, false},

	// s / z và mất rung cuối từ
	{"Sue", "zoo", "s", "z", "Đầu từ: “zoo” rung dây thanh ngay từ âm đầu.", "final_devoicing", "fricative", 1, true},
	{"rice", "rise", "s", "z", "Cuối từ: nguyên âm trước /z/ DÀI hơn — đó là manh mối chính.", "final_devoicing", "final_consonant", 2, true},
	{"bus", "buzz", "s", "z", "“buzz” rung và nguyên âm trước dài hơn.", "final_devoicing", "final_consonant", 2, false},

	// f / v / b
	{"fan", "van", "f", "v", "Cùng vị trí răng–môi, chỉ khác dây thanh có rung hay không.", "v_b_confusion", "fricative", 1, true},
	{"van", "ban", "v", "b", "“van” dùng RĂNG chạm môi; “ban” dùng HAI MÔI chạm nhau.", "v_b_confusion", "fricative", 1, true},
	{"vest", "best", "v", "b", "Kiểm tra: /v/ kéo dài được, /b/ thì không.", "v_b_confusion", "fricative", 2, false},

	// l / n / ɹ
	{"light", "night", "l", "n", "Bịt mũi thử: “night” sẽ phát không được.", "l_n_confusion", "liquid", 1, true},
	{"lice", "nice", "l", "n", "Cùng chạm lợi, khác đường hơi thoát: hai bên lưỡi hay qua mũi.", "l_n_confusion", "liquid", 2, false},
	{"rice", "lice", "ɹ", "l", "“lice” lưỡi CHẠM lợi; “rice” lưỡi KHÔNG chạm vào đâu.", "r_l_confusion", "liquid", 1, true},
	{"road", "load", "ɹ", "l", "Cùng nguyên tắc: chạm hay không chạm.", "r_l_confusion", "liquid", 2, false},
	{"grass", "glass", "ɹ", "l", "Trong cụm phụ âm còn khó nghe hơn.", "r_l_confusion", "liquid", 3, false},

	// nguyên âm dài / ngắn
	{"sheep", "ship", "iː", "ɪ", "“sheep” dài và CĂNG; “ship” ngắn và lỏng.", "vowel_length", "vowel", 1, true},
	{"feel", "fill", "iː", "ɪ", "Nghe độ căng của lưỡi, không chỉ độ dài.", "vowel_length", "vowel", 2, true},
	{"fool", "full", "uː", "ʊ", "“fool” môi tròn rõ và dài hơn.", "vowel_length", "vowel", 2, false},
	{"pool", "pull", "uː", "ʊ", "Cùng nguyên tắc tròn môi và độ căng.", "vowel_length", "vowel", 2, false},
	{"bad", "bed", "æ", "ɛ", "“bad” mở miệng rộng hơn hẳn.", "vowel_length", "vowel", 1, true},
	{"man", "men", "æ", "ɛ", "Hạ hàm xuống cho “man”.", "vowel_length", "vowel", 2, false},

	// phụ âm cuối
	{"bag", "back", "ɡ", "k", "Cuối từ: “bag” rung, nguyên âm trước dài hơn.", "final_devoicing", "final_consonant", 2, true},
	{"bed", "bet", "d", "t", "Nghe độ dài nguyên âm trước phụ âm cuối.", "final_devoicing", "final_consonant", 2, true},
	{"cab", "cap", "b", "p", "Cùng hai môi, khác dây thanh.", "final_devoicing", "final_consonant", 2, false},
	{"sing", "sin", "ŋ", "n", "“sing” lưỡi SAU chạm vòm mềm; “sin” đầu lưỡi chạm lợi.", "final_consonant_deletion", "final_consonant", 2, false},
	{"thin", "thing", "n", "ŋ", "Cùng cặp, đảo vị trí để luyện nghe hai chiều.", "final_consonant_deletion", "final_consonant", 3, false},
}

func seedMinimalPairs(ctx context.Context, pool *pgxpool.Pool) error {
	tagIDs := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT code, id FROM l1_error_tags WHERE is_active`)
	if err != nil {
		return fmt.Errorf("nạp nhãn lỗi: %w", err)
	}
	for rows.Next() {
		var code, id string
		if err := rows.Scan(&code, &id); err != nil {
			rows.Close()
			return fmt.Errorf("scan nhãn lỗi: %w", err)
		}
		tagIDs[code] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("duyệt nhãn lỗi: %w", err)
	}

	for _, p := range minimalPairs {
		if _, ok := tagIDs[p.tagCode]; !ok {
			return fmt.Errorf("cặp %q/%q trỏ tới nhãn lỗi %q không tồn tại",
				p.wordA, p.wordB, p.tagCode)
		}
		tagID := tagIDs[p.tagCode]

		// Khoá tự nhiên là (word_a, word_b, version) nhưng bảng không có UNIQUE, nên upsert
		// thủ công. Cột audio_* KHÔNG bị đụng tới — chúng do worker TTS sinh ra, và ghi đè
		// bằng NULL sẽ khiến audio đã tạo bị mất mỗi lần chạy lại seed.
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("mở transaction: %w", err)
		}

		var id string
		err = tx.QueryRow(ctx,
			`SELECT id FROM minimal_pairs WHERE word_a = $1 AND word_b = $2 AND version = 1`,
			p.wordA, p.wordB).Scan(&id)
		switch {
		case err == nil:
			_, err = tx.Exec(ctx,
				`UPDATE minimal_pairs
				    SET phoneme_a = $2, phoneme_b = $3, explain_vi = $4, l1_tag_id = $5,
				        category = $6, difficulty = $7, is_free = $8, is_active = TRUE,
				        updated_at = now()
				  WHERE id = $1`,
				id, p.phonemeA, p.phonemeB, p.explainVI, tagID, p.category, p.difficulty, p.isFree)
		default:
			_, err = tx.Exec(ctx,
				`INSERT INTO minimal_pairs
				   (word_a, word_b, phoneme_a, phoneme_b, explain_vi, l1_tag_id,
				    category, difficulty, is_free, version, is_active)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,TRUE)`,
				p.wordA, p.wordB, p.phonemeA, p.phonemeB, p.explainVI, tagID,
				p.category, p.difficulty, p.isFree)
		}
		if err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("ghi cặp %q/%q: %w", p.wordA, p.wordB, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit cặp %q/%q: %w", p.wordA, p.wordB, err)
		}
	}

	slog.Info("seeded minimal pairs", "count", len(minimalPairs))
	return nil
}

// ── Huy hiệu ──────────────────────────────────────────────────────────────────

type badge struct {
	code, nameVI, descVI, iconURL string
	criteriaType                  string
	criteriaValue                 int
}

// badges — mốc thành tích.
//
// `criteria_type` phải nằm trong CHECK của bảng: streak, phoneme_mastered, items_done,
// pairs_mastered. Cả bốn giờ đều có nguồn dữ liệu thật (xem `ERROR_PROFILE.md`); trước khi
// có `phoneme_mastery` và `pair_mastery` thì hai loại sau không bao giờ đạt được.
//
// Mốc đặt dày ở đầu rồi thưa dần: người mới cần thấy tiến bộ sớm, người đã gắn bó thì mốc
// xa mới còn ý nghĩa.
var badgeList = []badge{
	{"streak_3", "Ba ngày liên tiếp", "Luyện tập 3 ngày liên tiếp.", "ic_badge_streak_3", "streak", 3},
	{"streak_7", "Một tuần bền bỉ", "Luyện tập 7 ngày liên tiếp.", "ic_badge_streak_7", "streak", 7},
	{"streak_30", "Một tháng không nghỉ", "Luyện tập 30 ngày liên tiếp.", "ic_badge_streak_30", "streak", 30},
	{"streak_100", "Trăm ngày kiên trì", "Luyện tập 100 ngày liên tiếp.", "ic_badge_streak_100", "streak", 100},

	{"items_10", "Khởi động", "Hoàn thành 10 lượt luyện.", "ic_badge_items_10", "items_done", 10},
	{"items_50", "Vào guồng", "Hoàn thành 50 lượt luyện.", "ic_badge_items_50", "items_done", 50},
	{"items_200", "Chăm chỉ", "Hoàn thành 200 lượt luyện.", "ic_badge_items_200", "items_done", 200},
	{"items_1000", "Nghìn lượt", "Hoàn thành 1.000 lượt luyện.", "ic_badge_items_1000", "items_done", 1000},

	{"phoneme_5", "Năm âm vững", "Đạt mức thành thạo ở 5 âm vị.", "ic_badge_phoneme_5", "phoneme_mastered", 5},
	{"phoneme_10", "Mười âm vững", "Đạt mức thành thạo ở 10 âm vị.", "ic_badge_phoneme_10", "phoneme_mastered", 10},
	{"phoneme_20", "Hai mươi âm vững", "Đạt mức thành thạo ở 20 âm vị.", "ic_badge_phoneme_20", "phoneme_mastered", 20},

	{"pairs_5", "Tai thính", "Phân biệt thành thạo 5 cặp âm dễ nhầm.", "ic_badge_pairs_5", "pairs_mastered", 5},
	{"pairs_15", "Tai rất thính", "Phân biệt thành thạo 15 cặp âm dễ nhầm.", "ic_badge_pairs_15", "pairs_mastered", 15},
}

func seedBadges(ctx context.Context, pool *pgxpool.Pool) error {
	for _, b := range badgeList {
		// `criteria_ref` để chuỗi rỗng chứ KHÔNG để NULL: `BadgeService.List` quét cột này
		// vào `string` không phải con trỏ, nên một dòng NULL sẽ làm cả endpoint /v1/badges
		// lỗi. Cùng lý do với `description_vi` và `icon_url`.
		if _, err := pool.Exec(ctx,
			`INSERT INTO badges
			   (code, name_vi, description_vi, icon_url, criteria_type, criteria_value,
			    criteria_ref, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, '', TRUE)
			 ON CONFLICT (code) DO UPDATE
			   SET name_vi = EXCLUDED.name_vi,
			       description_vi = EXCLUDED.description_vi,
			       icon_url = EXCLUDED.icon_url,
			       criteria_type = EXCLUDED.criteria_type,
			       criteria_value = EXCLUDED.criteria_value,
			       criteria_ref = '',
			       is_active = TRUE`,
			b.code, b.nameVI, b.descVI, b.iconURL, b.criteriaType, b.criteriaValue,
		); err != nil {
			return fmt.Errorf("huy hiệu %q: %w", b.code, err)
		}
	}
	slog.Info("seeded badges", "count", len(badgeList))
	return nil
}

// ── Văn bản pháp lý và gói thuê bao — BẢN GIỮ CHỖ ─────────────────────────────

// placeholderBanner mở đầu mọi văn bản giữ chỗ.
//
// Ghi bằng tiếng Việt và đặt ngay dòng đầu để nó hiện ra trên màn hình app, không nằm khuất
// trong comment. Người xem thử app phải thấy ngay đây chưa phải văn bản thật.
const placeholderBanner = "> ⚠️ **BẢN GIỮ CHỖ — CHƯA CÓ HIỆU LỰC PHÁP LÝ.**\n" +
	"> Nội dung này chỉ để màn hình chạy được trong lúc phát triển.\n" +
	"> **Phải thay bằng văn bản do luật sư soạn trước khi phát hành.**\n\n"

// seedLegalDocuments nạp bản giữ chỗ cho điều khoản và chính sách riêng tư.
//
// KHÔNG tự soạn văn bản pháp lý thật: điều khoản dịch vụ và chính sách riêng tư ràng buộc
// trách nhiệm pháp lý, phải do luật sư viết và phải phản ánh đúng việc app thực sự làm gì
// với dữ liệu người dùng — riêng app này còn lưu **bản ghi giọng nói**, thuộc dữ liệu sinh
// trắc học ở nhiều pháp lý khác nhau.
//
// Nhưng để bảng rỗng thì `GET /v1/legal/:doc_type` báo lỗi và app không hiển thị nổi màn
// hình điều khoản — chặn cả việc nộp lên store. Bản giữ chỗ có cảnh báo rõ ràng giải quyết
// được thế kẹt đó mà không giả vờ là văn bản thật.
func seedLegalDocuments(ctx context.Context, pool *pgxpool.Pool) error {
	docs := []struct {
		docType, body string
	}{
		{"terms", "# Điều khoản sử dụng\n\n" + placeholderBanner +
			"## 1. Phạm vi\n\nPhonara là ứng dụng luyện phát âm tiếng Anh.\n\n" +
			"## 2. Tài khoản\n\nNgười dùng chịu trách nhiệm bảo mật thông tin đăng nhập.\n\n" +
			"## 3. Nội dung ghi âm\n\nỨng dụng ghi âm giọng nói để chấm phát âm. " +
			"Chi tiết về việc lưu trữ và xoá xem trong Chính sách quyền riêng tư.\n\n" +
			"## 4. Thanh toán\n\nCác gói trả phí được xử lý qua App Store hoặc Google Play.\n\n" +
			"## 5. Liên hệ\n\n(chưa điền)\n"},
		{"privacy", "# Chính sách quyền riêng tư\n\n" + placeholderBanner +
			"## 1. Dữ liệu thu thập\n\n" +
			"- Thông tin tài khoản: email.\n" +
			"- **Bản ghi giọng nói**: dùng để chấm phát âm.\n" +
			"- Dữ liệu luyện tập: điểm số, tiến độ.\n\n" +
			"## 2. Bản ghi giọng nói\n\nNgười dùng có thể tắt việc lưu bản ghi trong phần " +
			"cài đặt quyền riêng tư. Khi xoá tài khoản, bản ghi bị xoá khỏi kho lưu trữ.\n\n" +
			"## 3. Quyền của người dùng\n\nXem, tải về và xoá dữ liệu cá nhân.\n\n" +
			"## 4. Liên hệ\n\n(chưa điền)\n"},
	}

	for _, d := range docs {
		if _, err := pool.Exec(ctx,
			`INSERT INTO legal_documents (doc_type, version, content_md, locale, published_at)
			 VALUES ($1, 1, $2, 'vi', now())
			 ON CONFLICT (doc_type, version, locale) DO UPDATE
			   SET content_md = EXCLUDED.content_md, published_at = now()`,
			d.docType, d.body,
		); err != nil {
			return fmt.Errorf("văn bản %q: %w", d.docType, err)
		}
	}
	slog.Warn("seeded legal documents — BẢN GIỮ CHỖ, phải thay trước khi phát hành",
		"count", len(docs))
	return nil
}

// seedPlanConfigs nạp bản giữ chỗ cho các gói trả phí.
//
// `product_id_ios` và `product_id_android` phải KHỚP CHÍNH XÁC mã sản phẩm khai trong App
// Store Connect và Google Play Console. Mã ở đây là giữ chỗ — sai mã thì thanh toán thất
// bại ở đúng bước người dùng đã quyết định trả tiền.
//
// Giá cũng là giữ chỗ. Đặt sẵn để màn hình gói cước dựng được layout thật thay vì danh
// sách rỗng.
func seedPlanConfigs(ctx context.Context, pool *pgxpool.Pool) error {
	plans := []struct {
		plan, idIOS, idAndroid, period, displayVI string
		priceVND                                  int
		features                                  []string
	}{
		{
			"premium", "PLACEHOLDER.phonara.premium.monthly",
			"PLACEHOLDER_phonara_premium_monthly", "monthly", "Premium hàng tháng", 99000,
			[]string{"Không giới hạn lượt chấm mỗi ngày", "Toàn bộ cặp âm dễ nhầm",
				"Hồ sơ lỗi chi tiết", "Lộ trình luyện cá nhân hoá"},
		},
		{
			"premium", "PLACEHOLDER.phonara.premium.yearly",
			"PLACEHOLDER_phonara_premium_yearly", "yearly", "Premium hàng năm", 899000,
			[]string{"Mọi quyền lợi của gói tháng", "Tiết kiệm so với trả theo tháng"},
		},
		{
			"exam_pack", "PLACEHOLDER.phonara.exampack",
			"PLACEHOLDER_phonara_exampack", "one_time", "Gói luyện thi", 249000,
			[]string{"Đề luyện thi nói", "Báo cáo theo tiêu chí"},
		},
	}

	for _, p := range plans {
		features, err := json.Marshal(p.features)
		if err != nil {
			return fmt.Errorf("marshal quyền lợi gói %q: %w", p.plan, err)
		}
		// Khoá tự nhiên là (plan, billing_period) nhưng bảng không có UNIQUE — xoá rồi chèn
		// để chạy lại seed không nhân bản gói.
		if _, err := pool.Exec(ctx,
			`DELETE FROM plan_configs WHERE plan = $1 AND billing_period = $2`,
			p.plan, p.period,
		); err != nil {
			return fmt.Errorf("xoá gói cũ %q: %w", p.plan, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO plan_configs
			   (plan, product_id_ios, product_id_android, billing_period, price_vnd,
			    display_name_vi, features_vi, is_active)
			 VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,TRUE)`,
			p.plan, p.idIOS, p.idAndroid, p.period, p.priceVND, p.displayVI, string(features),
		); err != nil {
			return fmt.Errorf("gói %q: %w", p.plan, err)
		}
	}
	slog.Warn("seeded plan configs — MÃ SẢN PHẨM LÀ GIỮ CHỖ, phải thay bằng mã thật ở store",
		"count", len(plans))
	return nil
}
