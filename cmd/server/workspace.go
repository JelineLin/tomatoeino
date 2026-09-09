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
	"os"
	"path/filepath"
	"sync"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/google/uuid"

	"tomato-platform/internal/menu"
	"tomato-platform/internal/vectorstore"
)

// workspace 是一个用户的完整世界。
type workspace struct {
	agent    *react.Agent
	history  *menu.HistoryStore
	inv      *menu.InventoryStore
	profile  *menu.ProfileStore   // 宝宝档案：动态注入该户 agent 的人设
	store    *vectorstore.Store   // 该户内存向量库：界面直写历史后按同 ID Upsert 更新语义索引
	sessions *sessionStore        // L2 会话下沉到 workspace = 天然按用户隔离
	briefs   *briefStore
}

// registry 是 userID → workspace 的懒加载注册表。
type registry struct {
	mu       sync.Mutex
	m        map[string]*workspace
	building map[string]chan struct{} // 手写 singleflight：防同一用户并发首访重复构建

	purged map[string]struct{} // 已被账号删除清掉的租户墓碑：本进程内永不再建

	dataDir  string
	users    *userRegistry // 合法 uid 的裁判（nil = 单用户本地模式，只认 defaultUserID）
	platform bool          // 是否接受已由 account-server 验证的 UUID 用户
	embedder embedding.Embedder
	cm       model.ToolCallingChatModel
}

func newRegistry(dataDir string, users *userRegistry, platform bool, embedder embedding.Embedder, cm model.ToolCallingChatModel) *registry {
	return &registry{
		m:        map[string]*workspace{},
		building: map[string]chan struct{}{},
		purged:   map[string]struct{}{},
		dataDir:  dataDir,
		users:    users,
		platform: platform,
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
	r.mu.Lock()
	_, tombstoned := r.purged[uid]
	r.mu.Unlock()
	if tombstoned {
		// 账号已被平台删除。此刻正在飞行中的请求不许再把这户建回来，
		// 否则删完又冒出一个空 workspace，还会被简报调度当成活人天天生成。
		return fmt.Errorf("用户 %q 的数据已随账号删除清除", uid)
	}
	if r.users != nil {
		if _, ok := r.users.names[uid]; ok {
			return nil
		}
	}
	if r.platform {
		if canonicalUUID(uid) {
			return nil
		}
		return fmt.Errorf("统一账户 userID %q 不是规范 UUID", uid)
	}
	if r.users == nil {
		if uid != defaultUserID {
			return fmt.Errorf("单用户本地模式只认 %q，拒绝 %q", defaultUserID, uid)
		}
		return nil
	}
	return fmt.Errorf("未注册的用户 %q——users.json 里没有它", uid)
}

// canonicalUUID 是平台租户 ID 的唯一尺子：必须是 uuid 库能解析、且原样等于
// 规范小写形式的字符串。大写变体、带花括号的变体、路径片段都会被挡在外面——
// uid 会直接拼进 dataDir/users/<uid>/，这道尺子就是目录穿越的闸门。
func canonicalUUID(s string) bool {
	parsed, err := uuid.Parse(s)
	return err == nil && parsed.String() == s
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

	asm, err := menu.BuildAgent(ctx, r.embedder, r.cm,
		filepath.Join(dir, "history.json"),
		filepath.Join(dir, "inventory.json"),
		filepath.Join(dir, "profile.json"))
	if err != nil {
		return nil, fmt.Errorf("构建用户 %s 的 workspace 失败: %w", uid, err)
	}
	log.Printf("🏗️  用户 %s 就绪（历史 %d 天）", uid, len(asm.History.Snapshot()))
	return &workspace{
		agent:    asm.Agent,
		history:  asm.History,
		inv:      asm.Inv,
		profile:  asm.Profile,
		store:    asm.Store,
		sessions: newSessionStore(sessionTTL),
		briefs:   &briefStore{},
	}, nil
}

// purge 清掉一个租户的全部业务数据——account-server 硬删除账户前会调它。
// 幂等：这户从没来过、目录本就不存在，也算清干净了（删除任务会重试，
// 重试必须能安全地再走一遍）。
//
// 顺序是先立墓碑再删盘：反过来的话，正在飞行中的请求可能在删完之后又把
// 目录写回来。墓碑只活在本进程内存里，重启后账号在 account-server 那边
// 早已不存在，token 也就换不到身份，不需要持久化。
func (r *registry) purge(uid string) error {
	if !canonicalUUID(uid) {
		return fmt.Errorf("拒绝清除非规范 UUID %q", uid)
	}
	r.mu.Lock()
	r.purged[uid] = struct{}{}
	delete(r.m, uid)
	r.mu.Unlock()

	dir := filepath.Join(r.dataDir, "users", uid)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除用户 %s 的数据目录失败: %w", uid, err)
	}
	log.Printf("🗑️  已清除用户 %s 的全部备餐数据（%s）", uid, dir)
	return nil
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
//
// 统一账户没有本地名册：users.json 只管旧用户，平台用户的「存在」写在
// dataDir/users/<uuid>/ 这份数据上。所以这里要三处取并集：已建的 workspace
// （本次进程见过的人）、users.json（迁移通道）、磁盘目录（重启前来过的平台用户）。
// 少了磁盘那份，凌晨重启后没人访问过的平台用户当天就收不到 07:00 简报。
func (r *registry) allUIDs() []string {
	if r.users == nil && !r.platform {
		return []string{defaultUserID}
	}
	r.mu.Lock()
	known := make(map[string]struct{}, len(r.m))
	for uid := range r.m {
		known[uid] = struct{}{}
	}
	r.mu.Unlock()
	if r.users != nil {
		for uid := range r.users.names {
			known[uid] = struct{}{}
		}
	}
	if r.platform {
		for _, uid := range r.platformUIDsOnDisk() {
			known[uid] = struct{}{}
		}
	}
	uids := make([]string, 0, len(known))
	for uid := range known {
		uids = append(uids, uid)
	}
	return uids
}

// platformUIDsOnDisk 把 dataDir/users/ 下的规范 UUID 目录当成平台用户名册。
// 目录名是外部来源，所以用 legalUID 的同一把尺子再量一遍，非 UUID 的目录
// （旧的 home 等）留给 users.json 那条线，不在这里冒充平台租户。
//
// 名册的收敛靠 purge：账号删除时目录整个删掉，这里自然就扫不到了。
func (r *registry) platformUIDsOnDisk() []string {
	entries, err := os.ReadDir(filepath.Join(r.dataDir, "users"))
	if err != nil {
		if !os.IsNotExist(err) { // 目录还没建 = 一个平台用户都没来过，不是错
			log.Printf("⚠️  扫描平台用户目录失败: %v", err)
		}
		return nil
	}
	uids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && canonicalUUID(e.Name()) {
			uids = append(uids, e.Name())
		}
	}
	return uids
}
