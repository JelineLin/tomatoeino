import SwiftUI

struct TodayView: View {
    @EnvironmentObject private var session: AppSession
    @State private var lesson: Lesson?
    @State private var selections: [String: String] = [:]
    @State private var attempt: ReadingAttempt?
    @State private var isLoading = true
    @State private var isSubmitting = false
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if isLoading {
                LoadingStateView(message: "正在准备今天的课程…")
            } else if let errorMessage, lesson == nil {
                ErrorStateView(message: errorMessage) { Task { await load() } }
            } else if let lesson {
                ScrollView {
                    VStack(alignment: .leading, spacing: 22) {
                        header(lesson)
                        sourceCard(lesson)
                        passage(lesson)
                        vocabulary(lesson)
                        questions(lesson)
                        if let errorMessage {
                            Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                                .font(.subheadline).foregroundStyle(.red)
                        }
                    }
                    .padding()
                    .padding(.bottom, 20)
                }
                .refreshable { await load() }
            }
        }
        .navigationTitle("今日课程")
        .task { if lesson == nil { await load() } }
    }

    @ViewBuilder
    private func sourceCard(_ lesson: Lesson) -> some View {
        if !lesson.sourceName.isEmpty {
            VStack(alignment: .leading, spacing: 9) {
                HStack {
                    Text(lesson.exerciseStyle.isEmpty ? lesson.contentMode : lesson.exerciseStyle)
                        .font(.caption.bold()).foregroundStyle(.indigo)
                    Spacer()
                    if !lesson.sourcePublishedAt.isEmpty {
                        Text(lesson.sourcePublishedAt).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Text("内容来源").font(.caption).foregroundStyle(.secondary)
                Text(lesson.sourceName + (lesson.sourceTitle.isEmpty ? "" : " · \(lesson.sourceTitle)"))
                    .font(.headline)
                if !lesson.syllabusFocus.isEmpty {
                    Text("能力点：\(lesson.syllabusFocus)").font(.subheadline)
                }
                if !lesson.adaptationNote.isEmpty {
                    Text(lesson.adaptationNote).font(.caption).foregroundStyle(.secondary)
                }
                if let url = URL(string: lesson.sourceURL), !lesson.sourceURL.isEmpty {
                    Link("查看原始来源", destination: url).font(.subheadline.bold())
                }
            }
            .padding()
            .background(Color.indigo.opacity(0.07), in: RoundedRectangle(cornerRadius: 18))
        }
    }

    private func header(_ lesson: Lesson) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("难度 \(lesson.difficulty)/5")
                    .font(.caption.bold()).foregroundStyle(.indigo)
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .background(.indigo.opacity(0.12), in: Capsule())
                Spacer()
                Label("约 \(lesson.estimatedMinutes) 分钟", systemImage: "clock")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Text(lesson.title).font(.title.bold())
            if !lesson.generationReason.isEmpty {
                Text(lesson.generationReason).font(.subheadline).foregroundStyle(.secondary)
            }
        }
    }

    private func passage(_ lesson: Lesson) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("阅读", systemImage: "text.alignleft").font(.headline)
            Text(lesson.passage)
                .font(.body.leading(.loose))
                .textSelection(.enabled)
        }
        .padding()
        .background(Color.indigo.opacity(0.06), in: RoundedRectangle(cornerRadius: 18))
    }

    private func vocabulary(_ lesson: Lesson) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("重点词汇", systemImage: "character.book.closed.fill").font(.headline)
            ForEach(lesson.vocabulary) { item in
                VStack(alignment: .leading, spacing: 4) {
                    HStack(alignment: .firstTextBaseline) {
                        Text(item.word).font(.headline)
                        if let pronunciation = item.pronunciation, !pronunciation.isEmpty {
                            Text(pronunciation).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    Text(item.meaning)
                    Text(item.example).font(.subheadline).foregroundStyle(.secondary)
                }
                if item.id != lesson.vocabulary.last?.id { Divider() }
            }
        }
        .padding()
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private func questions(_ lesson: Lesson) -> some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack {
                Label("阅读练习", systemImage: "checklist").font(.headline)
                Spacer()
                if let attempt {
                    Text("\(attempt.correct)/\(attempt.total) · \(attempt.accuracy.percentText)")
                        .font(.subheadline.bold()).foregroundStyle(.indigo)
                }
            }
            ForEach(Array(lesson.questions.enumerated()), id: \.element.id) { index, question in
                let review = attempt?.review?.first { $0.questionID == question.id }
                VStack(alignment: .leading, spacing: 10) {
                    if let type = question.type, !type.isEmpty {
                        Text(type.replacingOccurrences(of: "_", with: " ").uppercased())
                            .font(.caption2.bold()).foregroundStyle(.secondary)
                    }
                    Text("\(index + 1). \(question.prompt)").fontWeight(.semibold)
                    ForEach(Array(question.options.enumerated()), id: \.offset) { optionIndex, option in
                        let value = String(UnicodeScalar(65 + optionIndex)!)
                        Button {
                            guard attempt == nil else { return }
                            selections[question.id] = value
                        } label: {
                            HStack(alignment: .top) {
                                Image(systemName: optionIcon(questionID: question.id, value: value, review: review))
                                    .foregroundStyle(optionColor(questionID: question.id, value: value, review: review))
                                Text(option).foregroundStyle(.primary).multilineTextAlignment(.leading)
                                Spacer()
                            }
                            .padding(12)
                            .background(optionBackground(questionID: question.id, value: value, review: review), in: RoundedRectangle(cornerRadius: 12))
                        }
                        .buttonStyle(.plain)
                    }
                    if let review {
                        VStack(alignment: .leading, spacing: 5) {
                            Label(
                                review.isCorrect ? "回答正确" : "正确答案：\(review.correctAnswer)",
                                systemImage: review.isCorrect ? "checkmark.circle.fill" : "lightbulb.fill"
                            )
                            .font(.subheadline.bold())
                            if !review.explanation.isEmpty {
                                Text("解析：\(review.explanation)")
                                    .font(.subheadline)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                        .foregroundStyle(review.isCorrect ? Color.green : Color.orange)
                        .padding(12)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background((review.isCorrect ? Color.green : Color.orange).opacity(0.1), in: RoundedRectangle(cornerRadius: 12))
                    }
                }
            }
            if attempt == nil {
                Button {
                    Task { await submit(lesson) }
                } label: {
                    Group {
                        if isSubmitting { ProgressView().tint(.white) }
                        else { Text("提交答案").fontWeight(.semibold) }
                    }
                    .frame(maxWidth: .infinity).frame(height: 46)
                }
                .buttonStyle(.borderedProminent)
                .disabled(selections.count != lesson.questions.count || isSubmitting)
            } else {
                Label("答案已保存。切换页面或下次打开仍会保留。", systemImage: "checkmark.circle.fill")
                    .font(.subheadline).foregroundStyle(.green)
            }
        }
        .padding()
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private func optionIcon(questionID: String, value: String, review: ReadingReview?) -> String {
        if let review, review.correctAnswer.caseInsensitiveCompare(value) == .orderedSame {
            return "checkmark.circle.fill"
        }
        if review != nil, selections[questionID] == value {
            return "xmark.circle.fill"
        }
        return selections[questionID] == value ? "largecircle.fill.circle" : "circle"
    }

    private func optionColor(questionID: String, value: String, review: ReadingReview?) -> Color {
        if let review, review.correctAnswer.caseInsensitiveCompare(value) == .orderedSame {
            return .green
        }
        if review != nil, selections[questionID] == value {
            return .red
        }
        return .indigo
    }

    private func optionBackground(questionID: String, value: String, review: ReadingReview?) -> Color {
        if let review, review.correctAnswer.caseInsensitiveCompare(value) == .orderedSame {
            return Color.green.opacity(0.13)
        }
        if review != nil, selections[questionID] == value {
            return Color.red.opacity(0.11)
        }
        return selections[questionID] == value ? Color.indigo.opacity(0.1) : Color.secondary.opacity(0.06)
    }

    private func load() async {
        guard let client = session.client else { return }
        isLoading = true
        errorMessage = nil
        do {
            let loaded: Lesson
            do { loaded = try await client.today() }
            catch APIError.server(let code, _) where code == 404 { loaded = try await client.generateToday() }
            lesson = loaded
            if let saved = try await client.latestAnswer(lessonID: loaded.id) {
                attempt = saved
                selections = Dictionary(uniqueKeysWithValues: saved.answers.map { ($0.questionID, $0.value) })
            } else {
                attempt = nil
                selections = [:]
            }
        } catch APIError.unauthorized {
            session.logout()
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    private func submit(_ lesson: Lesson) async {
        guard let client = session.client else { return }
        isSubmitting = true
        defer { isSubmitting = false }
        do {
            let answers = lesson.questions.compactMap { question in
                selections[question.id].map { ReadingAnswer(questionID: question.id, value: $0) }
            }
            attempt = try await client.submitAnswers(lessonID: lesson.id, answers: answers)
        } catch APIError.unauthorized {
            session.logout()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
