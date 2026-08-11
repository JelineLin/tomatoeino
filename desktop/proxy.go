package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const englishAPIPath = "/api/english/"

// newAPIProxy 只转发 English Coach API。静态页面请求仍由 Wails 的内嵌资源服务处理，
// 这相当于资金系统里为一个下游开白名单路由，不能退化成任意地址的开放代理。
func newAPIProxy(rawTarget string) (http.Handler, error) {
	target, err := validateAPIBaseURL(rawTarget)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	defaultDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		defaultDirector(r)
		// 使用上游 Host，避免 TLS/SNI 和虚拟主机路由落到错误实例。
		r.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "无法连接 English Coach 后端",
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, englishAPIPath) {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

func validateAPIBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("地址不能为空")
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析地址: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("只支持 http 或 https")
	}
	if target.Host == "" {
		return nil, errors.New("缺少主机名")
	}
	if target.User != nil {
		return nil, errors.New("地址中不能包含用户名或密码")
	}
	if target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("地址中不能包含查询参数或 fragment")
	}
	if target.Path != "" && target.Path != "/" {
		return nil, errors.New("地址只能包含 scheme 和 host，不能带路径前缀")
	}
	target.Path = ""
	return target, nil
}
