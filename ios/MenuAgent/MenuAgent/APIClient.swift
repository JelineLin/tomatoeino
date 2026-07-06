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

    // MARK: - 历史

    func fetchHistory() async throws -> [Day] {
        let url = baseURL.appendingPathComponent("api/history")
        let (data, response) = try await URLSession.shared.data(from: url)
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
        let (data, response) = try await URLSession.shared.data(from: components.url!)
        try Self.checkOK(response)
        return try JSONDecoder().decode(Season.self, from: data)
    }

    // MARK: - 流式对话

    // streamChat 把整段对话历史发给 /api/chat，返回一个事件异步序列。
    //
    // 协议（和后端 cmd/server 对齐）：每行 `data: {"type":"thinking|answer|context","text":"..."}`，
    // 收到 `[DONE]` 结束，收到 `[ERROR]...` 抛错。
    // thinking 是过程（模型推理 + 工具轨迹），answer 是最终答案——两条轨道连续下发；
    // context 是流末尾发一次的「工具备忘」，存到消息上、下一轮随历史带回（L1 轨迹回灌）。
    func streamChat(messages: [ChatMessage]) -> AsyncThrowingStream<ChatStreamEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = URLRequest(url: baseURL.appendingPathComponent("api/chat"))
                    request.httpMethod = "POST"
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")

                    let payload = ChatRequest(messages: messages.map {
                        ChatRequest.Message(
                            role: $0.role.rawValue,
                            content: $0.text,
                            context: $0.context.isEmpty ? nil : $0.context
                        )
                    })
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
        let messages: [Message]
    }

    // 后端 SSE 事件的线上格式。
    private struct WireEvent: Decodable {
        let type: String
        let text: String
    }
}

// ChatStreamEvent 是流式对话吐给 UI 的一个增量：过程、答案，或流末尾的工具备忘。
enum ChatStreamEvent {
    case thinking(String)
    case answer(String)
    case context(String)
}

enum APIError: LocalizedError {
    case server(String)

    var errorDescription: String? {
        switch self {
        case .server(let msg): return msg
        }
    }
}
