package english

import (
	"math"
	"regexp"
	"strings"
)

var wordPattern = regexp.MustCompile(`[A-Za-z]+(?:'[A-Za-z]+)?`)

func words(s string) []string {
	raw := wordPattern.FindAllString(strings.ToLower(s), -1)
	if raw == nil {
		return []string{}
	}
	return raw
}

// EvaluateTranscript 用最小编辑路径对齐原文和 ASR。它衡量“转写与原文一致度”，
// 不是音素级口语考试；以后接入 MDD 时可替换评分 Provider，账本结构不用迁移。
func EvaluateTranscript(expected, actual string, durationSec float64) (accuracy, wpm, score float64, issues []WordIssue) {
	a, b := words(expected), words(actual)
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
		dp[i][0] = i
	}
	for j := 0; j <= m; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dp[i][j] = min3(dp[i-1][j]+1, dp[i][j-1]+1, dp[i-1][j-1]+cost)
		}
	}
	for i, j := n, m; i > 0 || j > 0; {
		if i > 0 && j > 0 && a[i-1] == b[j-1] && dp[i][j] == dp[i-1][j-1] {
			i--
			j--
			continue
		}
		if i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+1 {
			issues = append(issues, WordIssue{Expected: a[i-1], Actual: b[j-1], Kind: "misread"})
			i--
			j--
			continue
		}
		if i > 0 && dp[i][j] == dp[i-1][j]+1 {
			issues = append(issues, WordIssue{Expected: a[i-1], Kind: "missing"})
			i--
			continue
		}
		issues = append(issues, WordIssue{Actual: b[j-1], Kind: "repeated"})
		j--
	}
	if n > 0 {
		accuracy = math.Max(0, 1-float64(dp[n][m])/float64(n))
	}
	if durationSec > 0 {
		wpm = float64(m) / (durationSec / 60)
	}
	pace := 1.0
	if wpm > 0 {
		pace = math.Min(1, wpm/100)
	}
	score = math.Round((accuracy*.85+pace*.15)*1000) / 10
	return
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
