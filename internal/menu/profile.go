package menu

// profile.go —— 每户档案：宝宝的基础信息（生日/过敏/忌口/要点）。
//
// 为什么需要它：新家庭冷启动时历史为空，agent 对宝宝一无所知，全靠 ask_user
// 现问、问完过期即忘。档案把这些「问一次就该记住」的事实落盘，并在每次请求时
// 动态注入该户 agent 的人设——agent 不需要工具去查，它「天生知道」。
//
// 设计要点：
//   - 存生日而不是月龄：月龄会自己长，注入人设时按当天现算（"17 个月大"）。
//   - 过敏原是硬禁忌（推荐绝对避开），忌口是软偏好（尽量避开/换做法），分开存。
//   - 写入口照旧收敛在聊天（update_profile 工具），HTTP 侧只读。
//   - 持久化照抄 inventory 范本：锁 + temp+rename 原子落盘。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Profile 是一户的宝宝档案。所有字段可空——空档案照样能用，只是 agent 少些依据。
type Profile struct {
	BabyName  string   `json:"babyName,omitempty"`  // 宝宝称呼
	BirthDate string   `json:"birthDate,omitempty"` // 出生日期 YYYY-MM-DD
	Allergies []string `json:"allergies,omitempty"` // 过敏原（硬禁忌）
	Dislikes  []string `json:"dislikes,omitempty"`  // 不吃/不爱吃（软偏好）
	Notes     string   `json:"notes,omitempty"`     // 其他要点（咀嚼能力/口味倾向等）
}

// IsEmpty 判断档案是否还是白纸——人设注入和「新户引导」口径都用它。
func (p Profile) IsEmpty() bool {
	return p.BabyName == "" && p.BirthDate == "" && len(p.Allergies) == 0 &&
		len(p.Dislikes) == 0 && p.Notes == ""
}

// ProfileStore 是带锁、带落盘的档案。
type ProfileStore struct {
	mu   sync.Mutex
	path string
	p    Profile
}

// NewProfileStore 打开（或新建）档案。文件不存在不算错——空档案，建档时落盘。
func NewProfileStore(path string) (*ProfileStore, error) {
	s := &ProfileStore{path: path}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取档案文件 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(raw, &s.p); err != nil {
		return nil, fmt.Errorf("解析档案文件 %s 失败: %w", path, err)
	}
	return s, nil
}

// Get 返回档案副本（切片也拷贝，调用方随便改不脏账）。
func (s *ProfileStore) Get() Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.p
	p.Allergies = append([]string(nil), s.p.Allergies...)
	p.Dislikes = append([]string(nil), s.p.Dislikes...)
	return p
}

// Set 整体落盘新档案（合并语义由工具层负责，store 只管存）。
func (s *ProfileStore) Set(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.p = p
	raw, err := json.MarshalIndent(s.p, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化档案失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建档案目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".profile-*.json")
	if err != nil {
		return fmt.Errorf("创建临时档案文件失败: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("写临时档案文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时档案文件失败: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("落盘档案失败: %w", err)
	}
	return nil
}

// ageMonths 按出生日期算当前月龄。日期非法或在未来返回 ok=false。
func ageMonths(birthDate string, now time.Time) (int, bool) {
	t, err := time.Parse("2006-01-02", birthDate)
	if err != nil {
		return 0, false
	}
	m := (now.Year()-t.Year())*12 + int(now.Month()) - int(t.Month())
	if now.Day() < t.Day() {
		m--
	}
	if m < 0 {
		return 0, false
	}
	return m, true
}

// renderProfile 把档案渲染成注入人设的一段话；空档案返回空串（不注入）。
// 月龄按 now 现算——档案存的是生日，"多大了"永远是新鲜的。
func renderProfile(p Profile, now time.Time) string {
	if p.IsEmpty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("【宝宝档案】")
	if p.BabyName != "" {
		fmt.Fprintf(&b, "称呼：%s。", p.BabyName)
	}
	if p.BirthDate != "" {
		if m, ok := ageMonths(p.BirthDate, now); ok {
			fmt.Fprintf(&b, "出生日期 %s（现在 %d 个月大）。", p.BirthDate, m)
		} else {
			fmt.Fprintf(&b, "出生日期 %s。", p.BirthDate)
		}
	}
	if len(p.Allergies) > 0 {
		fmt.Fprintf(&b, "过敏原：%s——任何推荐【绝对】不得包含它们及其制品。", strings.Join(p.Allergies, "、"))
	}
	if len(p.Dislikes) > 0 {
		fmt.Fprintf(&b, "不吃/不爱吃：%s——尽量避开或换个做法。", strings.Join(p.Dislikes, "、"))
	}
	if p.Notes != "" {
		fmt.Fprintf(&b, "其他要点：%s", p.Notes)
	}
	return b.String()
}
