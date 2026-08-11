package english

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type Coach interface {
	Feedback(context.Context, Profile, ProgressSnapshot, SpeakingAttempt) (string, error)
}

// Feedback 让固定的 English Coach 模型把程序算出的客观指标翻译成可执行建议。
// 分数和问题词仍由程序落账，LLM 只有“表达建议”的权限，不能反向修改成绩。
func (p *LLMPlanner) Feedback(ctx context.Context, profile Profile, progress ProgressSnapshot, attempt SpeakingAttempt) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"level": profile.Level, "goal": profile.Goal, "accuracy": attempt.Accuracy,
		"wpm": attempt.WPM, "score": attempt.Score, "issues": attempt.Issues,
		"recurring_errors": progress.RecurringErrors,
	})
	messages := []*schema.Message{
		schema.SystemMessage("You are an encouraging English pronunciation coach. Based only on the supplied metrics, write 2-3 concise Chinese sentences: first acknowledge one strength, then give one concrete slow-reading drill and at most three words to practice. Do not invent phoneme-level findings."),
		schema.UserMessage(string(payload)),
	}
	msg, err := p.model.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("生成朗读建议失败: %w", err)
	}
	feedback := strings.TrimSpace(msg.Content)
	if feedback == "" {
		return "", fmt.Errorf("模型返回空建议")
	}
	return feedback, nil
}
