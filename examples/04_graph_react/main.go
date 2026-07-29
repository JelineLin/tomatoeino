// 例子 04：亲手用 compose.Graph 搭一个 mini-ReAct——拆开 react.NewAgent 这个黑盒
//
// 学习目标：例 02 之后我们一直在用 react.NewAgent 预制件，模型怎么循环、消息怎么
// 累积、工具结果怎么回灌，全封在盒子里。这一课下到 eino 的编排层 compose，亲手把
// 同一张图搭出来，四个新零件一次到位：
//
//  1. prompt.ChatTemplate —— 模板节点：map 参数渲染成 []*schema.Message；
//  2. compose.Graph —— 节点 + 边；tools→model 那条回边就是 ReAct「循环」的本体；
//  3. compose.NewGraphBranch —— 分支：模型开没开 tool_calls，决定走 tools 还是 END；
//  4. 图内 State —— WithGenLocalState + 节点 pre/post handler，消息历史滚雪球的地方。
//     （对照多租户那课：ctx 是请求级「只读贯穿」通道，state 是图内「可写共享」台账。）
//
// 拆完黑盒立刻兑现一个真实收益：checkpoint。当年做 L3 中断-恢复时，react.NewAgent
// 把编译选项写死、塞不进 WithCheckPointStore，只能用 ToolReturnDirectly 在对话层绕；
// 自己持有图之后，编译选项自己说了算——Part B 演示「敏感工具执行前拦停 → 人工放行 →
// 断点续跑」的原生轨道。和支付系统一个道理：大额指令先进人工审批队列，放行后从断点
// 继续，已清算的步骤不重放（checkpoint 就是那本幂等断点账）。
//
// 运行（须在仓库根目录，.env 配好 chat 凭证；本例不用 embedding）：
//
//	go run ./examples/04_graph_react
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"tomatoeino/internal/llm"
)

// ---------- 玩具工具：数据写死在内存里，注意力全留给图 ----------

// fridge 是冰箱台账（只读工具的数据源）。
var fridge = map[string]string{
	"鸡蛋": "2 个", "西兰花": "1 颗", "鳕鱼": "1 块", "番茄": "3 个", "酸奶": "1 杯",
}

// shoppingList 是采购清单（写工具的落点）。进程内存活、重启即清——本课重点不在持久化。
var shoppingList []string

type fridgeParams struct {
	Keyword string `json:"keyword" jsonschema:"description=想查的食材名，留空返回全部"`
}

func checkFridge(_ context.Context, p *fridgeParams) (string, error) {
	fmt.Printf("      🔧 check_fridge(keyword=%q)\n", p.Keyword)
	var lines []string
	for name, qty := range fridge {
		if p.Keyword == "" || strings.Contains(name, p.Keyword) {
			lines = append(lines, name+" "+qty)
		}
	}
	if len(lines) == 0 {
		return "冰箱里没有匹配的食材", nil
	}
	return "冰箱现有：" + strings.Join(lines, "、"), nil
}

type shoppingParams struct {
	Item string `json:"item" jsonschema:"description=要采购的东西,required"`
	Qty  string `json:"qty" jsonschema:"description=数量，如「10 个」，可不传"`
}

func addShopping(_ context.Context, p *shoppingParams) (string, error) {
	fmt.Printf("      🔧 add_shopping(item=%q, qty=%q)\n", p.Item, p.Qty)
	shoppingList = append(shoppingList, strings.TrimSpace(p.Item+" "+p.Qty))
	return fmt.Sprintf("已加入采购清单：%s %s（当前共 %d 项）", p.Item, p.Qty, len(shoppingList)), nil
}

// ---------- 图内 State：消息历史滚雪球的地方 ----------

// agentState 伴随一次 Invoke 的整个生命周期；回环每转一圈，
// Messages 就多出「assistant(tool_calls) + 各条 tool 结果」两截。
type agentState struct {
	Messages []*schema.Message
	Rounds   int // 进模型的次数，纯为日志可读
}

// checkpoint 要把 state 序列化进断点账本，自定义类型得先在 schema 注册表挂号
// ——和 RPC 注册 IDL 结构同理，反序列化时凭名字找回 Go 类型（eino 内置类型已注册好）。
func init() {
	schema.RegisterName[*agentState]("lesson04_agent_state")
}

// ---------- 搭图 ----------

const (
	nodeTemplate = "template"
	nodeModel    = "model"
	nodeTools    = "tools"
)

