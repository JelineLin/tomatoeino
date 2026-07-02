// APIClient —— 和 Go 后端通信的唯一出口（把「怎么连后端」收敛到一处，类似后端的 llm 工厂）。
//
// 两个能力：
//   - fetchHistory()：GET /api/history，普通 JSON。
//   - streamChat(messages:)：POST /api/chat，解析 SSE 流，逐 token 吐出。
import Foundation

struct APIClient {
    // 后端地址。模拟器跑时 localhost 指向 Mac 本机，直接连本地 go run 的服务即可。
    // 真机调试时改成 Mac 的局域网 IP（如 http://192.168.1.10:8080）。
    var baseURL = URL(string: "http://localhost:8080")!

    // MARK: - 历史

    func fetchHistory() async throws -> [Day] {
        let url = baseURL.appendingPathComponent("api/history")
        let (data, response) = try await URLSession.shared.data(from: url)
        try Self.checkOK(response)
        return try JSONDecoder().decode([Day].self, from: data)
    }

    // MARK: - 流式对话

    // streamChat 把整段对话历史发给 /api/chat，返回一个 token 异步序列。
    //
    // 协议（和后端 cmd/server 对齐）：每行 `data: <JSON 字符串>`，
    // 收到 `[DONE]` 结束，收到 `[ERROR]...` 抛错。token 用 JSON 编码所以含换行也安全。
    func streamChat(messages: [ChatMessage]) -> AsyncThrowingStream<String, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = URLRequest(url: baseURL.appendingPathComponent("api/chat"))
                    request.httpMethod = "POST"
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")

                    let payload = ChatRequest(messages: messages.map {
                        ChatRequest.Message(role: $0.role.rawValue, content: $0.text)
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
                        // payload 是 JSON 编码的字符串 token，解出来再吐给 UI。
                        if let data = payload.data(using: .utf8),
                           let token = try? JSONDecoder().decode(String.self, from: data) {
                            continuation.yield(token)
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
        }
        let messages: [Message]
    }
}

enum APIError: LocalizedError {
    case server(String)

    var errorDescription: String? {
        switch self {
        case .server(let msg): return msg
        }
    }
}
