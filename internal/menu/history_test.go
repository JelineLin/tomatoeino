package menu

// HistoryStore + record_meal 的离线测试（多用户改造八步的第 2、3 步）。
// 向量侧用一个恒等 stub embedder，全程不联网不花钱。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"

	"tomatoeino/internal/vectorstore"
)

// storeFromDays 是白盒帮手：直接用现成切片造一个只读 HistoryStore，
// 供老的查询工具测试用（不落盘，勿对它调 SetMeal）。
func storeFromDays(days []Day) *HistoryStore {
	return &HistoryStore{days: days}
}

// stubEmbedder 给 record_meal 测试用：每条文本都返回同一个向量，够 Upsert 走通即可。
type stubEmbedder struct{}

func (stubEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = []float64{1}
	}
	return out, nil
}

func TestHistoryStore_MissingFileIsEmpty(t *testing.T) {
	hs, err := NewHistoryStore(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("文件不存在应是空历史而非报错: %v", err)
	}
	if len(hs.Snapshot()) != 0 {
		t.Error("空历史 Snapshot 应为空")
	}
}

func TestHistoryStore_SetMealInsertSorted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	hs, _ := NewHistoryStore(path)

	meal := Meal{Time: "11:30", Dishes: []Dish{{Name: "番茄鸡蛋面", Detail: "一小碗"}}}
	// 乱序写入两天，存储必须保持升序（recent_meals 取尾部依赖它）。
	if _, replaced, err := hs.SetMeal("2026-07-08", "lunch", meal); err != nil || replaced {
		t.Fatalf("新写入不该是 replaced，err=%v replaced=%v", err, replaced)
	}
	if _, _, err := hs.SetMeal("2026-07-06", "dinner", meal); err != nil {
		t.Fatal(err)
	}

	days := hs.Snapshot()
	if len(days) != 2 || days[0].Date != "2026-07-06" || days[1].Date != "2026-07-08" {
		t.Errorf("应按日期升序存储，实际 %v %v", days[0].Date, days[1].Date)
	}
	if days[1].Lunch == nil || days[1].Lunch.Dishes[0].Name != "番茄鸡蛋面" {
		t.Error("写入的餐次内容不对")
	}

	// 持久化往返：重开 store 应读回同样内容。
	hs2, err := NewHistoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := hs2.Snapshot(); len(got) != 2 || got[1].Lunch == nil {
		t.Errorf("重开后数据不一致: %+v", got)
	}
}

func TestHistoryStore_OverwriteAndBak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	hs, _ := NewHistoryStore(path)

	old := Meal{Dishes: []Dish{{Name: "番茄鸡蛋面"}}}
	if _, _, err := hs.SetMeal("2026-07-08", "lunch", old); err != nil {
		t.Fatal(err)
	}
	// 同天同餐再写 = 整餐覆盖，replaced=true。
	upd := Meal{Dishes: []Dish{{Name: "番茄鸡蛋面"}, {Name: "蒸蛋"}}}
	_, replaced, err := hs.SetMeal("2026-07-08", "lunch", upd)
	if err != nil || !replaced {
		t.Fatalf("覆盖写应 replaced=true，err=%v replaced=%v", err, replaced)
	}
	if got := hs.Snapshot()[0].Lunch.Dishes; len(got) != 2 {
		t.Errorf("覆盖后应是 2 道菜，实际 %d", len(got))
	}

	// .bak 应是覆盖前的最近一版（只有 1 道菜那版）。
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("覆盖写后应留 .bak: %v", err)
	}
	if !strings.Contains(string(bak), "番茄鸡蛋面") || strings.Contains(string(bak), "蒸蛋") {
		t.Errorf(".bak 应是覆盖前版本，实际: %s", bak)
	}

	// 同天另一餐不受影响、不算覆盖。
	if _, replaced, _ := hs.SetMeal("2026-07-08", "dinner", old); replaced {
		t.Error("同天不同餐是新写入，不该 replaced")
	}
}

func TestHistoryStore_InvalidMealField(t *testing.T) {
	hs, _ := NewHistoryStore(filepath.Join(t.TempDir(), "h.json"))
	if _, _, err := hs.SetMeal("2026-07-08", "宵夜", Meal{Dishes: []Dish{{Name: "x"}}}); err == nil {
		t.Error("非法餐别应报错")
	}
}

