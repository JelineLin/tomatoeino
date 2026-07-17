// MealEditorSheet —— 「编辑一餐」的通用弹层：时间 + 菜品增删改。
//
// 两个用户共用（写入语义都是 /api/history/apply 的整餐覆盖）：
//   - 今日推荐：编辑推荐后「采纳并写入历史」（BriefView）；
//   - 历史：改已有的餐 / 给某天补记一餐（HistoryView / 日历）。
// 菜级反馈不在这里编辑——整餐覆盖时后端按菜名自动结转，改做法不丢反馈。
import SwiftUI

struct MealEditorSheet: View {
    let title: String
    let confirmLabel: String
    let onSave: (_ time: String, _ dishes: [EditDish]) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var time: String
    @State private var dishes: [EditDish]

    init(title: String, confirmLabel: String, initialTime: String, initialDishes: [EditDish],
         onSave: @escaping (String, [EditDish]) -> Void) {
        self.title = title
        self.confirmLabel = confirmLabel
        self.onSave = onSave
        _time = State(initialValue: initialTime)
        // 补记场景从零开始：给一行空菜品当起点，别让家长先点一次「加菜」。
        _dishes = State(initialValue: initialDishes.isEmpty ? [EditDish(name: "", detail: "")] : initialDishes)
    }

    // 去空白、丢掉空菜名的菜品——保存前清洗一遍。
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
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(confirmLabel) {
                        onSave(time.trimmingCharacters(in: .whitespacesAndNewlines), cleanedDishes)
                        dismiss()
                    }
                    .disabled(cleanedDishes.isEmpty)
                }
            }
        }
    }
}
