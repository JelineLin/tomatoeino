package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tomato-platform/internal/platformpurge"
)

// 把生产里的那一层层包装原样搭起来：withAuth 在外、SPA 兜底在 "/"、清除接口在
// /internal/。这个测试盯的是路由优先级——清除请求既不能被鉴权中间件拦下
// （它认的是进程间密钥，不是用户会话），也不能掉进 SPA 兜底拿到一个 200。
func TestPurgeRouteSurvivesAuthAndSPAWiring(t *testing.T) {
	const uid = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"
	ctx := context.Background()
	dataDir := t.TempDir()
	reg := newRegistry(dataDir, nil, true, stubEmbedder{}, stubChatModel{}, nil)
	if _, err := reg.get(ctx, uid); err != nil {
		t.Fatal(err)
	}

	webDir := t.TempDir() // 有 index.html 的 SPA 目录：兜底是会返回 200 的
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	purgeHandler, err := platformpurge.Handler("shared-secret", func(_ context.Context, id string) error {
		return reg.purge(id)
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(platformpurge.Path, purgeHandler)
	mux.Handle("/", spaHandler(webDir))
	users, err := loadUsers(filepath.Join(t.TempDir(), "missing.json"), "legacy-token")
	if err != nil {
		t.Fatal(err)
	}
	product := httptest.NewServer(withCORS(withAuth(users, nil, mux)))
	defer product.Close()

	client, err := platformpurge.NewClient("menu", product.URL, "shared-secret", product.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Purge(ctx, uid); err != nil {
		t.Fatalf("Purge() = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "users", uid)); !os.IsNotExist(err) {
		t.Errorf("清除后数据目录仍在: %v", err)
	}

	// 没有密钥的人打同一个路径，只能得到 401——不能因为 SPA 兜底在就变成 200。
	request, err := http.NewRequest(http.MethodDelete, product.URL+platformpurge.Path+uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := product.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无密钥的清除请求 = %d，必须是 401", response.StatusCode)
	}
}
