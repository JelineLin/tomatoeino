// workspace.go —— 多用户改造第 5 步：per-user workspace 注册表（评审优胜方案 A）。
//
// 核心思路：每个用户（家庭）一整套独立世界——账本、向量索引、agent 图、会话、简报，
// 注册表按 userID 懒加载缓存。租户边界切在 HTTP 入口（withAuth 解析 uid → 这里取
// workspace），下游 handler 和 internal/menu 对多租户零感知：工具闭包捕获的就是
// 自己用户的 store，「拿错人数据」在结构上不存在，不靠任何运行时纪律。
//
// 资源账（评审核定）：真正贵的是向量数据，它在任何方案里都按用户份存；
// 每用户多出的只是一张 agent 编译图（几十 KB）。embedder/chat client 是
// 进程级共享的重资源，由 registry 持有一份注入，不随用户数增长。
//
// 已知债（写在这里防隐形炸弹）：registry 只进不出——20 户内没有内存压力。
// 将来若做闲置淘汰，sessions 必须摘出来单独处理，否则淘汰会连带清掉活跃会话、
// L3 中断恢复断链（评审揪出的三案共有暗雷）。
package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/agent/react"

	"tomatoeino/internal/menu"
)

// workspace 是一个用户的完整世界。
type workspace struct {
	agent    *react.Agent
	history  *menu.HistoryStore
	inv      *menu.InventoryStore
	sessions *sessionStore // L2 会话下沉到 workspace = 天然按用户隔离
	briefs   *briefStore
}

// registry 是 userID → workspace 的懒加载注册表。
type registry struct {
	mu       sync.Mutex
	m        map[string]*workspace
	building map[string]chan struct{} // 手写 singleflight：防同一用户并发首访重复构建

	dataDir  string
	users    *userRegistry // 合法 uid 的裁判（nil = 单用户本地模式，只认 defaultUserID）
	embedder embedding.Embedder
	cm       model.ToolCallingChatModel
}

func newRegistry(dataDir string, users *userRegistry, embedder embedding.Embedder, cm model.ToolCallingChatModel) *registry {
	return &registry{
		m:        map[string]*workspace{},
		building: map[string]chan struct{}{},
		dataDir:  dataDir,
		users:    users,
		embedder: embedder,
		cm:       cm,
	}
}

// legalUID 判定 uid 是否被允许建 workspace——fail-closed 契约：
// 未知/空 uid 一律报错，绝不合成幽灵租户（哪个入口忘了鉴权，在这里炸出来，
// 而不是悄悄给它开一个新世界）。
func (r *registry) legalUID(uid string) error {
	if uid == "" {
		return fmt.Errorf("拒绝空 userID——请求没有经过鉴权中间件？")
	}
	if r.users == nil {
		if uid != defaultUserID {
			return fmt.Errorf("单用户本地模式只认 %q，拒绝 %q", defaultUserID, uid)
		}
		return nil
	}
	if _, ok := r.users.names[uid]; !ok {
		return fmt.Errorf("未注册的用户 %q——users.json 里没有它", uid)
	}
	return nil
}

// get 取（或懒构建）用户的 workspace。
// 同一用户并发首访时，只有一个 goroutine 真正构建（首次要全量 embed 历史，
// 十几秒），其他人等在 channel 上——绝不能让同一个用户被构建两次，
// 既浪费 embedding 钱，更会造出两套各自为政的账本。
func (r *registry) get(ctx context.Context, uid string) (*workspace, error) {
	if err := r.legalUID(uid); err != nil {
		return nil, err
	}

	for {
		r.mu.Lock()
		if ws, ok := r.m[uid]; ok {
			r.mu.Unlock()
			return ws, nil
		}
		if ch, ok := r.building[uid]; ok {
			r.mu.Unlock()
			select { // 别人在建：等它建完再回到循环开头取
			case <-ch:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		ch := make(chan struct{})
		r.building[uid] = ch
		r.mu.Unlock()

		ws, err := r.build(ctx, uid)

		r.mu.Lock()
		delete(r.building, uid)
		if err == nil {
			r.m[uid] = ws
		}
		r.mu.Unlock()
		close(ch) // 无论成败都放行等待者；失败时它们会重走循环、自己再试一次

		return ws, err
	}
}

// build 构建一个用户的 workspace：数据在 dataDir/users/<uid>/ 下，
// 文件不存在就是空账本（新用户从零起步，record_meal 自举历史）。
func (r *registry) build(ctx context.Context, uid string) (*workspace, error) {
	dir := filepath.Join(r.dataDir, "users", uid)
	log.Printf("🏗️  构建用户 %s 的 workspace（%s）…", uid, dir)

	agent, hs, inv, err := menu.BuildAgent(ctx, r.embedder, r.cm,
		filepath.Join(dir, "history.json"),
		filepath.Join(dir, "inventory.json"))
	if err != nil {
		return nil, fmt.Errorf("构建用户 %s 的 workspace 失败: %w", uid, err)
	}
	log.Printf("🏗️  用户 %s 就绪（历史 %d 天）", uid, len(hs.Snapshot()))
	return &workspace{
		agent:    agent,
		history:  hs,
		inv:      inv,
		sessions: newSessionStore(sessionTTL),
		briefs:   &briefStore{},
	}, nil
}

// forEach 遍历所有已构建的 workspace（会话清扫等全局巡检用）。
// 回调在锁外执行，遍历的是快照——巡检不该阻塞新用户构建。
func (r *registry) forEach(f func(uid string, ws *workspace)) {
	r.mu.Lock()
	snapshot := make(map[string]*workspace, len(r.m))
	for uid, ws := range r.m {
		snapshot[uid] = ws
	}
	r.mu.Unlock()
	for uid, ws := range snapshot {
		f(uid, ws)
	}
}

// allUIDs 列出应该存在的全部用户（不管建没建）——简报调度按它逐户生成。
func (r *registry) allUIDs() []string {
	if r.users == nil {
		return []string{defaultUserID}
	}
	uids := make([]string, 0, len(r.users.names))
	for uid := range r.users.names {
		uids = append(uids, uid)
	}
	return uids
}
