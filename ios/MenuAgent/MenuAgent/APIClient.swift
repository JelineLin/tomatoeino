// APIClient —— 和 Go 后端通信的唯一出口（把「怎么连后端」收敛到一处，类似后端的 llm 工厂）。
//
// 两个能力：
//   - fetchHistory()：GET /api/history，普通 JSON。
//   - streamChat(messages:)：POST /api/chat，解析 SSE 流，逐 token 吐出。
import Foundation
import Security

struct APIClient {
    // 后端地址：默认连云端 hermas（HTTPS，自签证书链）。模拟器和真机统一走它，
    // 库存账本以云端为准。自签证书的信任不靠设备装 CA，而是 CA pinning——
    // ca.der 打包在 app 里，握手时做唯一信任锚（见下方 PinnedCADelegate）。
    // 任何设备装上 app 即可用，零配置。
    // 本地调试也走 HTTPS（证书 SAN 含 127.0.0.1/localhost，同一个 CA 签的，pinning 同样过）。
    // 端口用 443 标准位（URL 里省略）：国内运营商蜂窝网络对 8080 等「HTTP 味」端口
    // 有透明代理，TLS 握手会被掐断（5G 下 -1200）；443 是唯一不被动手脚的端口。
    var baseURL = URL(string: "https://101.132.191.7")!
    // var baseURL = URL(string: "https://127.0.0.1:8080")!  // ← 本地调试用（本地 go run 默认仍是 8080）

    // pinnedSession：全 app 唯一的网络会话，TLS 校验用内置 CA 做锚点。
    // static 保证 delegate/连接池只建一份；所有请求（含 SSE 流）都必须走它，
    // 不许再用 URLSession.shared——shared 走系统信任，连自签后端必失败。
    static let pinnedSession = URLSession(
        configuration: .default,
        delegate: PinnedCADelegate(),
        delegateQueue: nil
    )
    private var session: URLSession { Self.pinnedSession }

    // apiToken 与后端 .env 里的 API_TOKEN 一致，值放在 Secrets.swift（已 gitignore，
    // 见 Secrets.swift.example）——仓库是公开的，密钥绝不能写进要提交的源码。
    // 所有请求统一从 authorizedRequest 出去，别再裸建 URLRequest。
    private let apiToken = Secrets.apiToken

    // authorizedRequest 是全部请求的统一出口：带上鉴权头。
    private func authorizedRequest(_ url: URL) -> URLRequest {
        var req = URLRequest(url: url)
        req.setValue("Bearer \(apiToken)", forHTTPHeaderField: "Authorization")
        return req
    }

    // pinnedData 是 session.data 的包装：请求失败且刚发生过 pinning 拒绝时，
    // 把 SecTrust 的真实原因换上去——否则界面只能看到系统笼统的 -1200。
    private func pinnedData(for request: URLRequest) async throws -> (Data, URLResponse) {
        do { return try await session.data(for: request) }
        catch { throw Self.enrich(error) }
    }

    static func enrich(_ error: Error) -> Error {
        if let reason = PinnedCADelegate.lastFailure {
            return APIError.server("证书校验未通过: \(reason)")
        }
        return error
    }

    // MARK: - 历史

    func fetchHistory() async throws -> [Day] {
        let url = baseURL.appendingPathComponent("api/history")
        let (data, response) = try await pinnedData(for: authorizedRequest(url))
        try Self.checkOK(response)
        return try JSONDecoder().decode([Day].self, from: data)
    }

    // MARK: - 时令

    // fetchSeasonal 查某个月的应季食材。month 传 nil 表示当前月（由后端定，
    // 客户端不自己算「现在几月」，和后端 seasonal_produce 工具保持同一个时钟口径）。
    func fetchSeasonal(month: Int? = nil) async throws -> Season {
        var components = URLComponents(
            url: baseURL.appendingPathComponent("api/seasonal"),
            resolvingAgainstBaseURL: false
        )!
        if let month {
            components.queryItems = [URLQueryItem(name: "month", value: String(month))]
        }
        let (data, response) = try await pinnedData(for: authorizedRequest(components.url!))
        try Self.checkOK(response)
        return try JSONDecoder().decode(Season.self, from: data)
    }

