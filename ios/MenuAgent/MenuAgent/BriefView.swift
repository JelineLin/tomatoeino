// 今日简报界面：展示后端定时生成的「今日备餐简报」（L4 主动 agent 的产出面）。
//
// 数据来自 /api/brief：后端每天早上定点自动生成（agent 自主查历史+时令写成三餐建议），
// 这个 tab 打开就能看——agent 主动干活，人只负责看结果。
// 还没有简报（后端刚启动、没到点）时给一个「立即生成」按钮走 ?refresh=1 现做。
import SwiftUI

@MainActor
final class BriefViewModel: ObservableObject {
    @Published var brief: DailyBrief?
    @Published var isLoading = false     // 拉现成的（快）
    @Published var isGenerating = false  // 现做一份（agent 要跑几十秒）
    @Published var errorText: String?

    private let api = APIClient()

    // 拉现成的简报。404（还没有）不是错误，进空态。
    func load() async {
        isLoading = true
        errorText = nil
        do {
            brief = try await api.fetchBrief()
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }

    // 让后端现做一份（refresh=1）。生成期间保留旧简报显示，成功后原地替换。
    func regenerate() async {
        guard !isGenerating else { return }
        isGenerating = true
        errorText = nil
        do {
            brief = try await api.fetchBrief(refresh: true)
        } catch {
            errorText = error.localizedDescription
        }
        isGenerating = false
    }

    // 简报是不是今天的——昨天的照样显示，但提示家长可以重新生成。
    var isStale: Bool {
        guard let brief else { return false }
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        return brief.date != fmt.string(from: Date())
    }
}

struct BriefView: View {
    @StateObject private var vm = BriefViewModel()

    var body: some View {
        NavigationStack {
            Group {
                if vm.isLoading && vm.brief == nil {
                    ProgressView("加载中…")
                } else if let brief = vm.brief {
                    content(brief)
                } else if vm.isGenerating {
                    generatingState
                } else {
                    emptyState
                }
            }
            .navigationTitle("今日推荐")
            .toolbar {
                if vm.brief != nil {
                    Button {
                        Task { await vm.regenerate() }
                    } label: {
                        if vm.isGenerating {
                            ProgressView()
                        } else {
                            Image(systemName: "arrow.clockwise")
                        }
                    }
                    .disabled(vm.isGenerating)
                }
            }
        }
        .task {
            if vm.brief == nil { await vm.load() }
        }
    }

    // MARK: - 有简报

    private func content(_ brief: DailyBrief) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header(brief)
                if let err = vm.errorText {
                    errorBanner(err)
                }
                MarkdownText(text: brief.content)
                    .padding()
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(.secondarySystemBackground))
                    .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
            .padding()
        }
        .refreshable { await vm.load() } // 下拉只拉现成的；重新生成走右上角按钮
    }

    // 头卡：日期 + 生成时刻；隔天的简报给一条「过期」提示引导重新生成。
    private func header(_ brief: DailyBrief) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("📋 \(brief.date)")
                    .font(.title3.bold())
                Spacer()
                Text(brief.generatedAt, style: .time)
                    .font(.caption)
                    .opacity(0.85)
            }
            if vm.isGenerating {
                Label("正在重新生成，agent 需要跑一会儿…", systemImage: "hourglass")
                    .font(.footnote)
                    .opacity(0.9)
            } else if vm.isStale {
                Label("这是之前生成的简报，点右上角刷新可重新生成", systemImage: "clock.arrow.circlepath")
                    .font(.footnote)
                    .opacity(0.9)
            }
        }
        .foregroundStyle(.white)
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            LinearGradient(
                colors: [Color.orange, Color(red: 0.95, green: 0.45, blue: 0.2)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
    }

    // MARK: - 空态 / 生成中

    // 还没有简报：说明定时机制 + 一键现做。
    private var emptyState: some View {
        VStack(spacing: 16) {
            Text("🌅")
                .font(.system(size: 56))
            Text("今天的简报还没生成")
                .font(.title3.bold())
            Text("后端每天早上会自动生成三餐推荐\n也可以现在就让 agent 写一份")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            if let err = vm.errorText {
                errorBanner(err)
            }
            Button {
                Task { await vm.regenerate() }
            } label: {
                Label("立即生成", systemImage: "wand.and.stars")
                    .padding(.horizontal, 8)
            }
            .buttonStyle(.borderedProminent)
        }
        .padding()
    }

    // 现做等待态：agent 查历史+时令再写文案，几十秒是正常的，把预期说清楚。
    private var generatingState: some View {
        VStack(spacing: 16) {
            ProgressView()
                .controlSize(.large)
            Text("agent 正在写今天的简报…")
                .font(.headline)
            Text("会先翻宝宝的吃饭历史和当月时令\n通常需要半分钟左右")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .padding()
    }

    private func errorBanner(_ text: String) -> some View {
        Label(text, systemImage: "exclamationmark.triangle")
            .font(.footnote)
            .foregroundStyle(.red)
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.red.opacity(0.08))
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}
