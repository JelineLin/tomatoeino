package english

// RecommendDifficulty 把升降级权收回程序。模型可以解释理由，但不能像未经复核的
// 资金指令一样直接改账：未完成不算退步，每周最多只动一级。
func RecommendDifficulty(current int, p ProgressSnapshot) int {
	if current < 1 {
		current = 1
	}
	if current > 5 {
		current = 5
	}
	next := current
	if p.LessonsCompleted >= 8 && p.CompletionRate4W >= .8 && p.ReadingAccuracy4W >= .75 && p.SpeakingAccuracy4W >= .7 {
		next++
	} else if p.LessonsCompleted >= 3 && p.ReadingAccuracy4W > 0 && p.ReadingAccuracy4W < .6 {
		next--
	}
	if next < 1 {
		return 1
	}
	if next > 5 {
		return 5
	}
	return next
}
