import Foundation

enum APIError: LocalizedError {
    case invalidResponse
    case unauthorized
    case server(Int, String)

    var errorDescription: String? {
        switch self {
        case .invalidResponse: return "服务器返回了无法识别的内容"
        case .unauthorized: return "访问码不正确或已失效"
        case let .server(_, message): return message.isEmpty ? "服务暂时不可用" : message
        }
    }
}

struct APIClient {
    static let baseURL = URL(string: "https://jelinelin.com")!
    let token: String

    private func request(path: String, method: String = "GET", body: Data? = nil, contentType: String? = nil) -> URLRequest {
        var request = URLRequest(url: Self.baseURL.appendingPathComponent(path))
        request.httpMethod = method
        request.httpBody = body
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let contentType { request.setValue(contentType, forHTTPHeaderField: "Content-Type") }
        request.timeoutInterval = 90
        return request
    }

    private func send<T: Decodable>(_ request: URLRequest, as type: T.Type) async throws -> T {
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw APIError.invalidResponse }
        if http.statusCode == 401 { throw APIError.unauthorized }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw APIError.server(http.statusCode, message ?? String(data: data, encoding: .utf8) ?? "")
        }
        do { return try JSONDecoder().decode(T.self, from: data) }
        catch { throw APIError.invalidResponse }
    }

    func profile() async throws -> Profile {
        try await send(request(path: "api/english/profile"), as: Profile.self)
    }

    func updateProfile(_ profile: ProfileUpdate) async throws -> Profile {
        let body = try JSONEncoder().encode(profile)
        return try await send(request(path: "api/english/profile", method: "POST", body: body, contentType: "application/json"), as: Profile.self)
    }

    func today() async throws -> Lesson {
        try await send(request(path: "api/english/today"), as: Lesson.self)
    }

    func generateToday() async throws -> Lesson {
        struct Response: Decodable { let lesson: Lesson }
        let response = try await send(request(path: "api/english/generate-today", method: "POST", body: Data(), contentType: "application/json"), as: Response.self)
        return response.lesson
    }

    func lessons() async throws -> [Lesson] {
        try await send(request(path: "api/english/lessons"), as: [Lesson].self)
    }

    func latestAnswer(lessonID: Int64) async throws -> ReadingAttempt? {
        var components = URLComponents(url: Self.baseURL.appendingPathComponent("api/english/answers"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "lesson_id", value: String(lessonID))]
        var req = request(path: "api/english/answers")
        req.url = components.url
        do { return try await send(req, as: ReadingAttempt.self) }
        catch APIError.server(let code, _) where code == 404 { return nil }
    }

    func submitAnswers(lessonID: Int64, answers: [ReadingAnswer]) async throws -> ReadingAttempt {
        struct Payload: Encodable { let lessonID: Int64; let answers: [ReadingAnswer]
            enum CodingKeys: String, CodingKey { case lessonID = "lesson_id"; case answers }
        }
        let body = try JSONEncoder().encode(Payload(lessonID: lessonID, answers: answers))
        return try await send(request(path: "api/english/answers", method: "POST", body: body, contentType: "application/json"), as: ReadingAttempt.self)
    }

    func progress() async throws -> Progress {
        try await send(request(path: "api/english/progress"), as: Progress.self)
    }

    func reports() async throws -> [WeeklyReport] {
        try await send(request(path: "api/english/weekly-reports"), as: [WeeklyReport].self)
    }

    func uploadRecording(lessonID: Int64, durationSeconds: Int, fileURL: URL) async throws -> SpeakingAttempt {
        struct Response: Decodable { let attempt: SpeakingAttempt }
        let boundary = "Boundary-\(UUID().uuidString)"
        var body = Data()
        func append(_ string: String) { body.append(Data(string.utf8)) }
        append("--\(boundary)\r\nContent-Disposition: form-data; name=\"lesson_id\"\r\n\r\n\(lessonID)\r\n")
        append("--\(boundary)\r\nContent-Disposition: form-data; name=\"duration_seconds\"\r\n\r\n\(durationSeconds)\r\n")
        append("--\(boundary)\r\nContent-Disposition: form-data; name=\"audio\"; filename=\"reading.m4a\"\r\nContent-Type: audio/mp4\r\n\r\n")
        body.append(try Data(contentsOf: fileURL))
        append("\r\n--\(boundary)--\r\n")
        let response = try await send(request(path: "api/english/attempts", method: "POST", body: body, contentType: "multipart/form-data; boundary=\(boundary)"), as: Response.self)
        return response.attempt
    }
}