// buildMiniReAct 手搭 mini-ReAct 并编译成可执行体。骨架抄自官方 react.NewAgent
// （flow/agent/react/react.go），去掉了流式分支和 ToolReturnDirectly 这些支线。
// extraCompile 是本课的题眼：react.NewAgent 把编译选项写死，而这里留了口子——
// Part A 什么都不传，Part B 塞进 checkpoint 两件套。
func buildMiniReAct(ctx context.Context, cm model.ToolCallingChatModel,
	extraCompile ...compose.GraphCompileOption) (compose.Runnable[map[string]any, *schema.Message], error) {

	// 1) 工具组：InferTool 从 Go 函数反推 JSON-Schema，例 02 的老朋友。
	fridgeTool, err := utils.InferTool(
		"check_fridge",
		"查看家里冰箱现有的食材和数量。keyword 传食材名做筛选，留空返回全部。",
		checkFridge,
	)
	if err != nil {
		return nil, fmt.Errorf("创建 check_fridge 工具失败: %w", err)
	}
	shoppingTool, err := utils.InferTool(
		"add_shopping",
		"把一件要买的东西加进家庭采购清单。写操作：只有用户明确要求购买/补货时才调用。",
		addShopping,
	)
	if err != nil {
		return nil, fmt.Errorf("创建 add_shopping 工具失败: %w", err)
	}
	tools := []tool.BaseTool{fridgeTool, shoppingTool}

	// 2) 给模型绑工具说明书。react.NewAgent 内部同款：WithTools 返回带说明书的新实例，
	//    原实例不动（所以多张图可共享一个底座模型，各绑各的工具组）。
	infos := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("取工具说明书失败: %w", err)
		}
		infos = append(infos, info)
	}
	cmWithTools, err := cm.WithTools(infos)
	if err != nil {
		return nil, fmt.Errorf("绑定工具失败: %w", err)
	}

	// 3) ToolsNode：例 02 里 ToolsConfig 那行配置的真身——按 tool_calls 逐个执行工具、
	//    把结果打包成 role=tool 的消息列表。
	toolsNode, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: tools})
	if err != nil {
		return nil, fmt.Errorf("创建 ToolsNode 失败: %w", err)
	}

	// 4) 模板节点：FString 语法 {var}，Invoke 传 map 参数进来渲染成消息列表。
	//    多轮场景还能用 schema.MessagesPlaceholder 在模板里挖「历史消息」槽位，本课单轮用不上。
	//    「必须先调工具拿事实」这句是 DeepSeek 那课的教训：不给工具优先口径，模型会打太极。
	tpl := prompt.FromMessages(schema.FString,
		schema.SystemMessage("今天是{date}。你是家里的备餐助手，回答简短、直接。"+
			"凡涉及家里实际情况（冰箱有什么、要买什么），必须先调用对应工具拿事实，禁止凭空编造；"+
			"往采购清单加东西必须通过 add_shopping 完成。"),
		schema.UserMessage("{question}"),
	)

	// 5) 建图。泛型参数 = 整张图的入参/出参类型（编译期就把节点间类型对齐查掉）；
	//    WithGenLocalState 声明伴随一次 Invoke 的 state，每次运行新建一份，互不串台。
	g := compose.NewGraph[map[string]any, *schema.Message](
		compose.WithGenLocalState(func(ctx context.Context) *agentState { return &agentState{} }))

	// model 节点 pre-handler：把上游来的消息并进 state，再把**全量历史**交给模型。
	// 第一圈上游是模板渲染的 [system, user]，之后每圈上游是 tools 的执行结果——
	// 模型是无状态 RPC，每次都要看到完整对账单，state 就是那本总账。
	modelPre := func(_ context.Context, in []*schema.Message, st *agentState) ([]*schema.Message, error) {
		st.Messages = append(st.Messages, in...)
		st.Rounds++
		fmt.Printf("   🧠 第 %d 次进模型（带 %d 条消息）\n", st.Rounds, len(st.Messages))
		return st.Messages, nil
	}
	// model 节点 post-handler：模型的回复（含开出 tool_calls 的那条）也记进总账。
	// 官方 react 把这步放在 tools 节点的 pre-handler 里，语义等价；挪到这儿多一个好处：
	// Part B 在 tools 前拦停时，审批单已经躺在 state 里，拦截现场直接可读。
	modelPost := func(_ context.Context, out *schema.Message, st *agentState) (*schema.Message, error) {
		st.Messages = append(st.Messages, out)
		return out, nil
	}
	if err := g.AddChatModelNode(nodeModel, cmWithTools,
		compose.WithStatePreHandler(modelPre), compose.WithStatePostHandler(modelPost)); err != nil {
		return nil, err
	}

	// tools 节点 pre-handler 只剩一个恢复彩蛋（抄自官方 react）：断点续跑时输入可能
	// 不重放（in == nil），此时从 state 把最后那条 assistant 消息捞回来——一切从总账来。
	toolsPre := func(_ context.Context, in *schema.Message, st *agentState) (*schema.Message, error) {
		if in == nil {
			return st.Messages[len(st.Messages)-1], nil
		}
		return in, nil
	}
	if err := g.AddToolsNode(nodeTools, toolsNode, compose.WithStatePreHandler(toolsPre)); err != nil {
		return nil, err
	}

	// 6) 布线。分支挂在 model 出口：有 tool_calls 去干活，没有就是终答。
	//    （官方用 NewStreamGraphBranch 看首个流式分片判断，那是流式课的内容；
	//    本课走 Invoke 非流式，用 NewGraphBranch 看完整消息即可。）
	branchCond := func(_ context.Context, msg *schema.Message) (string, error) {
		if n := len(msg.ToolCalls); n > 0 {
			fmt.Printf("   ↪️ 分支：模型开出 %d 个 tool_calls → %s\n", n, nodeTools)
			return nodeTools, nil
		}
		fmt.Println("   ↪️ 分支：没有 tool_calls → END")
		return compose.END, nil
	}
	if err := errors.Join(
		g.AddChatTemplateNode(nodeTemplate, tpl),
		g.AddEdge(compose.START, nodeTemplate),
		g.AddEdge(nodeTemplate, nodeModel),
		g.AddBranch(nodeModel, compose.NewGraphBranch(branchCond,
			map[string]bool{nodeTools: true, compose.END: true})),
		g.AddEdge(nodeTools, nodeModel), // 回边——ReAct 的「循环」就这一条线
	); err != nil {
		return nil, fmt.Errorf("布线失败: %w", err)
	}

	// 7) 编译成 Runnable。带环的图必须用 AnyPredecessor 触发模式（pregel 逐超步推进，
	//    任一前驱就绪即触发；默认的 AllPredecessor 是 DAG 语义，见环直接报错）。
	//    MaxRunSteps 是死循环保险丝：模型若一直开工具单，转满步数强制熔断。
	return g.Compile(ctx, append([]compose.GraphCompileOption{
		compose.WithMaxRunSteps(12),
		compose.WithNodeTriggerMode(compose.AnyPredecessor),
	}, extraCompile...)...)
}

