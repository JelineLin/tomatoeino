// MenuAgent —— 「幼儿备餐助手」iOS 前端入口。
//
// 四个 tab：
//   - 今日：展示后端定时生成的「今日备餐简报」（agent 主动产出，打开即看）。
//   - 聊天：和 Go 后端的 ReAct agent 对话，回答以 SSE 流式「打字机」呈现。
//   - 历史：拉取后端 /api/history，浏览宝宝每天的 午餐/水果/晚餐。
//   - 时令：拉取后端 /api/seasonal，按月查应季蔬菜/水果/水产和备餐提示。
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

// RootView 是四个 tab 的容器。「今日」放第一位：
// 家长早上打开 app 最想看的就是「今天吃什么」，主动产出的价值要摆在门口。
struct RootView: View {
    var body: some View {
        TabView {
            BriefView()
                .tabItem {
                    Label("今日", systemImage: "sun.max")
                }
            ChatView()
                .tabItem {
                    Label("聊天", systemImage: "bubble.left.and.bubble.right")
                }
            HistoryView()
                .tabItem {
                    Label("历史", systemImage: "calendar")
                }
            SeasonalView()
                .tabItem {
                    Label("时令", systemImage: "leaf")
                }
        }
        // 全局主色：暖橙——备餐 app 的「食欲色」，tab/按钮/选中态一起生效。
        .tint(.orange)
    }
}
