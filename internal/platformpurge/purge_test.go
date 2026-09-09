package platformpurge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testUserID = "c733a5d7-7b65-49ac-b6d2-872fd57a4ce6"

// 删除链的两端接在一起跑一遍：account-server 发起 → 产品服务承接 → 数据被清掉。
func TestClientAndHandlerAgreeOnTheWire(t *testing.T) {
	var purged []string
	handler, err := Handler("shared-secret", func(_ context.Context, userID string) error {
		purged = append(purged, userID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(Path, handler)
	product := httptest.NewServer(mux)
	defer product.Close()

	client, err := NewClient("menu", product.URL, "shared-secret", product.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Purge(context.Background(), testUserID); err != nil {
		t.Fatalf("Purge() = %v", err)
	}
	if len(purged) != 1 || purged[0] != testUserID {
		t.Fatalf("产品侧收到的清除目标 = %v", purged)
	}

	// 重试必须能安全地再打一次：幂等由产品侧保证，客户端不做去重。
	if err := client.Purge(context.Background(), testUserID); err != nil {
		t.Fatalf("重试 Purge() = %v", err)
	}
	if len(purged) != 2 {
		t.Fatalf("重试应再次执行清除，得到 %v", purged)
	}
}

// 密钥配错时绝不能被当成「删干净了」——那会让账号数据在业务侧永久残留。
func TestClientTreatsRejectionAsFailure(t *testing.T) {
	handler, err := Handler("right-secret", func(context.Context, string) error {
		t.Fatal("密钥不对不该执行清除")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(Path, handler)
	product := httptest.NewServer(mux)
	defer product.Close()

	client, err := NewClient("menu", product.URL, "wrong-secret", product.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Purge(context.Background(), testUserID); !errors.Is(err, ErrPurgeFailed) {
		t.Fatalf("Purge() error = %v, want ErrPurgeFailed", err)
	}
}

func TestClientFailsClosedOnProductErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "已清除", status: http.StatusNoContent},
		{name: "产品内部错误", status: http.StatusInternalServerError, wantErr: true},
		{name: "密钥被拒", status: http.StatusUnauthorized, wantErr: true},
		{name: "路由没挂载被网关吃掉", status: http.StatusNotFound, wantErr: true},
		{name: "掉进 SPA 兜底拿到 200", status: http.StatusOK, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			product := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer product.Close()
			client, err := NewClient("english", product.URL, "secret", product.Client())
			if err != nil {
				t.Fatal(err)
			}
			err = client.Purge(context.Background(), testUserID)
			if test.wantErr != (err != nil) {
				t.Fatalf("HTTP %d => %v", test.status, err)
			}
			if test.wantErr && !errors.Is(err, ErrPurgeFailed) {
				t.Fatalf("失败必须是 ErrPurgeFailed，得到 %v", err)
			}
		})
	}
}

// 产品侧清除失败必须变成 5xx：返回 2xx 会让 account-server 继续硬删账户，
// 从此再没人知道这份业务数据该属于谁、该被删掉。
func TestHandlerReportsPurgeFailure(t *testing.T) {
	handler, err := Handler("secret", func(context.Context, string) error {
		return errors.New("磁盘只读")
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, Path+testUserID, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("清除失败应返回 500，得到 %d", response.Code)
	}
}

func TestHandlerRejectsUnauthorizedAndMalformedRequests(t *testing.T) {
	handler, err := Handler("secret", func(context.Context, string) error {
		t.Fatal("非法请求不该进到清除逻辑")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name          string
		method        string
		authorization string
		path          string
		want          int
	}{
		{name: "没有凭证", method: http.MethodDelete, path: Path + testUserID, want: http.StatusUnauthorized},
		{name: "凭证错误", method: http.MethodDelete, authorization: "Bearer nope", path: Path + testUserID, want: http.StatusUnauthorized},
		{name: "凭证 scheme 错误", method: http.MethodDelete, authorization: "Basic secret", path: Path + testUserID, want: http.StatusUnauthorized},
		{name: "方法错误", method: http.MethodGet, authorization: "Bearer secret", path: Path + testUserID, want: http.StatusMethodNotAllowed},
		{name: "路径穿越", method: http.MethodDelete, authorization: "Bearer secret", path: Path + "..%2f..%2fetc", want: http.StatusBadRequest},
		{name: "非 UUID", method: http.MethodDelete, authorization: "Bearer secret", path: Path + "home", want: http.StatusBadRequest},
		{name: "大写 UUID", method: http.MethodDelete, authorization: "Bearer secret", path: Path + "C733A5D7-7B65-49AC-B6D2-872FD57A4CE6", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestConstructorsRejectIncompleteConfiguration(t *testing.T) {
	if _, err := NewClient("menu", "http://127.0.0.1:8080", "", nil); err == nil {
		t.Error("没有共享密钥就不该建出客户端")
	}
	if _, err := NewClient("menu", "", "secret", nil); err == nil {
		t.Error("没有产品地址就不该建出客户端")
	}
	if _, err := NewClient("menu", "not-a-url", "secret", nil); err == nil {
		t.Error("非法地址应报错")
	}
	if _, err := Handler("", func(context.Context, string) error { return nil }); err == nil {
		t.Error("没有共享密钥就不该挂载清除接口")
	}
	if _, err := Handler("secret", nil); err == nil {
		t.Error("没有清除实现就不该挂载清除接口")
	}
}

// 产品服务同时托管 SPA：没挂载清除路由时请求会掉进 index.html 兜底，拿到 200。
// 那绝不能算删干净了——否则账户被硬删，业务数据成孤儿。
func TestClientRejectsSPAFallback(t *testing.T) {
	product := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>English Coach</title>"))
	}))
	defer product.Close()

	client, err := NewClient("english", product.URL, "secret", product.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Purge(context.Background(), testUserID); !errors.Is(err, ErrPurgeFailed) {
		t.Fatalf("SPA 兜底的 200 必须当失败，得到 %v", err)
	}
}
