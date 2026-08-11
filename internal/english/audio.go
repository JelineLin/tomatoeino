package english

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var ErrAudioTooLong = errors.New("录音超过 10 分钟")

// NormalizeAudio 用 ffprobe 读取真实时长，再转成 16kHz 单声道 PCM/WAV。
// 浏览器传来的 duration 只用于交互展示，账本和限额以媒体本身为准，防止客户端伪造。
func NormalizeAudio(ctx context.Context, input string) (output string, duration float64, err error) {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	probe := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", input)
	raw, err := probe.Output()
	if err != nil {
		return "", 0, fmt.Errorf("ffprobe 无法读取录音: %w", err)
	}
	duration, err = strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil || duration <= 0 {
		return "", 0, fmt.Errorf("录音时长无效")
	}
	if duration > 600.5 {
		return "", duration, ErrAudioTooLong
	}
	output = input + ".wav"
	convertCtx, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	cmd := exec.CommandContext(convertCtx, "ffmpeg", "-v", "error", "-nostdin", "-y", "-i", input, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", output)
	if raw, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(output)
		return "", duration, fmt.Errorf("ffmpeg 转换失败: %w (%s)", err, strings.TrimSpace(string(raw)))
	}
	return output, duration, nil
}
