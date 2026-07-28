-- Keep Home practice-mode copy and ordering aligned with the refined Home design.
UPDATE practice_modes
SET title_vi = CASE key
      WHEN 'word' THEN 'Phát âm từ'
      WHEN 'sentence' THEN 'Phát âm câu'
      WHEN 'minimal_pair' THEN 'Cặp âm dễ nhầm'
      WHEN 'shadowing' THEN 'Shadowing'
      WHEN 'flashcard' THEN 'Từ vựng'
      WHEN 'profile' THEN 'Hồ sơ phát âm'
      ELSE title_vi
    END,
    subtitle_vi = CASE key
      WHEN 'flashcard' THEN 'Ôn tập Flashcard'
      WHEN 'word' THEN NULL
      WHEN 'sentence' THEN NULL
      WHEN 'minimal_pair' THEN NULL
      WHEN 'shadowing' THEN NULL
      WHEN 'profile' THEN NULL
      ELSE subtitle_vi
    END,
    order_index = CASE key
      WHEN 'word' THEN 1
      WHEN 'sentence' THEN 2
      WHEN 'minimal_pair' THEN 3
      WHEN 'shadowing' THEN 4
      WHEN 'flashcard' THEN 5
      WHEN 'profile' THEN 6
      ELSE order_index
    END
WHERE key IN ('word', 'sentence', 'minimal_pair', 'shadowing', 'flashcard', 'profile');
