// 历史界面：拉 /api/history，按天列出 午餐/水果/晚餐 → 每道菜的名字 + 做法明细。
// 支持两种展示：列表（按天倒序翻）和日历（月历表格，见 CalendarHistoryView）。
import SwiftUI

@MainActor
final class HistoryViewModel: ObservableObject {
    @Published var days: [Day] = []
    @Published var isLoading = false
    @Published var errorText: String?

    private let api = APIClient()

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

    // submitFeedback 给某一餐记反馈；后端返回更新后的整份历史，直接替换（徽章即时刷新）。
    func submitFeedback(date: String, field: String, rating: String, note: String) async {
        do {
            days = try await api.submitFeedback(date: date, meal: field, rating: rating, note: note).reversed()
        } catch {
            errorText = error.localizedDescription
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
                            Task { await vm.submitFeedback(date: date, field: field, rating: rating, note: note) }
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
                    Task { await vm.submitFeedback(date: day.date, field: "lunch", rating: r, note: n) }
                }
                MealRowView(label: "水果", meal: day.fruit, date: day.date, field: "fruit") { r, n in
                    Task { await vm.submitFeedback(date: day.date, field: "fruit", rating: r, note: n) }
                }
                MealRowView(label: "晚餐", meal: day.dinner, date: day.date, field: "dinner") { r, n in
                    Task { await vm.submitFeedback(date: day.date, field: "dinner", rating: r, note: n) }
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
