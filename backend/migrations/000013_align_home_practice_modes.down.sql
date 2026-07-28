UPDATE practice_modes
SET title_vi = CASE key
      WHEN 'word' THEN 'Phát âm từ'
      WHEN 'sentence' THEN 'Phát âm câu'
      WHEN 'minimal_pair' THEN 'Cặp âm dễ nhầm'
      WHEN 'shadowing' THEN 'Shadowing'
      WHEN 'flashcard' THEN 'Từ vựng (flashcard)'
      WHEN 'profile' THEN 'Hồ sơ phát âm'
      ELSE title_vi
    END,
    subtitle_vi = CASE key
      WHEN 'word' THEN 'Luyện phát âm từng từ'
      WHEN 'sentence' THEN 'Luyện phát âm theo câu'
      WHEN 'minimal_pair' THEN 'Phân biệt các âm gần giống'
      WHEN 'shadowing' THEN 'Nói nhại theo đoạn mẫu'
      WHEN 'flashcard' THEN 'Học từ vựng với thẻ ghi nhớ'
      WHEN 'profile' THEN 'Xem điểm mạnh/yếu phát âm'
      ELSE subtitle_vi
    END
WHERE key IN ('word', 'sentence', 'minimal_pair', 'shadowing', 'flashcard', 'profile');
