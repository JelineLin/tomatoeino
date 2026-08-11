import SwiftUI

@main
struct EnglishCoachApp: App {
    @StateObject private var session = AppSession()

    var body: some Scene {
        WindowGroup {
            Group {
                if session.isChecking {
                    ProgressView("正在连接 English Coach…")
                } else if session.token == nil {
                    LoginView()
                } else {
                    MainTabView()
                }
            }
            .environmentObject(session)
            .tint(.indigo)
        }
    }
}

@MainActor
final class AppSession: ObservableObject {
    @Published private(set) var token: String?
    @Published var isChecking = true
    @Published var loginError: String?

    var client: APIClient? { token.map(APIClient.init(token:)) }

    init() {
        token = KeychainStore.loadToken()
        Task { await validateSavedToken() }
    }

    func login(with rawToken: String) async {
        let candidate = rawToken.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !candidate.isEmpty else {
            loginError = "请输入访问码"
            return
        }
        loginError = nil
        do {
            _ = try await APIClient(token: candidate).profile()
            try KeychainStore.saveToken(candidate)
            token = candidate
        } catch {
            loginError = error.localizedDescription
        }
    }

    func logout() {
        KeychainStore.deleteToken()
        token = nil
    }

    private func validateSavedToken() async {
        defer { isChecking = false }
        guard let token else { return }
        do { _ = try await APIClient(token: token).profile() }
        catch APIError.unauthorized { logout() }
        catch { /* 离线时保留登录状态，进入页面后再显示重试提示。 */ }
    }
}

struct LoginView: View {
    @EnvironmentObject private var session: AppSession
    @State private var token = ""
    @State private var isLoggingIn = false

    var body: some View {
        NavigationStack {
            VStack(spacing: 24) {
                Spacer()
                Image(systemName: "text.book.closed.fill")
                    .font(.system(size: 64))
                    .foregroundStyle(.indigo)
                VStack(spacing: 8) {
                    Text("English Coach").font(.largeTitle.bold())
                    Text("每天一点阅读与朗读练习")
                        .foregroundStyle(.secondary)
                }
                VStack(alignment: .leading, spacing: 10) {
                    SecureField("访问码", text: $token)
                        .textContentType(.password)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .padding(14)
                        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 14))
                    if let error = session.loginError {
                        Text(error).font(.footnote).foregroundStyle(.red)
                    }
                    Button {
                        isLoggingIn = true
                        Task {
                            await session.login(with: token)
                            isLoggingIn = false
                        }
                    } label: {
                        Group {
                            if isLoggingIn { ProgressView().tint(.white) }
                            else { Text("开始学习").fontWeight(.semibold) }
                        }
                        .frame(maxWidth: .infinity).frame(height: 48)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(isLoggingIn)
                }
                .frame(maxWidth: 420)
                Spacer()
                Text("访问码只保存在这台设备的 Keychain 中")
                    .font(.caption).foregroundStyle(.secondary)
            }
            .padding(28)
        }
    }
}

struct MainTabView: View {
    var body: some View {
        TabView {
            NavigationStack { TodayView() }
                .tabItem { Label("今日", systemImage: "sun.max.fill") }
            NavigationStack { RecordView() }
                .tabItem { Label("朗读", systemImage: "waveform") }
            NavigationStack { HistoryView() }
                .tabItem { Label("历史", systemImage: "books.vertical.fill") }
            NavigationStack { ProgressDashboardView() }
                .tabItem { Label("进度", systemImage: "chart.line.uptrend.xyaxis") }
            NavigationStack { ProfileView() }
                .tabItem { Label("我的", systemImage: "person.crop.circle") }
        }
    }
}
