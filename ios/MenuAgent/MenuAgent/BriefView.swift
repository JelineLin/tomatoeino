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

    // 采纳（应用推荐入库）相关：
    @Published var appliedMeals: Set<String> = []  // 本份简报里已采纳入库的餐（lunch/fruit/dinner）
    @Published var applyError: String?             // 采纳失败（弹 alert，不动简报）

    private let api = APIClient()

    // 拉现成的简报。404（还没有）不是错误，进空态。
    func load() async {
        isLoading = true
        errorText = nil
        do {
            brief = try await api.fetchBrief()
            appliedMeals = []  // 换了一份简报，采纳状态重置
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
            appliedMeals = []
        } catch {
            errorText = error.localizedDescription
        }
        isGenerating = false
    }

    // applyMeal 采纳（家长编辑后的）某一餐入库。成功后标记该餐为「已采纳」。
    // 失败走 applyError（弹 alert），不碰简报显示。
    func applyMeal(date: String, field: String, time: String, dishes: [EditDish]) async {
        applyError = nil
        do {
            _ = try await api.applyMeal(date: date, meal: field, time: time, dishes: dishes)
            appliedMeals.insert(field)
        } catch {
            applyError = error.localizedDescription
        }
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
    @State private var editing: EditingTarget?   // 正在编辑的那一餐（打开编辑 sheet）

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
                // 结构化推荐：可逐项编辑 + 一键采纳入库。agent 没登记菜单时（menu==nil）跳过，只显示文字。
                if let menu = brief.menu, !menu.meals.isEmpty {
                    menuSection(menu)
                }
                // 文字版简报（agent 的完整叙述）。
                MarkdownText(text: brief.content)
                    .padding()
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(.secondarySystemBackground))
                    .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            }
            .padding()
        }
        .refreshable { await vm.load() } // 下拉只拉现成的；重新生成走右上角按钮
        .sheet(item: $editing) { target in
            MealEditSheet(
                date: target.date,
                mealField: target.meal.meal,
                mealLabel: mealLabel(target.meal.meal),
                meal: target.meal
            ) { time, dishes in
                Task { await vm.applyMeal(date: target.date, field: target.meal.meal, time: time, dishes: dishes) }
            }
        }
        .alert(
            "采纳没成功",
            isPresented: Binding(get: { vm.applyError != nil }, set: { if !$0 { vm.applyError = nil } })
        ) {
            Button("好", role: .cancel) {}
        } message: {
            Text(vm.applyError ?? "")
        }
    }

    // MARK: - 结构化推荐（可编辑 + 采纳）

    private func menuSection(_ menu: RecommendedMenu) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("今日推荐 · 可编辑后采纳")
                .font(.headline)
            ForEach(menu.meals) { meal in
                mealCard(date: menu.date, meal: meal)
            }
        }
    }

    private func mealCard(date: String, meal: ProposedMeal) -> some View {
        let applied = vm.appliedMeals.contains(meal.meal)
        return VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text(mealIcon(meal.meal))
                Text(mealLabel(meal.meal)).font(.subheadline.bold())
                if !meal.time.isEmpty {
                    Text(meal.time)
                        .font(.caption2)
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if applied {
                    Label("已采纳", systemImage: "checkmark.circle.fill")
                        .font(.caption)
                        .foregroundStyle(.green)
                } else {
                    Button {
                        editing = EditingTarget(date: date, meal: meal)
                    } label: {
                        Label("编辑并采纳", systemImage: "square.and.pencil")
                            .font(.caption)
                    }
                    .buttonStyle(.bordered)
                }
            }
            ForEach(meal.dishes) { dish in
                VStack(alignment: .leading, spacing: 1) {
                    Text(dish.name).font(.callout)
                    if !dish.detail.isEmpty {
                        Text(dish.detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if !meal.reason.isEmpty {
                Text("💡 " + meal.reason)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding()
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemBackground))
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    private func mealLabel(_ field: String) -> String {
        ["lunch": "午餐", "fruit": "水果", "dinner": "晚餐"][field] ?? field
    }

    private func mealIcon(_ field: String) -> String {
        ["lunch": "🍚", "fruit": "🍎", "dinner": "🍲"][field] ?? "🍽"
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

// EditingTarget 是「正在编辑哪一餐」——date + 那一餐。meal.meal（lunch/fruit/dinner）当稳定 id。
private struct EditingTarget: Identifiable {
    let date: String
    let meal: ProposedMeal
    var id: String { meal.meal }
}

// MealEditSheet：编辑推荐的一餐（时间 + 菜品增删改），点「采纳」写进历史。
private struct MealEditSheet: View {
    let date: String
    let mealField: String
    let mealLabel: String
    let onApply: (_ time: String, _ dishes: [EditDish]) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var time: String
    @State private var dishes: [EditDish]

    init(date: String, mealField: String, mealLabel: String, meal: ProposedMeal,
         onApply: @escaping (String, [EditDish]) -> Void) {
        self.date = date
        self.mealField = mealField
        self.mealLabel = mealLabel
        self.onApply = onApply
        _time = State(initialValue: meal.time)
        _dishes = State(initialValue: meal.dishes)
    }

    // 去空白、丢掉空菜名的菜品——采纳前清洗一遍。
    private var cleanedDishes: [EditDish] {
        dishes
            .map { EditDish(name: $0.name.trimmingCharacters(in: .whitespacesAndNewlines),
                            detail: $0.detail.trimmingCharacters(in: .whitespacesAndNewlines)) }
            .filter { !$0.name.isEmpty }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("时间") {
                    TextField("如 12:00（可留空）", text: $time)
                }
                Section("菜品（可增删改）") {
                    ForEach($dishes) { $dish in
                        VStack(alignment: .leading, spacing: 4) {
                            TextField("菜名", text: $dish.name)
                            TextField("做法 / 分量（可留空）", text: $dish.detail)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .onDelete { dishes.remove(atOffsets: $0) }
                    Button {
                        dishes.append(EditDish(name: "", detail: ""))
                    } label: {
                        Label("加一道菜", systemImage: "plus.circle")
                    }
                }
            }
            .navigationTitle("编辑\(mealLabel)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("采纳") {
                        onApply(time.trimmingCharacters(in: .whitespacesAndNewlines), cleanedDishes)
                        dismiss()
                    }
                    .disabled(cleanedDishes.isEmpty)
                }
            }
        }
    }
}
