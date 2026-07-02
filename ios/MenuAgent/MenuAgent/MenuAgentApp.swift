// MenuAgent —— 「幼儿备餐助手」iOS 前端入口。
//
// 两个 tab：
//   - 聊天：和 Go 后端的 ReAct agent 对话，回答以 SSE 流式「打字机」呈现。
//   - 历史：拉取后端 /api/history，浏览宝宝每天的 午餐/水果/晚餐。
//
// 后端默认地址 http://localhost:8080（见 APIClient）。模拟器可直接访问 Mac 本机的 localhost。
import SwiftUI

@main
struct MenuAgentApp: App {
    var body: some Scene {
        WindowGroup {
            RootView()
        }
    }
}

// RootView 是两个 tab 的容器。
struct RootView: View {
    var body: some View {
        TabView {
            ChatView()
                .tabItem {
                    Label("聊天", systemImage: "bubble.left.and.bubble.right")
                }
            HistoryView()
                .tabItem {
                    Label("历史", systemImage: "calendar")
                }
        }
    }
}
