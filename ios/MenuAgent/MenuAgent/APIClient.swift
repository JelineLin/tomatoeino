// APIClient —— 和 Go 后端通信的唯一出口（把「怎么连后端」收敛到一处，类似后端的 llm 工厂）。
//
// 两个能力：
//   - fetchHistory()：GET /api/history，普通 JSON。
//   - streamChat(messages:)：POST /api/chat，解析 SSE 流，逐 token 吐出。
import Foundation

struct APIClient {
    // 后端地址。模拟器和 Mac 是同一台机器，走 localhost；
    // 真机和 Mac 不是一台机器，得走 Mac 的局域网 IP（手机和 Mac 需在同一 Wi-Fi）。
    // 编译期按目标环境二选一——IP 变了只需改下面这一处（Mac 上查：ipconfig getifaddr en0）。
    #if targetEnvironment(simulator)
    var baseURL = URL(string: "http://localhost:8080")!
    #else
    var baseURL = URL(string: "http://192.168.1.24:8080")!
    #endif

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

    // MARK: - 历史

    func fetchHistory() async throws -> [Day] {
        let url = baseURL.appendingPathComponent("api/history")
        let (data, response) = try await URLSession.shared.data(for: authorizedRequest(url))
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
        let (data, response) = try await URLSession.shared.data(for: authorizedRequest(components.url!))
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

        let (data, response) = try await URLSession.shared.data(for: request)
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

                    let (bytes, response) = try await URLSession.shared.bytes(for: request)
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
                    continuation.finish(throwing: error)
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