// 反馈结转回归：先记反馈、再 record_meal 覆盖同餐（不带反馈），旧反馈必须结转，
// 且 SetMeal 返回的 stored 要带上它——调用方拿 stored 重建的向量 doc 才不会丢反馈
//（否则就是审查抓到的「JSON 有反馈、语义检索无反馈」两视图错位）。
func TestHistoryStore_FeedbackCarryOverOnReRecord(t *testing.T) {
	hs, _ := NewHistoryStore(filepath.Join(t.TempDir(), "h.json"))
	if _, _, err := hs.SetMeal("2026-07-08", "lunch", Meal{Dishes: []Dish{{Name: "面"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.SetFeedback("2026-07-08", "lunch", &Feedback{Rating: "dislike", Note: "只吃几口"}); err != nil {
		t.Fatal(err)
	}

	// 再记同餐、不带反馈。
	stored, replaced, err := hs.SetMeal("2026-07-08", "lunch", Meal{Dishes: []Dish{{Name: "面"}, {Name: "蒸蛋"}}})
	if err != nil || !replaced {
		t.Fatalf("覆盖写应 replaced=true，err=%v replaced=%v", err, replaced)
	}
	if stored.Feedback == nil || stored.Feedback.Rating != "dislike" {
		t.Fatalf("stored 应结转旧反馈 dislike，实际 %+v", stored.Feedback)
	}
	if fb := hs.Snapshot()[0].Lunch.Feedback; fb == nil || fb.Rating != "dislike" {
		t.Errorf("JSON 侧反馈应保留，实际 %+v", fb)
	}
	// 关键：用 stored 渲染的向量 doc 必须含反馈文本，才与 JSON 同步。
	if doc := BuildMealDocument("2026-07-08", "lunch", stored); !strings.Contains(doc.Content, "不爱吃") {
		t.Errorf("向量 doc 应含结转的反馈文本，实际: %s", doc.Content)
	}
}

// ---- record_meal 工具 ----

func TestRecordMeal(t *testing.T) {
	ctx := context.Background()
	hs, _ := NewHistoryStore(filepath.Join(t.TempDir(), "history.json"))
	vs := vectorstore.New(stubEmbedder{})
	record := makeRecordMeal(hs, vs)

	// 正常入库。
	out, err := record(ctx, recordMealInput{
		Date: "2026-07-08", Meal: "lunch",
		Dishes: []dishInput{{Name: "番茄鸡蛋面", Detail: "一小碗"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已记录") || !strings.Contains(out, "午餐") {
		t.Errorf("回执措辞不对: %q", out)
	}
	if len(hs.Snapshot()) != 1 {
		t.Error("历史应有 1 天")
	}
	// 向量视图同步：语义检索应能召回。
	docs, _ := vs.Retrieve(ctx, "面")
	if len(docs) != 1 || !strings.Contains(docs[0].Content, "番茄鸡蛋面") {
		t.Errorf("向量索引应有这餐: %+v", docs)
	}

	// 覆盖修正：回执换措辞，索引不留旧尸体。
	out, _ = record(ctx, recordMealInput{
		Date: "2026-07-08", Meal: "lunch",
		Dishes: []dishInput{{Name: "番茄鸡蛋面"}, {Name: "蒸蛋"}},
	})
	if !strings.Contains(out, "已覆盖更新") {
		t.Errorf("覆盖时回执应说明: %q", out)
	}
	docs, _ = vs.Retrieve(ctx, "面")
	if len(docs) != 1 || !strings.Contains(docs[0].Content, "蒸蛋") {
		t.Errorf("覆盖后索引应只剩新版且含新菜: %+v", docs)
	}

	// 三类坏参数都用人话回执（不是 error），让模型自纠。
	for _, in := range []recordMealInput{
		{Date: "今天", Meal: "lunch", Dishes: []dishInput{{Name: "x"}}},   // 坏日期
		{Date: "2026-07-08", Meal: "宵夜", Dishes: []dishInput{{Name: "x"}}}, // 坏餐别
		{Date: "2026-07-08", Meal: "dinner", Dishes: nil},                 // 空菜品
	} {
		out, err := record(ctx, in)
		if err != nil {
			t.Errorf("坏参数应人话回执而非 error: %v", err)
		}
		if !strings.Contains(out, "入库失败") {
			t.Errorf("坏参数回执应说明失败原因: %q", out)
		}
	}
}
