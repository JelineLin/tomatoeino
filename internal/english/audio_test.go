package english

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 未安装")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 未安装")
	}
	input := filepath.Join(t.TempDir(), "sample.wav")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-y", input).CombinedOutput(); err != nil {
		t.Fatalf("生成测试音频失败: %v %s", err, out)
	}
	output, duration, err := NormalizeAudio(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if duration < .9 || duration > 1.1 {
		t.Fatalf("真实时长=%v", duration)
	}
	if filepath.Ext(output) != ".wav" {
		t.Fatalf("输出格式=%s", output)
	}
}
