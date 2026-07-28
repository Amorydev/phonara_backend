-- =============================================================================
-- Migration: 000008_clear_dead_audio_urls
--
-- Xoá URL audio trỏ tới cdn.phonara.app — CDN đó không tồn tại, trả 404.
--
-- Vì sao cần migration riêng: seed đã ngừng sinh URL bịa, nhưng dữ liệu CŨ vẫn giữ
-- chúng. Và đây đúng là tác hại của URL bịa — với MỌI phép kiểm downstream, một URL
-- chết trông y hệt một URL hợp lệ:
--   · worker tts:batch lọc theo "IS NULL OR = ''" nên bỏ qua, không bao giờ sinh lại
--   · client thấy có URL nên gọi, chờ ~3,6 giây rồi mới biết hỏng
--
-- Đặt về NULL để worker nhặt lại và sinh audio thật.
-- =============================================================================

UPDATE assessment_questions
   SET sample_audio_url = NULL
 WHERE sample_audio_url LIKE 'https://cdn.phonara.app/%';

UPDATE content_items
   SET audio_url_us = NULL
 WHERE audio_url_us LIKE 'https://cdn.phonara.app/%';

UPDATE content_items
   SET audio_url_uk = NULL
 WHERE audio_url_uk LIKE 'https://cdn.phonara.app/%';

UPDATE passage_sentences
   SET native_audio_url = NULL
 WHERE native_audio_url LIKE 'https://cdn.phonara.app/%';