// ---------- 内存版断点账本 ----------

// memStore 是 20 行教学版 CheckPointStore。换成落盘/Redis 实现，断点就能跨进程存活
// ——那才是审批队列的真实形态：拦停的单子写库，审批通过后由任意一台 worker 续跑。
type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.m[id]
	return cp, ok, nil
}

func (s *memStore) Set(_ context.Context, id string, cp []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = cp
	return nil
}

// ---------- 主流程 ----------

func main() {
	ctx := context.Background()
	today := time.Now().Format("2006-01-02")

	// 底座模型只拨号一次，两张图共享（重资源进程级共享——例 02 讲过的装配原则）。
	cm, err := llm.NewToolCallingChatModel(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// ═══ Part A：手搭的图跑一轮带工具的问答，日志里看回环转动 ═══
	fmt.Println("═══ Part A：mini-ReAct 回环 ═══")
	agentA, err := buildMiniReAct(ctx, cm)
	if err != nil {
		log.Fatal(err)
	}
	ans, err := agentA.Invoke(ctx, map[string]any{
		"date":     today,
		"question": "冰箱里现在有什么？帮娃想个明天的早饭。",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n【答】%s\n\n", ans.Content)

	// ═══ Part B：checkpoint 拦停-放行——react.NewAgent 给不了的那个口子 ═══
	fmt.Println("═══ Part B：写操作执行前拦停，人工放行后断点续跑 ═══")
	agentB, err := buildMiniReAct(ctx, cm,
		compose.WithCheckPointStore(newMemStore()),            // 断点账本存哪
		compose.WithInterruptBeforeNodes([]string{nodeTools}), // 进 tools 节点前一律拦停
	)
	if err != nil {
		log.Fatal(err)
	}

	in := map[string]any{
		"date":     today,
		"question": "家里鸡蛋不多了，帮我把鸡蛋加进采购清单，买 10 个。",
	}
	// 第一跑：模板→模型→分支判定去 tools→在 tools 门口被拦停。
	// 拦停不是异常是「有单待审」：现场（state + 待执行输入）已记入断点账本，
	// Invoke 以一个可解包的 error 返回。
	_, err = agentB.Invoke(ctx, in, compose.WithCheckPointID("approve-001"))
	info, interrupted := compose.ExtractInterruptInfo(err)
	if !interrupted {
		// 模型没开工具单才会走到这（比如它嘴上答应没干活）——那就没有可演示的拦停。
		log.Fatalf("预期在 tools 前拦停，实际 err=%v", err)
	}
	st := info.State.(*agentState)
	last := st.Messages[len(st.Messages)-1]
	fmt.Println("\n⛔ 已在 tools 节点前拦停，待审批指令：")
	for _, tc := range last.ToolCalls {
		fmt.Printf("      %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
	}

	// 第二跑：同一个 checkpoint id 再 Invoke 即「放行」。留意日志——template 不再渲染、
	// 模型直接从「第 2 次」起跳：前半程没有重放，是真断点续跑，不是重头再来。
	fmt.Println("\n✅ 人工放行（同一 checkpoint id 再次 Invoke，从断点续跑）：")
	ans2, err := agentB.Invoke(ctx, in, compose.WithCheckPointID("approve-001"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n【答】%s\n\n当前采购清单：%v\n", ans2.Content, shoppingList)
}
