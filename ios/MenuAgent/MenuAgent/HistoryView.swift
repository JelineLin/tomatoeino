// 历史界面：拉 /api/history，按天列出 午餐/水果/晚餐 → 每道菜的名字 + 做法明细。
// 支持两种展示：列表（按天倒序翻）和日历（月历表格，见 CalendarHistoryView）。
import SwiftUI

@MainActor
final class HistoryViewModel: ObservableObject {
    @Published var days: [Day] = []
    @Published var isLoading = false
    @Published var errorText: String?      // 整页加载失败（致命门，会替换整屏）
    @Published var actionError: String?    // 记反馈等操作失败（非致命，弹 alert，不动列表）

    private let api = APIClient()
    // 反馈写串行化：后一次等前一次完成再发。否则并发保存时，先发起但含较旧快照的响应
    // 若最后到达，会覆盖掉已含新写入的状态，刚存的徽章闪没（审查抓到的低危时序问题）。
    private var writeChain: Task<Void, Never> = Task {}

    func load() async {
        isLoading = true
        errorText = nil
        do {
            // 后端按日期升序返回，这里倒过来——最近的排最前面，符合「翻看最近吃了啥」的直觉。
            days = try await api.fetchHistory().reversed()
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }

    // submitFeedback 给某一餐记反馈（fire-and-forget，内部串行排队）。
    // 后端返回更新后的整份历史，直接替换（徽章即时刷新）。
    // 失败走 actionError（弹 alert），【绝不】写 errorText——否则一次瞬时 POST 失败会被
    // body 当成整页加载失败、把历史列表/日历整屏顶掉（审查抓到的 bug）。
    func submitFeedback(date: String, field: String, rating: String, note: String) {
        let previous = writeChain
        writeChain = Task { @MainActor in
            await previous.value // 等上一笔写完，串行落地，避免旧快照覆盖新状态
            do {
                self.days = try await self.api.submitFeedback(date: date, meal: field, rating: rating, note: note).reversed()
            } catch {
                self.actionError = error.localizedDescription
            }
        }
    }
}

struct HistoryView: View {
    // 展示模式。列表适合连续翻最近几天，日历适合按月定位「某天吃了啥」。
    private enum Mode: String, CaseIterable {
        case list = "列表"
        case calendar = "日历"
    }

    @StateObject private var vm = HistoryViewModel()
    @State private var mode: Mode = .list

    var body: some View {
        NavigationStack {
            Group {
                if vm.isLoading && vm.days.isEmpty {
                    ProgressView("加载中…")
                } else if let err = vm.errorText {
                    VStack(spacing: 12) {
                        Image(systemName: "exclamationmark.triangle")
                            .font(.largeTitle)
                            .foregroundStyle(.secondary)
                        Text("加载失败").font(.headline)
                        Text(err)
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                        Button("重试") { Task { await vm.load() } }
                            .buttonStyle(.borderedProminent)
                    }
                    .padding()
                } else {
                    switch mode {
                    case .list: list
                    case .calendar:
                        CalendarHistoryView(days: vm.days) { date, field, rating, note in
                            vm.submitFeedback(date: date, field: field, rating: rating, note: note)
                        }
                    }
                }
            }
            .navigationTitle("吃饭历史")
            .toolbar {
                ToolbarItem(placement: .principal) {
                    Picker("展示方式", selection: $mode) {
                        ForEach(Mode.allCases, id: \.self) { m in
                            Text(m.rawValue).tag(m)
                        }
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 160)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await vm.load() }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                }
            }
            // 记反馈失败：弹个可关的提示，不动列表/日历（区别于整页加载失败）。
            .alert(
                "反馈没保存成功",
                isPresented: Binding(get: { vm.actionError != nil }, set: { if !$0 { vm.actionError = nil } })
            ) {
                Button("好", role: .cancel) {}
            } message: {
                Text(vm.actionError ?? "")
            }
        }
        .task {
            if vm.days.isEmpty { await vm.load() }
        }
    }

    private var list: some View {
        List(vm.days) { day in
            Section {
                // 一餐的具体渲染在 MealRowView（CalendarHistoryView.swift），列表和日历共用。
                MealRowView(label: "午餐", meal: day.lunch, date: day.date, field: "lunch") { r, n in
                    vm.submitFeedback(date: day.date, field: "lunch", rating: r, note: n)
                }
                MealRowView(label: "水果", meal: day.fruit, date: day.date, field: "fruit") { r, n in
                    vm.submitFeedback(date: day.date, field: "fruit", rating: r, note: n)
                }
                MealRowView(label: "晚餐", meal: day.dinner, date: day.date, field: "dinner") { r, n in
                    vm.submitFeedback(date: day.date, field: "dinner", rating: r, note: n)
                }
            } header: {
                HStack(spacing: 6) {
                    Text(day.date)
                    if let wd = DateFmt.weekdayName(day.date) {
                        Text(wd).foregroundStyle(.secondary)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable { await vm.load() }
    }
}
