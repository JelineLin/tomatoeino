package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

const defaultAPIBaseURL = "https://jelinelin.com"

// frontend 是 english-web 静态导出的构建结果。桌面构建前由 stage-assets 更新，
// 最终页面和 Go 桌面壳一起进入 .app，用户机器不需要 Node.js。
//
//go:embed all:frontend
var frontend embed.FS

func main() {
	apiURL := os.Getenv("ENGLISH_DESKTOP_API_URL")
	if apiURL == "" {
		apiURL = defaultAPIBaseURL
	}
	proxy, err := newAPIProxy(apiURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "English Coach 后端地址无效:", err)
		os.Exit(1)
	}

	err = wails.Run(&options.App{
		Title:     "English Coach",
		Width:     1040,
		Height:    820,
		MinWidth:  720,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets:  frontend,
			Handler: proxy,
		},
		BackgroundColour: &options.RGBA{R: 245, G: 247, B: 255, A: 255},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dd28742e-89ce-4f7c-9668-b3f04ad8b75e",
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarDefault(),
			Appearance: mac.DefaultAppearance,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "English Coach 桌面端启动失败:", err)
		os.Exit(1)
	}
}
