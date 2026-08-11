package english

import "testing"

func TestEvaluateTranscript(t *testing.T) {
	accuracy, wpm, score, issues := EvaluateTranscript("We learn English every day", "We learn every day day", 3)
	if accuracy <= 0 || accuracy >= 1 {
		t.Fatalf("准确率应介于 0 和 1: %v", accuracy)
	}
	if wpm != 100 {
		t.Fatalf("WPM=%v, want 100", wpm)
	}
	if score <= 0 {
		t.Fatalf("score=%v", score)
	}
	if len(issues) != 2 {
		t.Fatalf("issues=%+v", issues)
	}
}

func TestRecommendDifficultyDoesNotPunishMissingWork(t *testing.T) {
	p := ProgressSnapshot{LessonsAssigned: 20, LessonsCompleted: 0, CompletionRate4W: 0}
	if got := RecommendDifficulty(3, p); got != 3 {
		t.Fatalf("未完成不应降级: %d", got)
	}
	p = ProgressSnapshot{LessonsAssigned: 10, LessonsCompleted: 9, CompletionRate4W: .9, ReadingAccuracy4W: .8, SpeakingAccuracy4W: .75}
	if got := RecommendDifficulty(3, p); got != 4 {
		t.Fatalf("稳定表现应只升一级: %d", got)
	}
}