    // MARK: - 今日简报

    // fetchBrief 拉取后端定时生成的「今日备餐简报」。
    //   - refresh=false：拿现成的；后端还没生成时返回 nil（404 不是错误，是「还没有」）。
    //   - refresh=true ：让后端现做一份（agent 要跑几十秒，调用方自己给等待反馈）。
    func fetchBrief(refresh: Bool = false) async throws -> DailyBrief? {
        var components = URLComponents(
            url: baseURL.appendingPathComponent("api/brief"),
            resolvingAgainstBaseURL: false
        )!
        if refresh {
            components.queryItems = [URLQueryItem(name: "refresh", value: "1")]
        }
        var request = authorizedRequest(components.url!)
        request.timeoutInterval = refresh ? 180 : 15 // 现做要等 agent 跑完，放宽超时

        let (data, response) = try await pinnedData(for: request)
        if let http = response as? HTTPURLResponse, http.statusCode == 404 {
            return nil // 还没有简报——空态，不抛错
        }
        try Self.checkOK(response)
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601 // 后端已截断到秒，标准 ISO8601 直接解
        return try decoder.decode(DailyBrief.self, from: data)
    }

    // MARK: - 流式对话

    // streamChat 把整段对话历史发给 /api/chat，返回一个事件异步序列。
    //
    // 协议（和后端 cmd/server 对齐）：每行 `data: {"type":"thinking|answer|context|session","text":"..."}`，
    // 收到 `[DONE]` 结束，收到 `[ERROR]...` 抛错。
    // thinking 是过程（模型推理 + 工具轨迹），answer 是最终答案——两条轨道连续下发；
    // context 是流末尾发一次的「工具备忘」（L1 轨迹回灌）；
    // session 是流末尾发一次的「会话钥匙」（L2）——下一轮带回，后端直接用它存的
    // 全保真历史（含真实工具消息）。messages 仍然全量带：会话过期/后端重启时自动降级 L1。
    func streamChat(messages: [ChatMessage], sessionID: String = "") -> AsyncThrowingStream<ChatStreamEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = authorizedRequest(baseURL.appendingPathComponent("api/chat"))
                    request.httpMethod = "POST"
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")

                    let payload = ChatRequest(
                        session_id: sessionID.isEmpty ? nil : sessionID,
                        messages: messages.map {
                            ChatRequest.Message(
                                role: $0.role.rawValue,
                                content: $0.text,
                                context: $0.context.isEmpty ? nil : $0.context
                            )
                        }
                    )
                    request.httpBody = try JSONEncoder().encode(payload)

                    let (bytes, response) = try await APIClient.pinnedSession.bytes(for: request)
                    try Self.checkOK(response)

                    for try await line in bytes.lines {
                        guard line.hasPrefix("data: ") else { continue }
                        let payload = String(line.dropFirst("data: ".count))

                        if payload == "[DONE]" { break }
                        if payload.hasPrefix("[ERROR]") {
                            let msg = String(payload.dropFirst("[ERROR]".count))
                            throw APIError.server(msg)
                        }
                        // payload 是 JSON 事件对象，按 type 分流给 UI。
                        if let data = payload.data(using: .utf8),
                           let ev = try? JSONDecoder().decode(WireEvent.self, from: data) {
                            switch ev.type {
                            case "thinking": continuation.yield(.thinking(ev.text))
                            case "context": continuation.yield(.context(ev.text))
                            case "session": continuation.yield(.session(ev.text))
                            default: continuation.yield(.answer(ev.text))
                            }
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: Self.enrich(error))
                }
            }
            // 调用方取消（如界面消失）时，连带取消底层网络任务。
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: - 辅助

    private static func checkOK(_ response: URLResponse) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            throw APIError.server("HTTP \(http.statusCode)")
        }
    }

    private struct ChatRequest: Encodable {
        struct Message: Encodable {
            let role: String
            let content: String
            // 工具备忘（L1 轨迹回灌）：nil 时 JSONEncoder 自动省略该字段。
            let context: String?
        }
        // 会话钥匙（L2）：nil 时省略，后端视作新会话/L1 全量模式。
        let session_id: String?
        let messages: [Message]
    }

    // 后端 SSE 事件的线上格式。
    private struct WireEvent: Decodable {
        let type: String
        let text: String
    }
}

