// 今日简报界面：展示后端定时生成的「今日备餐简报」（L4 主动 agent 的产出面）。
//
// 数据来自 /api/brief：后端每天早上定点自动生成（agent 自主查历史+时令写成三餐建议），
// 这个 tab 打开就能看——agent 主动干活，人只负责看结果。
// 还没有简报（后端刚启动、没到点）时给一个「立即生成」按钮走 ?refresh=1 现做。
import SwiftUI
import UIKit // UIPasteboard：推荐菜单一键复制

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
            restoreAppliedState()
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
            restoreAppliedState()
        } catch {
            errorText = error.localizedDescription
        }
        isGenerating = false
    }

    // 换了一份简报后，从后端回写的 applied 标记恢复采纳状态——
    // 已采纳的餐（含编辑后的菜品）由服务端简报缓存带回来，重进 App 不丢。
    private func restoreAppliedState() {
        appliedMeals = Set(brief?.menu?.meals.filter { $0.applied == true }.map(\.meal) ?? [])
    }

    // applyMeal 采纳（家长编辑后的）某一餐入库。成功后标记「已采纳」，并把
    // 编辑后的时间/菜品写回本地卡片——卡片显示的必须是实际采纳的版本，
    // 否则家长编辑完看到原推荐纹丝不动，以为没保存（后端简报缓存同步做了回写）。
    // 失败走 applyError（弹 alert），不碰简报显示。
    func applyMeal(date: String, field: String, time: String, dishes: [EditDish]) async {
        applyError = nil
        do {
            _ = try await api.applyMeal(date: date, meal: field, time: time, dishes: dishes)
            appliedMeals.insert(field)
            if var menu = brief?.menu, let i = menu.meals.firstIndex(where: { $0.meal == field }) {
                menu.meals[i].time = time
                menu.meals[i].dishes = dishes
                menu.meals[i].applied = true
                brief?.menu = menu
            }
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
    @State private var copiedMenu = false        // 复制回执：按钮短暂变「已复制」

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
            MealEditorSheet(
                title: "编辑\(mealLabel(target.meal.meal))",
                confirmLabel: "采纳",
                initialTime: target.meal.time,
                initialDishes: target.meal.dishes
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
            HStack {
                Text("今日推荐 · 可编辑后采纳")
                    .font(.headline)
                Spacer()
                // 一键复制整份推荐：发家庭群/贴备忘录。按钮短暂变「已复制」作为回执。
                Button {
                    UIPasteboard.general.string = menuCopyText(menu)
                    withAnimation { copiedMenu = true }
                    Task {
                        try? await Task.sleep(nanoseconds: 1_500_000_000)
                        withAnimation { copiedMenu = false }
                    }
                } label: {
                    Label(copiedMenu ? "已复制" : "复制", systemImage: copiedMenu ? "checkmark" : "doc.on.doc")
                        .font(.caption)
                }
                .buttonStyle(.bordered)
                .disabled(copiedMenu)
            }
            ForEach(menu.meals) { meal in
                mealCard(date: menu.date, meal: meal)
            }
        }
    }

    // menuCopyText 把结构化推荐排成一段可粘贴的纯文本：日期 + 每餐（时间）+ 菜品明细。
    // 理由不带——复制是给「照着做/发给家人」用的，要的是清单不是论证。
    private func menuCopyText(_ menu: RecommendedMenu) -> String {
        var lines = ["\(menu.date) 推荐菜单"]
        for meal in menu.meals {
            var head = "\(mealIcon(meal.meal)) \(mealLabel(meal.meal))"
            if !meal.time.isEmpty { head += "（\(meal.time)）" }
            lines.append(head)
            for dish in meal.dishes {
                lines.append(dish.detail.isEmpty ? "- \(dish.name)" : "- \(dish.name)：\(dish.detail)")
            }
        }
        return lines.joined(separator: "\n")
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

// （编辑弹层已抽成共享的 MealEditorSheet.swift——推荐页「采纳」和历史页「编辑/补记」共用。）
