import SwiftUI

struct HistoryView: View {
    @EnvironmentObject private var session: AppSession
    @State private var lessons: [Lesson]?
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if let lessons {
                if lessons.isEmpty {
                    EmptyStateView(title: "还没有课程", message: "完成第一课后，这里会留下学习轨迹", systemImage: "books.vertical")
                } else {
                    List(lessons) { lesson in
                        NavigationLink {
                            LessonDetailView(lesson: lesson)
                        } label: {
                            VStack(alignment: .leading, spacing: 7) {
                                HStack {
                                    Text(lesson.date)
                                    Spacer()
                                    Text("难度 \(lesson.difficulty)/5")
                                }
                                .font(.caption).foregroundStyle(.secondary)
                                Text(lesson.title).font(.headline)
                                if !lesson.sourceName.isEmpty {
                                    Text(lesson.sourceName + (lesson.exerciseStyle.isEmpty ? "" : " · \(lesson.exerciseStyle)"))
                                        .font(.caption).foregroundStyle(.indigo)
                                }
                                Text(lesson.passage).font(.subheadline).foregroundStyle(.secondary).lineLimit(3)
                                HStack {
                                    Label("\(lesson.vocabulary.count) 短语/词汇", systemImage: "character.book.closed")
                                    Label("\(lesson.questions.count) 道题", systemImage: "checklist")
                                }
                                .font(.caption).foregroundStyle(.indigo)
                            }
                            .padding(.vertical, 7)
                        }
                    }
                    .refreshable { await load() }
                }
            } else if let errorMessage {
                ErrorStateView(message: errorMessage) { Task { await load() } }
            } else {
                LoadingStateView(message: "正在读取课程历史…")
            }
        }
        .navigationTitle("课程历史")
        .task { if lessons == nil { await load() } }
    }

    private func load() async {
        guard let client = session.client else { return }
        errorMessage = nil
        do { lessons = try await client.lessons() }
        catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
    }
}

struct LessonDetailView: View {
    let lesson: Lesson
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                HStack {
                    Text(lesson.date).foregroundStyle(.secondary)
                    Spacer()
                    Text("难度 \(lesson.difficulty)/5").foregroundStyle(.indigo)
                }.font(.subheadline)
                Text(lesson.title).font(.title.bold())
                if !lesson.sourceName.isEmpty {
                    VStack(alignment: .leading, spacing: 5) {
                        Text(lesson.sourceName + (lesson.sourceTitle.isEmpty ? "" : " · \(lesson.sourceTitle)"))
                            .font(.headline).foregroundStyle(.indigo)
                        if !lesson.adaptationNote.isEmpty {
                            Text(lesson.adaptationNote).font(.caption).foregroundStyle(.secondary)
                        }
                        if let url = URL(string: lesson.sourceURL), !lesson.sourceURL.isEmpty {
                            Link("查看原始来源", destination: url).font(.subheadline.bold())
                        }
                    }
                    .padding()
                    .background(Color.indigo.opacity(0.07), in: RoundedRectangle(cornerRadius: 16))
                }
                Text(lesson.passage).font(.body.leading(.loose)).textSelection(.enabled)
                Divider()
                Text("重点短语 + 词汇").font(.headline)
                ForEach(lesson.vocabulary) { item in
                    VStack(alignment: .leading, spacing: 5) {
                        Text(item.displayPhrase)
                            .fontWeight(.semibold).foregroundStyle(.indigo)
                        if let phraseMeaning = item.phraseMeaning, !phraseMeaning.isEmpty {
                            Text(phraseMeaning)
                        }
                        HStack(alignment: .firstTextBaseline) {
                            Text(item.word).font(.caption.bold()).foregroundStyle(.indigo)
                            if let pronunciation = item.pronunciation, !pronunciation.isEmpty {
                                Text(pronunciation).font(.caption).foregroundStyle(.secondary)
                            }
                            Text(item.meaning).font(.subheadline)
                        }
                        Text("例句：\(item.example)").font(.subheadline).foregroundStyle(.secondary)
                    }
                }
            }.padding()
        }
        .navigationTitle("课程回顾")
        .navigationBarTitleDisplayMode(.inline)
    }
}

