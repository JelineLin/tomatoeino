// 历史界面：拉 /api/history，按天列出 午餐/水果/晚餐 → 每道菜的名字 + 做法明细。
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
}

struct HistoryView: View {
    @StateObject private var vm = HistoryViewModel()

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
                    list
                }
            }
            .navigationTitle("吃饭历史")
            .toolbar {
                Button {
                    Task { await vm.load() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
            }
        }
        .task {
            if vm.days.isEmpty { await vm.load() }
        }
    }

    private var list: some View {
        List(vm.days) { day in
            Section(day.date) {
                mealRow(label: "午餐", meal: day.lunch)
                mealRow(label: "水果", meal: day.fruit)
                mealRow(label: "晚餐", meal: day.dinner)
            }
        }
        .listStyle(.insetGrouped)
        .refreshable { await vm.load() }
    }

    // mealRow 渲染一餐；这一餐缺席（nil）时不显示。
    @ViewBuilder
    private func mealRow(label: String, meal: Meal?) -> some View {
        if let meal, !meal.dishes.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text(label).font(.headline)
                    if !meal.time.isEmpty {
                        Text(meal.time).font(.caption).foregroundStyle(.secondary)
                    }
                }
                ForEach(meal.dishes, id: \.self) { dish in
                    VStack(alignment: .leading, spacing: 2) {
                        Text("· \(dish.name)")
                        if !dish.detail.isEmpty {
                            Text(dish.detail)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .padding(.vertical, 4)
        }
    }
}
