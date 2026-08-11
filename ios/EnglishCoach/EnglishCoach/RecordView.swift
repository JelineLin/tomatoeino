import AVFoundation
import SwiftUI

@MainActor
final class AudioRecorder: ObservableObject {
    @Published private(set) var isRecording = false
    @Published private(set) var startedAt: Date?
    private var recorder: AVAudioRecorder?
    private(set) var fileURL: URL?

    func start() async throws {
        let granted = await withCheckedContinuation { continuation in
            AVAudioSession.sharedInstance().requestRecordPermission { continuation.resume(returning: $0) }
        }
        guard granted else { throw RecordingError.permissionDenied }

        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.record, mode: .spokenAudio)
        try session.setActive(true)
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("english-coach-\(UUID().uuidString).m4a")
        let settings: [String: Any] = [
            AVFormatIDKey: Int(kAudioFormatMPEG4AAC),
            AVSampleRateKey: 44_100,
            AVNumberOfChannelsKey: 1,
            AVEncoderAudioQualityKey: AVAudioQuality.high.rawValue
        ]
        recorder = try AVAudioRecorder(url: url, settings: settings)
        recorder?.prepareToRecord()
        guard recorder?.record() == true else { throw RecordingError.cannotStart }
        fileURL = url
        startedAt = Date()
        isRecording = true
    }

    func stop() -> Int {
        let duration = max(1, Int(Date().timeIntervalSince(startedAt ?? Date()).rounded()))
        recorder?.stop()
        try? AVAudioSession.sharedInstance().setActive(false)
        isRecording = false
        startedAt = nil
        return duration
    }

    func discard() {
        if isRecording { _ = stop() }
        if let fileURL { try? FileManager.default.removeItem(at: fileURL) }
        fileURL = nil
    }
}

enum RecordingError: LocalizedError {
    case permissionDenied, cannotStart
    var errorDescription: String? {
        switch self {
        case .permissionDenied: return "请在系统设置中允许 English Coach 使用麦克风"
        case .cannotStart: return "无法开始录音，请稍后再试"
        }
    }
}

struct RecordView: View {
    @EnvironmentObject private var session: AppSession
    @StateObject private var recorder = AudioRecorder()
    @State private var lesson: Lesson?
    @State private var result: SpeakingAttempt?
    @State private var isLoading = true
    @State private var isUploading = false
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if isLoading {
                LoadingStateView(message: "正在加载朗读材料…")
            } else if let errorMessage, lesson == nil {
                ErrorStateView(message: errorMessage) { Task { await load() } }
            } else if let lesson {
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text(lesson.title).font(.title2.bold())
                        Text(lesson.passage)
                            .font(.body.leading(.loose)).textSelection(.enabled)
                            .padding()
                            .background(Color.indigo.opacity(0.06), in: RoundedRectangle(cornerRadius: 18))
                        recorderPanel(lesson)
                        if let result { resultPanel(result) }
                        if let errorMessage {
                            Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                                .font(.subheadline).foregroundStyle(.red)
                        }
                    }
                    .padding()
                }
            }
        }
        .navigationTitle("朗读练习")
        .task { if lesson == nil { await load() } }
        .onDisappear { if recorder.isRecording { recorder.discard() } }
    }

    private func recorderPanel(_ lesson: Lesson) -> some View {
        VStack(spacing: 18) {
            ZStack {
                Circle().fill(recorder.isRecording ? Color.red.opacity(0.12) : Color.indigo.opacity(0.1))
                    .frame(width: 110, height: 110)
                Image(systemName: recorder.isRecording ? "stop.fill" : "mic.fill")
                    .font(.system(size: 38)).foregroundStyle(recorder.isRecording ? .red : .indigo)
            }
            if recorder.isRecording, let startedAt = recorder.startedAt {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    Text(durationText(Int(context.date.timeIntervalSince(startedAt))))
                        .font(.title3.monospacedDigit().bold())
                }
            } else {
                Text(result == nil ? "准备好后，朗读上面的全文" : "可以重新录制")
                    .foregroundStyle(.secondary)
            }
            Button {
                if recorder.isRecording {
                    let duration = recorder.stop()
                    Task { await upload(lesson: lesson, duration: duration) }
                } else {
                    result = nil
                    errorMessage = nil
                    Task {
                        do { try await recorder.start() }
                        catch { errorMessage = error.localizedDescription }
                    }
                }
            } label: {
                Group {
                    if isUploading { ProgressView().tint(.white) }
                    else { Label(recorder.isRecording ? "完成并提交" : "开始录音", systemImage: recorder.isRecording ? "paperplane.fill" : "mic.fill") }
                }
                .frame(maxWidth: .infinity).frame(height: 46)
            }
            .buttonStyle(.borderedProminent)
            .tint(recorder.isRecording ? .red : .indigo)
            .disabled(isUploading)
        }
        .padding()
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private func resultPanel(_ result: SpeakingAttempt) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Label("本次反馈", systemImage: "sparkles").font(.headline)
            HStack(spacing: 12) {
                MetricCard(title: "得分", value: "\(Int(result.score.rounded()))", systemImage: "star.fill")
                MetricCard(title: "语速", value: "\(Int(result.wpm.rounded())) WPM", systemImage: "speedometer")
            }
            HStack(spacing: 12) {
                MetricCard(title: "准确率", value: result.accuracy.percentText, systemImage: "scope")
                MetricCard(title: "用时", value: durationText(Int(result.durationSeconds.rounded())), systemImage: "clock")
            }
            Text(result.feedback)
            if !result.transcript.isEmpty {
                DisclosureGroup("查看识别文本") {
                    Text(result.transcript).font(.subheadline).foregroundStyle(.secondary).padding(.top, 8)
                }
            }
            if let issues = result.issues, !issues.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("需要留意").font(.subheadline.bold())
                    ForEach(issues) { issue in
                        Text("• \(issue.expected)" + (issue.actual.map { " → \($0)" } ?? ""))
                            .font(.subheadline).foregroundStyle(.secondary)
                    }
                }
            }
        }
        .padding()
        .background(Color.green.opacity(0.08), in: RoundedRectangle(cornerRadius: 18))
    }

    private func load() async {
        guard let client = session.client else { return }
        isLoading = true
        errorMessage = nil
        do {
            do { lesson = try await client.today() }
            catch APIError.server(let code, _) where code == 404 { lesson = try await client.generateToday() }
        } catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
        isLoading = false
    }

    private func upload(lesson: Lesson, duration: Int) async {
        guard let client = session.client, let fileURL = recorder.fileURL else { return }
        isUploading = true
        defer {
            isUploading = false
            recorder.discard()
        }
        do { result = try await client.uploadRecording(lessonID: lesson.id, durationSeconds: duration, fileURL: fileURL) }
        catch APIError.unauthorized { session.logout() }
        catch { errorMessage = error.localizedDescription }
    }

    private func durationText(_ seconds: Int) -> String {
        String(format: "%d:%02d", max(0, seconds) / 60, max(0, seconds) % 60)
    }
}
