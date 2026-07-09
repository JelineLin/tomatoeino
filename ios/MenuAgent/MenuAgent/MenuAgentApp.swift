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
            DiagnosticsView()
                .tabItem {
                    Label("诊断", systemImage: "stethoscope")
                }
        }
        // 全局主色：暖橙——备餐 app 的「食欲色」，tab/按钮/选中态一起生效。
        .tint(.orange)
    }
}

// 【临时】TLS 诊断页：三个探针一次跑完，把「真机 -1200」的病灶定位到具体一层。
// 问题解决后整个删掉（连同 RootView 里的 tab）。
struct DiagnosticsView: View {
    @State private var lines: [String] = ["点「跑诊断」开始"]
    @State private var running = false

    var body: some View {
        NavigationStack {
            List {
                ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                    Text(line).font(.footnote.monospaced()).textSelection(.enabled)
                }
                Button(running ? "跑着呢…" : "跑诊断") {
                    Task { await run() }
                }
                .disabled(running)
            }
            .navigationTitle("TLS 诊断")
        }
    }

    // 盲信会话：无条件接受任何服务器证书（仅诊断用！）。
    // 如果连它都 -1200，问题就与信任判定无关，是握手协议层本身。
    private static let blindSession = URLSession(
        configuration: .ephemeral,
        delegate: BlindTrustDelegate(),
        delegateQueue: nil
    )
    // 全新 pinned 会话（ephemeral，无连接复用/缓存），排除连接池干扰。
    private static let freshPinned = URLSession(
        configuration: .ephemeral,
        delegate: PinnedCADelegate(),
        delegateQueue: nil
    )

    private func run() async {
        running = true
        lines = []
        // P1：全新 pinned 会话 → 自家服务器（复现问题，无连接复用干扰）
        await probe("P1 pinned→hermas", Self.freshPinned, "https://101.132.191.7/healthz")
        // P2：pinned → apple.com（验证 delegate 被调用；预期被拒）
        await probe("P2 pinned→apple", APIClient.pinnedSession, "https://www.apple.com")
        // P3：系统默认会话（无 delegate）→ 自家服务器
        await probe("P3 shared→hermas", URLSession.shared, "https://101.132.191.7/healthz")
        // P4：盲信一切 → 自家服务器（终极对照：还失败就是协议层死）
        await probe("P4 blind→hermas", Self.blindSession, "https://101.132.191.7/healthz")
        // P5/P6：协议版本二分——中间人对 TLS 版本挑不挑食？
        // （1.2 的服务器证书是明文可见的，1.3 是加密的——如果只有一边被杀，凶手立刻现形）
        let cfg12 = URLSessionConfiguration.ephemeral
        cfg12.tlsMaximumSupportedProtocolVersion = .TLSv12
        let s12 = URLSession(configuration: cfg12, delegate: BlindTrustDelegate(), delegateQueue: nil)
        await probe("P5 blind+max1.2→hermas", s12, "https://101.132.191.7/healthz")
        let cfg13 = URLSessionConfiguration.ephemeral
        cfg13.tlsMinimumSupportedProtocolVersion = .TLSv13
        let s13 = URLSession(configuration: cfg13, delegate: BlindTrustDelegate(), delegateQueue: nil)
        await probe("P6 blind+min1.3→hermas", s13, "https://101.132.191.7/healthz")
        running = false
    }

    private func probe(_ name: String, _ session: URLSession, _ urlStr: String) async {
        let before = PinnedCADelegate.callCount
        PinnedCADelegate.lastFailure = nil
        do {
            let (_, resp) = try await session.data(for: URLRequest(url: URL(string: urlStr)!))
            let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
            lines.append("✅ \(name): HTTP \(code)｜delegate调用 +\(PinnedCADelegate.callCount - before)")
        } catch {
            let ns = error as NSError
            // 逐层剥出底层错误码——kCFStreamErrorDomainSSL(=3) 的 code 是 SecureTransport
            // OSStatus（如 -9807 链无效 / -9802 对端 fatal alert），这才是真正的死因。
            var deep = ""
            if let u = ns.userInfo[NSUnderlyingErrorKey] as? NSError {
                deep += "｜under=[\(u.domain) \(u.code)]"
                if let uu = u.userInfo[NSUnderlyingErrorKey] as? NSError {
                    deep += " under2=[\(uu.domain) \(uu.code)]"
                }
            }
            if let sslCode = ns.userInfo["_kCFStreamErrorCodeKey"] {
                deep += "｜streamCode=\(sslCode)"
            }
            lines.append("❌ \(name): [\(ns.domain) \(ns.code)] \(ns.localizedDescription)｜delegate调用 +\(PinnedCADelegate.callCount - before)｜trace=\(PinnedCADelegate.lastFailure ?? "无")\(deep)")
        }
    }
}

// 【临时诊断】无条件信任任何证书的 delegate——只给诊断页用，定位后删。
final class BlindTrustDelegate: NSObject, URLSessionDelegate {
    func urlSession(
        _ session: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        if challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
           let trust = challenge.protectionSpace.serverTrust {
            completionHandler(.useCredential, URLCredential(trust: trust))
        } else {
            completionHandler(.performDefaultHandling, nil)
        }
    }
}
