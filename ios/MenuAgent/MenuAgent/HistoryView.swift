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
            // errorText 是整屏替换的致命门，只配给「一条数据都没有」的首载失败；
            // 已有数据时（切 tab 自动刷新/手动刷新偶发失败）静默保留旧列表——
            // 旧数据可看，比一屏错误有用。
            if days.isEmpty {
                errorText = error.localizedDescription
            }
        }
        isLoading = false
    }

    // submitFeedback 给某道菜记反馈（fire-and-forget，内部串行排队）。
    // 后端返回更新后的整份历史，直接替换（徽章即时刷新）。
    // 失败走 actionError（弹 alert），【绝不】写 errorText——否则一次瞬时 POST 失败会被
    // body 当成整页加载失败、把历史列表/日历整屏顶掉（审查抓到的 bug）。
    func submitFeedback(date: String, field: String, dish: String, rating: String, note: String) {
        let previous = writeChain
        writeChain = Task { @MainActor in
            await previous.value // 等上一笔写完，串行落地，避免旧快照覆盖新状态
            do {
                self.days = try await self.api.submitFeedback(date: date, meal: field, dish: dish, rating: rating, note: note).reversed()
            } catch {
                self.actionError = error.localizedDescription
            }
        }
    }

    // saveMeal 整餐保存（编辑已有 / 补记缺席的一餐），同样串行排队。
    // 走 /api/history/apply 的整餐覆盖：后端按菜名结转菜级反馈，改做法不丢已记的反馈。
    func saveMeal(date: String, field: String, time: String, dishes: [EditDish]) {
        let previous = writeChain
        writeChain = Task { @MainActor in
            await previous.value
            do {
                self.days = try await self.api.applyMeal(date: date, meal: field, time: time, dishes: dishes).reversed()
            } catch {
                self.actionError = error.localizedDescription
            }
        }
    }
}

// 正在编辑/补记的一餐（打开整餐编辑弹层）。meal 为 nil = 这天该餐还没记录（补记）。
struct MealEditTarget: Identifiable {
    let date: String
    let field: String   // lunch/fruit/dinner
    let label: String   // 午餐/水果/晚餐
    let meal: Meal?
    var id: String { "\(date)-\(field)" }
}

struct HistoryView: View {
    // 展示模式。列表适合连续翻最近几天，日历适合按月定位「某天吃了啥」。
    private enum Mode: String, CaseIterable {
        case list = "列表"
        case calendar = "日历"
    }

    @StateObject private var vm = HistoryViewModel()
    @State private var mode: Mode = .list
    @State private var showImport = false
    @State private var mealEditing: MealEditTarget?  // 整餐编辑/补记弹层
    @State private var addPicking = false            // 「记一餐」的日期/餐别选择步

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
                        CalendarHistoryView(
                            days: vm.days,
                            onFeedback: { date, field, dish, rating, note in
                                vm.submitFeedback(date: date, field: field, dish: dish, rating: rating, note: note)
                            },
                            onMealEdit: { date, field, label, meal in
                                mealEditing = MealEditTarget(date: date, field: field, label: label, meal: meal)
                            }
                        )
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
                        addPicking = true
                    } label: {
                        Image(systemName: "plus")
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        showImport = true
                    } label: {
                        Image(systemName: "square.and.arrow.down")
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await vm.load() }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                }
            }
            .sheet(isPresented: $showImport) {
                ImportHistoryView { Task { await vm.load() } }
            }
            // 整餐编辑/补记：推荐页同款编辑器，确认后走 /api/history/apply 覆盖写入。
            .sheet(item: $mealEditing) { target in
                MealEditorSheet(
                    title: "\(target.meal == nil ? "补记" : "编辑") \(target.date) \(target.label)",
                    confirmLabel: "保存",
                    initialTime: target.meal?.time ?? "",
                    initialDishes: target.meal?.dishes.map { EditDish(name: $0.name, detail: $0.detail) } ?? []
                ) { time, dishes in
                    vm.saveMeal(date: target.date, field: target.field, time: time, dishes: dishes)
                }
            }
            // 「记一餐」第一步：选日期 + 餐别（主键，先明确写到哪，防覆盖错记录）。
            .sheet(isPresented: $addPicking) {
                AddMealPickerSheet { date, field, label in
                    let existing = vm.days.first { $0.date == date }?.mealOf(field: field)
                    // 等选择步的 sheet 收完再弹编辑器——连续弹层不留间隙会被 SwiftUI 吞掉第二个。
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.45) {
                        mealEditing = MealEditTarget(date: date, field: field, label: label, meal: existing)
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
        // 每次 tab 出现都重拉：聊天里 record_meal 记的餐、记的反馈都写在服务端，
        // 只加载一次的话切过来看不到最新历史。
        .task {
            await vm.load()
        }
    }

    private var list: some View {
        List(vm.days) { day in
            Section {
                // 一餐的具体渲染在 MealRowView（CalendarHistoryView.swift），列表和日历共用。
                ForEach([("lunch", "午餐"), ("fruit", "水果"), ("dinner", "晚餐")], id: \.0) { field, label in
                    MealRowView(
                        label: label, meal: day.mealOf(field: field), date: day.date, field: field,
                        onFeedback: { d, r, n in
                            vm.submitFeedback(date: day.date, field: field, dish: d, rating: r, note: n)
                        },
                        onEdit: {
                            mealEditing = MealEditTarget(date: day.date, field: field, label: label,
                                                         meal: day.mealOf(field: field))
                        }
                    )
                }
                // 缺席的餐给「补记」入口：漏记的那顿随手补上。
                missingChips(day)
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

    // 一天里缺席的餐 →「补记」胶囊。三餐俱全时整行不出现。
    @ViewBuilder
    private func missingChips(_ day: Day) -> some View {
        let missing = [("lunch", "午餐"), ("fruit", "水果"), ("dinner", "晚餐")].filter { field, _ in
            let m = day.mealOf(field: field)
            return m == nil || m!.dishes.isEmpty
        }
        if !missing.isEmpty {
            HStack(spacing: 6) {
                ForEach(missing, id: \.0) { field, label in
                    Button {
                        mealEditing = MealEditTarget(date: day.date, field: field, label: label, meal: nil)
                    } label: {
                        Text("＋ 补记\(label)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 5)
                            .background(Capsule().fill(Color.primary.opacity(0.06)))
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }
}

// AddMealPickerSheet ——「记一餐」的第一步：选日期 + 餐别，然后进整餐编辑。
// 独立成一步是因为日期/餐别是写入的主键，改错会覆盖别的记录——先让家长明确写到哪。
struct AddMealPickerSheet: View {
    let onPick: (_ date: String, _ field: String, _ label: String) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var date = Date()

    var body: some View {
        NavigationStack {
            Form {
                DatePicker("日期", selection: $date, in: ...Date(), displayedComponents: .date)
                Section("记哪一餐") {
                    ForEach([("lunch", "午餐"), ("fruit", "水果"), ("dinner", "晚餐")], id: \.0) { field, label in
                        Button(label) {
                            onPick(DateFmt.ymd.string(from: date), field, label)
                            dismiss()
                        }
                    }
                }
            }
            .navigationTitle("记一餐")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
            }
        }
        .presentationDetents([.medium])
    }
}
