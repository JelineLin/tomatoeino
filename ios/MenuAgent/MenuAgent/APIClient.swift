// APIClient —— 和 Go 后端通信的唯一出口（把「怎么连后端」收敛到一处，类似后端的 llm 工厂）。
//
// 两个能力：
//   - fetchHistory()：GET /api/history，普通 JSON。
//   - streamChat(messages:)：POST /api/chat，解析 SSE 流，逐 token 吐出。
import Foundation

struct APIClient {
    // 后端地址：默认连云端 hermas，暂用【明文 HTTP + Bearer】——回到最初能用的方案。
    // 为什么退回明文：jelinelin.com 的阿里云「接入备案」没批下来之前，域名/443/HTTPS
    // 都被阿里云边缘掐 SNI 走不通；IP+自签又得给每台设备做 CA pinning，麻烦。明文最省事。
    // ⚠️ token 走明文传输（自用/亲友可接受）。备案下来后回到 TLS：baseURL 换成
    // https://jelinelin.com、服务端 .env 配 DigiCert 证书、这段注释和 scheme 一起改回。
    var baseURL = URL(string: "http://101.132.191.7:8080")!
    // var baseURL = URL(string: "http://127.0.0.1:8080")!  // ← 本地调试用

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

    // MARK: - 历史反馈

    // submitFeedback 给某一餐记「儿童食用反馈」（POST /api/history/feedback）。
    // rating 传 like/dislike/ok，空串表示清除该餐反馈。后端写完返回整份历史，直接拿去刷新。
    func submitFeedback(date: String, meal: String, rating: String, note: String) async throws -> [Day] {
        struct Body: Encodable {
            let date: String
            let meal: String
            let rating: String
            let note: String
        }
        var req = authorizedRequest(baseURL.appendingPathComponent("api/history/feedback"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(Body(date: date, meal: meal, rating: rating, note: note))
        let (data, response) = try await URLSession.shared.data(for: req)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let msg = String(data: data, encoding: .utf8).flatMap { $0.isEmpty ? nil : $0 }
                ?? "HTTP \(http.statusCode)"
            throw APIError.server(msg)
        }
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

    // MARK: - 库存

    // fetchInventory 拉取家庭库存（GET /api/inventory，可选 keyword 子串过滤）。
    func fetchInventory(keyword: String = "") async throws -> [InventoryItem] {
        var comps = URLComponents(
            url: baseURL.appendingPathComponent("api/inventory"),
            resolvingAgainstBaseURL: false
        )!
        if !keyword.isEmpty {
            comps.queryItems = [URLQueryItem(name: "keyword", value: keyword)]
        }
        let (data, response) = try await URLSession.shared.data(for: authorizedRequest(comps.url!))
        try Self.checkOK(response)
        return try JSONDecoder().decode([InventoryItem].self, from: data)
    }

    // 库存写操作：add=累加入库、set=设为精确值、remove=删除整条。后端写完返回整份账本。
    func addInventory(name: String, quantity: Double, unit: String) async throws -> [InventoryItem] {
        try await writeInventory(op: "add", name: name, quantity: quantity, unit: unit)
    }

    func setInventory(name: String, quantity: Double, unit: String) async throws -> [InventoryItem] {
        try await writeInventory(op: "set", name: name, quantity: quantity, unit: unit)
    }

    func removeInventory(name: String) async throws -> [InventoryItem] {
        try await writeInventory(op: "remove", name: name, quantity: 0, unit: "")
    }

    private func writeInventory(op: String, name: String, quantity: Double, unit: String) async throws -> [InventoryItem] {
        struct Body: Encodable {
            let op: String
            let name: String
            let quantity: Double
            let unit: String
        }
        var req = authorizedRequest(baseURL.appendingPathComponent("api/inventory"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(Body(op: op, name: name, quantity: quantity, unit: unit))
        let (data, response) = try await URLSession.shared.data(for: req)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let msg = String(data: data, encoding: .utf8).flatMap { $0.isEmpty ? nil : $0 }
                ?? "HTTP \(http.statusCode)"
            throw APIError.server(msg)
        }
        return try JSONDecoder().decode([InventoryItem].self, from: data)
    }

    // MARK: - 档案

    // fetchProfile 拉取宝宝档案（GET /api/profile）。空档案也会正常返回（字段大多缺省）。
    func fetchProfile() async throws -> Profile {
        let url = baseURL.appendingPathComponent("api/profile")
        let (data, response) = try await URLSession.shared.data(for: authorizedRequest(url))
        try Self.checkOK(response)
        return try JSONDecoder().decode(Profile.self, from: data)
    }

    // updateProfile 提交档案更新（POST /api/profile）。后端做合并/校验/落盘，返回更新后的档案。
    // 合并语义：空字符串字段保持原值，非 nil 数组整组替换（传空数组即清空）。
    // 出错（如生日格式/未来）时后端会给中文说明，这里原样抛出给界面展示。
    func updateProfile(_ profile: Profile) async throws -> Profile {
        var req = authorizedRequest(baseURL.appendingPathComponent("api/profile"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(profile)
        let (data, response) = try await URLSession.shared.data(for: req)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let msg = String(data: data, encoding: .utf8).flatMap { $0.isEmpty ? nil : $0 }
                ?? "HTTP \(http.statusCode)"
            throw APIError.server(msg)
        }
        return try JSONDecoder().decode(Profile.self, from: data)
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

    // applyMeal 采纳（家长编辑后的）推荐的一餐，写进历史（POST /api/history/apply）。
    // 后端等价于 record_meal，但由家长确认后触发。返回更新后的整份历史。
    func applyMeal(date: String, meal: String, time: String, dishes: [EditDish]) async throws -> [Day] {
        struct DishBody: Encodable {
            let name: String
            let detail: String
        }
        struct Body: Encodable {
            let date: String
            let meal: String
            let time: String
            let dishes: [DishBody]
        }
        let body = Body(
            date: date, meal: meal, time: time,
            dishes: dishes.map { DishBody(name: $0.name, detail: $0.detail) }
        )
        var req = authorizedRequest(baseURL.appendingPathComponent("api/history/apply"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
        let (data, response) = try await URLSession.shared.data(for: req)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let msg = String(data: data, encoding: .utf8).flatMap { $0.isEmpty ? nil : $0 }
                ?? "HTTP \(http.statusCode)"
            throw APIError.server(msg)
        }
        return try JSONDecoder().decode([Day].self, from: data)
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
