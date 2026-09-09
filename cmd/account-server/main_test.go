package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type fakeSchemaChecker struct{ err error }

func (f fakeSchemaChecker) Ready(context.Context) error { return f.err }

func TestHealth(t *testing.T) {
	srv := &server{db: fakePinger{}, schema: fakeSchemaChecker{}}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz = (%d, %q), want (200, ok)", rec.Code, rec.Body.String())
	}
}

func TestReady(t *testing.T) {
	tests := []struct {
		name      string
		dbErr     error
		schemaErr error
		want      int
	}{
		{name: "database available", want: http.StatusOK},
		{name: "database unavailable", dbErr: errors.New("down"), want: http.StatusServiceUnavailable},
		{name: "schema unavailable", schemaErr: errors.New("migration missing"), want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{db: fakePinger{err: tt.dbErr}, schema: fakeSchemaChecker{err: tt.schemaErr}}
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rec := httptest.NewRecorder()
			srv.routes().ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("readyz status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// 配了产品地址却漏配共享密钥是「配置写了一半」——必须起不来，
// 而不是悄悄降级成删号不清业务数据。
func TestProductPurgersRefuseHalfConfiguration(t *testing.T) {
	t.Setenv("MENU_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("ENGLISH_BASE_URL", "")
	if _, err := productPurgers(""); err == nil {
		t.Fatal("没有 PLATFORM_INTERNAL_TOKEN 时应拒绝启动")
	}

	purgers, err := productPurgers("shared-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(purgers) != 1 || purgers[0].Product() != "menu" {
		t.Fatalf("只配了 menu 就只该有 menu，得到 %d 个", len(purgers))
	}

	// 一个产品都没配时能起服（本地开发没有业务进程），删除任务会自行留痕。
	t.Setenv("MENU_BASE_URL", "")
	purgers, err = productPurgers("shared-secret")
	if err != nil || len(purgers) != 0 {
		t.Fatalf("未配置任何产品时应放行: %v, %d", err, len(purgers))
	}
}
