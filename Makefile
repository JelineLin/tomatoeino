# Makefile —— 把每次手敲的「编译/测试/发包/部署」流程固化成一条命令。
#
# 常用：
#   make build            本地编译全部包（含 cmd/server）
#   make test             全部离线测试（RealEmbedding 无 key 自跳过）
#   make linux            交叉编译 linux 服务端二进制 → dist/server-linux-amd64
#   make release VERSION=v0.2.0
#                         打 tag + 推 tag + 把 linux 二进制发到 GitHub Releases
#                         （需要 gh CLI 且已 gh auth login）
#   make deploy           部署到 hermas：linux 编译 → scp 临时路径 → sudo mv 覆盖
#                         （绕开 text-file-busy）→ 重启 → healthz 验证
#
# 发包为什么走 GitHub Release 而不是 commit 二进制进仓库：
# 27MB/次的二进制会让 git 历史迅速肥胖且无法瘦身；Release 挂附件有版本号可回溯，
# 仓库里只留源码。dist/ 已入 .gitignore。

BINARY   := dist/server-linux-amd64
HOST     := hermas
REMOTE   := /opt/menuagent/server
HEALTH   := http://101.132.191.7:8080/healthz

.PHONY: build test vet linux release deploy clean

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

# 交叉编译 linux 二进制（hermas 是 x86_64 Ubuntu；CGO 关掉保证纯静态、免装依赖）。
linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY) ./cmd/server
	@ls -lh $(BINARY) | awk '{print "  built:", $$9, $$5}'

# 发包到 GitHub Releases：make release VERSION=v0.2.0
# 流程：确认工作区干净 → 编译 → 打 tag → 推 tag → gh release create 挂二进制。
release: linux
ifndef VERSION
	$(error 请指定版本号：make release VERSION=v0.2.0)
endif
	@test -z "$$(git status --porcelain)" || { echo "❌ 工作区有未提交改动，先 commit 再发版"; exit 1; }
	git tag -a $(VERSION) -m "$(VERSION)"
	git push origin $(VERSION)
	gh release create $(VERSION) $(BINARY) \
		--title "$(VERSION)" \
		--generate-notes
	@echo "✅ Release $(VERSION) 已发布（附 server-linux-amd64）"

# 部署到 hermas。两个踩坑实录：
#   ① scp 直接覆盖在跑的二进制会报 text file busy——先传临时路径，再 sudo mv 覆盖
#     （rename 对运行中的文件合法），最后重启。
#   ② 27M 走公网 scp 常中途断线（Broken pipe）——改 rsync -z 压缩 + --partial 断点续传
#     + 自动重试，md5 校验一致才覆盖线上，绝不拿半截文件重启服务。
deploy: linux
	@ok=0; for i in 1 2 3 4 5 6 7 8; do \
		echo "── 传输尝试 $$i ──"; \
		rsync -z --partial --timeout=60 -e "ssh -o ServerAliveInterval=10 -o ServerAliveCountMax=3" \
			$(BINARY) $(HOST):/tmp/menuagent-server.new && { ok=1; break; }; \
		sleep 3; \
	done; test $$ok -eq 1 || { echo "❌ 传输失败（8 次重试用尽）"; exit 1; }
	@LOCAL=$$(md5 -q $(BINARY)); \
	REMOTE_SUM=$$(ssh $(HOST) "md5sum /tmp/menuagent-server.new | cut -d' ' -f1"); \
	test "$$LOCAL" = "$$REMOTE_SUM" || { echo "❌ md5 不一致（$$LOCAL vs $$REMOTE_SUM），不覆盖"; exit 1; }; \
	echo "  md5 一致：$$LOCAL"
	ssh $(HOST) 'sudo mv /tmp/menuagent-server.new $(REMOTE) \
		&& sudo chmod 755 $(REMOTE) \
		&& sudo systemctl restart menuagent \
		&& sleep 2 && systemctl is-active menuagent'
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		curl -sf -m 3 $(HEALTH) >/dev/null && { echo "✅ 部署完成，healthz ok"; exit 0; }; \
		sleep 1; \
	done; echo "❌ healthz 超时，去查 journalctl -u menuagent"; exit 1

clean:
	rm -rf dist