// ChatStreamEvent 是流式对话吐给 UI 的一个增量：过程、答案，或流末尾的备忘/会话钥匙。
enum ChatStreamEvent {
    case thinking(String)
    case answer(String)
    case context(String)
    case session(String)
}

enum APIError: LocalizedError {
    case server(String)

    var errorDescription: String? {
        switch self {
        case .server(let msg): return msg
        }
    }
}

// MARK: - CA pinning

// PinnedCADelegate 在 TLS 握手的「服务器信任」挑战里，用 app 内置的自建 CA
// 做**唯一**信任锚校验服务器证书链：
//   - SetAnchorCertificatesOnly(true) 把系统根证书全部排除——只认自家 CA，
//     连「设备被装了恶意描述文件」这种场景都防住（比装 ca.crt 更安全）；
//   - 域名/IP 匹配、有效期等其余校验仍由系统 SecTrustEvaluateWithError 完成，一项不少。
// ca.der 由 certs/ca.crt 转换而来（openssl x509 -outform der），随包分发；
// 重新生成 CA 后记得重导一份进 app，否则新证书链会被这里拒掉。
final class PinnedCADelegate: NSObject, URLSessionDelegate {
    // 最近一次 pinning 失败的具体原因（SecTrust 的原话）。
    // 系统把拒绝包装成笼统的 -1200/-999，真实原因只有这里有——
    // APIClient 抛错时会把它拼进错误文本，直接显示到界面上，真机排查全靠它。
    static var lastFailure: String?
    // 【临时诊断】delegate 被调用的次数——用来判断真机上握手失败发生在
    // 信任评估之前（次数不涨）还是之后（次数涨了）。问题定位后删。
    static var callCount = 0

    // 内置 CA，进程内加载一次。加载失败说明包坏了——fail closed，拒绝所有 TLS。
    private static let anchorCA: SecCertificate? = {
        guard let url = Bundle.main.url(forResource: "ca", withExtension: "der"),
              let data = try? Data(contentsOf: url) else { return nil }
        return SecCertificateCreateWithData(nil, data as CFData)
    }()

    func urlSession(
        _ session: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        Self.callCount += 1
        // 只接管「服务器信任」类挑战；其它（HTTP Basic 之类）交回系统默认处理。
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        guard let trust = challenge.protectionSpace.serverTrust else {
            Self.lastFailure = "挑战里没有 serverTrust"
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        guard let ca = Self.anchorCA else {
            Self.lastFailure = "内置 ca.der 加载失败（包里缺文件或格式不对）"
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        SecTrustSetAnchorCertificates(trust, [ca] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true) // 只认内置 CA，不认系统根
        var evalError: CFError?
        if SecTrustEvaluateWithError(trust, &evalError) {
            Self.lastFailure = nil
            completionHandler(.useCredential, URLCredential(trust: trust))
        } else {
            let ns = evalError as Error? as NSError?
            var detail = ns?.localizedDescription ?? "SecTrustEvaluateWithError 返回 false（无错误对象）"
            // userInfo 里常藏着逐张证书的具体不合格原因，一并带上。
            if let underlying = ns?.userInfo[NSUnderlyingErrorKey] as? NSError {
                detail += "｜底层: \(underlying.localizedDescription)"
            }
            Self.lastFailure = detail
            completionHandler(.cancelAuthenticationChallenge, nil)
        }
    }
}
