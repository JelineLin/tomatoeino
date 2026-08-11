package english

import "testing"

func TestValidateVocabularyRequiresWordInsidePhrase(t *testing.T) {
	valid := []Vocabulary{{
		Phrase: "make steady progress", PhraseMeaning: "稳步进步",
		Word: "progress", Meaning: "进步；进展", Example: "She made steady progress every week.",
	}}
	if err := validateVocabulary(valid); err != nil {
		t.Fatalf("有效短语被拒绝: %v", err)
	}

	invalid := append([]Vocabulary(nil), valid...)
	invalid[0].Phrase = "improve every day"
	if err := validateVocabulary(invalid); err == nil {
		t.Fatal("未包含目标词的短语应被拒绝")
	}

	invalid[0].Phrase = "a fresh start"
	invalid[0].Word = "art"
	if err := validateVocabulary(invalid); err == nil {
		t.Fatal("目标词只作为另一个词的子串时不应通过")
	}
}

func TestPublicLessonBackfillsPhraseForLegacyVocabulary(t *testing.T) {
	lesson := Lesson{Vocabulary: []Vocabulary{{
		Word: "progress", Meaning: "进步", Example: "She made steady progress every week.",
	}, {
		Word: "connect", Meaning: "连接", Example: "It improves access.",
	}}, Passage: "A new railway connects remote communities every day."}
	public := PublicLesson(lesson)
	if public.Vocabulary[0].Phrase != "made steady progress every week." {
		t.Fatalf("旧课程短语提取错误: %q", public.Vocabulary[0].Phrase)
	}
	if lesson.Vocabulary[0].Phrase != "" {
		t.Fatal("兼容展示不应改写账本中的原课程")
	}
	if public.Vocabulary[1].Phrase != "new railway connects remote communities" {
		t.Fatalf("正文词形回填错误: %q", public.Vocabulary[1].Phrase)
	}
}
