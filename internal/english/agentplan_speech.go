package english

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AgentPlanSpeech 对接显式配置的转写端点。计划阶段尚未确认 Agent Plan ASR 的准确
// URL/模型名，因此这里拒绝猜测：只有 ENGLISH_ASR_URL 与 ENGLISH_ASR_MODEL 同时存在才启用。
type AgentPlanSpeech struct {
	URL, APIKey, Model string
	Client             *http.Client
}

func NewSpeechRecognizerFromEnv() SpeechRecognizer {
	url, model, key := strings.TrimSpace(os.Getenv("ENGLISH_ASR_URL")), strings.TrimSpace(os.Getenv("ENGLISH_ASR_MODEL")), os.Getenv("OPENAI_API_KEY")
	if url == "" || model == "" {
		return UnavailableSpeechRecognizer{Reason: "请完成 Agent Plan ASR 技术验证后配置 ENGLISH_ASR_URL 和 ENGLISH_ASR_MODEL"}
	}
	if key == "" {
		return UnavailableSpeechRecognizer{Reason: "OPENAI_API_KEY 未配置"}
	}
	return &AgentPlanSpeech{URL: url, APIKey: key, Model: model, Client: &http.Client{Timeout: 2 * time.Minute}}
}

func (a *AgentPlanSpeech) Transcribe(ctx context.Context, path string) (Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", a.Model)
	_ = mw.WriteField("response_format", "verbose_json")
	part, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return Transcript{}, err
	}
	if _, err = io.Copy(part, f); err != nil {
		return Transcript{}, err
	}
	if err = mw.Close(); err != nil {
		return Transcript{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, &body)
	if err != nil {
		return Transcript{}, err
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := a.Client.Do(req)
	if err != nil {
		return Transcript{}, fmt.Errorf("调用 ASR 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode/100 != 2 {
		return Transcript{}, fmt.Errorf("ASR 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out Transcript
	if err := json.Unmarshal(raw, &out); err != nil {
		return Transcript{}, fmt.Errorf("解析 ASR 响应失败: %w", err)
	}
	if strings.TrimSpace(out.Text) == "" {
		return Transcript{}, fmt.Errorf("ASR 返回空转写")
	}
	return out, nil
}
