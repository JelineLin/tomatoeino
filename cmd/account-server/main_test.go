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
