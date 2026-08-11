import SwiftUI

struct ProfileView: View {
    @EnvironmentObject private var session: AppSession
    @State private var profile = Profile(userID: "", cet4Score: 430, level: "B1", dailyMinutes: 45, goal: "", difficulty: 1, navigationPosition: "bottom", contentMode: "balanced", ieltsTrack: "academic", updatedAt: "")
    @State private var hasLoaded = false
    @State private var isSaving = false
    @State private var message: String?
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if hasLoaded {
                Form {
                    Section("基础水平") {
                        Stepper("CET-4 分数：\(profile.cet4Score)", value: $profile.cet4Score, in: 0...710, step: 5)
                        Picker("CEFR 水平", selection: $profile.level) {
                            ForEach(["A2", "B1", "B2", "C1"], id: \.self) { Text($0).tag($0) }
                        }
                    }
                    Section("学习计划") {
                        Picker("每天学习时间", selection: $profile.dailyMinutes) {
                            Text("30 分钟").tag(30)
                            Text("45 分钟").tag(45)
                            Text("60 分钟").tag(60)
                        }
                        VStack(alignment: .leading) {
                            Text("当前难度：\(profile.difficulty)/5")
                            Slider(value: Binding(get: { Double(profile.difficulty) }, set: { profile.difficulty = Int($0.rounded()) }), in: 1...5, step: 1)
                        }
                        VStack(alignment: .leading, spacing: 8) {
                            Text("学习目标")
                            TextEditor(text: $profile.goal).frame(minHeight: 90)
                        }
                    }
                    Section {
                        Picker("内容安排", selection: $profile.contentMode) {
                            Text("每周混合（推荐）").tag("balanced")
                            Text("新概念能力路径").tag("new-concept")
                            Text("IELTS 风格").tag("ielts")
                            Text("China Daily 新闻").tag("china-daily")
                            Text("IT 技术英语").tag("tech")
                        }
                        if profile.contentMode == "ielts" {
                            Picker("IELTS 类型", selection: $profile.ieltsTrack) {
                                Text("Academic").tag("academic")
                                Text("General Training").tag("general")
                            }
                        }
                    } header: {
                        Text("课程内容")
                    } footer: {
                        Text("混合安排：周一教材能力、周二 China Daily、周三 IT、周四 IELTS、周五个人复习。")
                    }
                    Section {
                        Button {
                            Task { await save(profile) }
                        } label: {
                            HStack {
                                Spacer()
                                if isSaving { ProgressView() }
                                else { Text("保存档案").fontWeight(.semibold) }
                                Spacer()
                            }
                        }
                        .disabled(isSaving)
                        if let message { Text(message).foregroundStyle(.green) }
                        if let errorMessage { Text(errorMessage).foregroundStyle(.red) }
                    }
                    Section {
                        Button("退出登录", role: .destructive) { session.logout() }
                    } footer: {
                        Text("iOS 版使用系统底部导航；网页与桌面版的导航位置设置会原样保留。")
                    }
                }
            } else if let errorMessage {
                ErrorStateView(message: errorMessage) { Task { await load() } }
            } else {
                LoadingStateView(message: "正在读取学习档案…")
            }
        }
        .navigationTitle("我的档案")
        .task { if !hasLoaded { await load() } }
    }

    private func load() async {
        guard let client = session.client else { return }
        errorMessage = nil
        do {
            profile = try await client.profile()
            hasLoaded = true
        }
        catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
    }

    private func save(_ value: Profile) async {
        guard let client = session.client else { return }
        isSaving = true
        message = nil
        errorMessage = nil
        defer { isSaving = false }
        do {
            profile = try await client.updateProfile(ProfileUpdate(
                cet4Score: value.cet4Score,
                level: value.level,
                dailyMinutes: value.dailyMinutes,
                goal: value.goal,
                difficulty: value.difficulty,
                navigationPosition: value.navigationPosition,
                contentMode: value.contentMode,
                ieltsTrack: value.ieltsTrack
            ))
            message = "已保存，后续课程会使用新的难度和目标"
        } catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
    }
}