struct ProgressDashboardView: View {
    @EnvironmentObject private var session: AppSession
    @State private var progress: Progress?
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if let progress {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("当前水平").font(.subheadline).foregroundStyle(.white.opacity(0.8))
                            HStack(alignment: .bottom) {
                                Text(progress.level).font(.system(size: 42, weight: .bold))
                                Spacer()
                                Text("难度 \(progress.difficulty)/5").fontWeight(.semibold)
                            }
                            if progress.recommendedDifficulty != progress.difficulty {
                                Text("下周建议调整为难度 \(progress.recommendedDifficulty)/5")
                                    .font(.caption).foregroundStyle(.white.opacity(0.82))
                            }
                        }
                        .foregroundStyle(.white).padding()
                        .background(LinearGradient(colors: [.indigo, .purple], startPoint: .topLeading, endPoint: .bottomTrailing), in: RoundedRectangle(cornerRadius: 20))

                        HStack(spacing: 12) {
                            MetricCard(title: "课程完成率", value: progress.completionRate4W.percentText, systemImage: "checkmark.circle")
                            MetricCard(title: "阅读正确率", value: progress.readingAccuracy4W.percentText, systemImage: "text.book.closed")
                        }
                        HStack(spacing: 12) {
                            MetricCard(title: "朗读一致度", value: progress.speakingAccuracy4W.percentText, systemImage: "waveform")
                            MetricCard(title: "平均语速", value: "\(Int(progress.speakingSpeedWPM.rounded())) WPM", systemImage: "speedometer")
                        }
                        Text("已完成 \(progress.lessonsCompleted)/\(progress.lessonsAssigned) 课")
                            .font(.subheadline).foregroundStyle(.secondary)

                        reviewSection(title: "需要复习的词", values: progress.recurringErrors ?? [], empty: "完成朗读后会聚合高频问题词")
                        reviewSection(title: "薄弱模式", values: progress.weakPatterns ?? [], empty: "继续练习后会形成更明确的建议")

                        NavigationLink {
                            WeeklyReportsView()
                        } label: {
                            Label("查看每周学习报告", systemImage: "doc.text.magnifyingglass")
                                .frame(maxWidth: .infinity).padding()
                        }
                        .buttonStyle(.borderedProminent)
                    }
                    .padding()
                }
                .refreshable { await load() }
            } else if let errorMessage {
                ErrorStateView(message: errorMessage) { Task { await load() } }
            } else {
                LoadingStateView(message: "正在汇总最近四周…")
            }
        }
        .navigationTitle("学习进度")
        .task { if progress == nil { await load() } }
    }

    private func reviewSection(title: String, values: [String], empty: String) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.headline)
            if values.isEmpty {
                Text(empty).font(.subheadline).foregroundStyle(.secondary)
            } else {
                ForEach(values, id: \.self) { Text("• \($0)") }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading).padding()
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private func load() async {
        guard let client = session.client else { return }
        errorMessage = nil
        do { progress = try await client.progress() }
        catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
    }
}

struct WeeklyReportsView: View {
    @EnvironmentObject private var session: AppSession
    @State private var reports: [WeeklyReport]?
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if let reports {
                if reports.isEmpty {
                    EmptyStateView(title: "还没有周报", message: "积累一周学习记录后会生成报告", systemImage: "doc.text")
                } else {
                    List(reports) { report in
                        VStack(alignment: .leading, spacing: 10) {
                            Text("本周起始：\(report.weekStart)").font(.caption).foregroundStyle(.secondary)
                            Text(report.summary).font(.headline)
                            HStack {
                                Text("完成 \(report.completionRate.percentText)")
                                Text("阅读 \(report.readingAccuracy.percentText)")
                                Text("\(Int(report.speakingWPM.rounded())) WPM")
                            }.font(.caption).foregroundStyle(.indigo)
                            if let words = report.problemWords, !words.isEmpty { Text("问题词：\(words.joined(separator: "、"))").font(.subheadline) }
                            Text("下一步：\(report.nextFocus)").font(.subheadline).foregroundStyle(.secondary)
                        }.padding(.vertical, 8)
                    }
                }
            } else if let errorMessage {
                ErrorStateView(message: errorMessage) { Task { await load() } }
            } else {
                LoadingStateView(message: "正在读取周报…")
            }
        }
        .navigationTitle("每周报告")
        .task { if reports == nil { await load() } }
    }

    private func load() async {
        guard let client = session.client else { return }
        errorMessage = nil
        do { reports = try await client.reports() }
        catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
    }
}
