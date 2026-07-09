// 库存界面：展示并增删改家庭库存（食材 + 份数 + 单位）。
//
// 数据来自后端 /api/inventory：GET 读、POST 写（op=set/add/remove）。写路径和聊天里的
// add/consume_inventory 工具共用同一本账（同一个 store、同一把锁），界面改和 agent 改不打架。
// 订单截图解析入库留到后续（视觉模型）——这一版只做手动增删改。
import SwiftUI

@MainActor
final class InventoryViewModel: ObservableObject {
    @Published var items: [InventoryItem] = []
    @Published var loaded = false
    @Published var errorText: String?

    private let api = APIClient()

    func load() async {
        errorText = nil
        do {
            items = try await api.fetchInventory()
        } catch {
            errorText = error.localizedDescription
        }
        loaded = true
    }

    func add(name: String, quantity: Double, unit: String) async {
        await apply { try await self.api.addInventory(name: name, quantity: quantity, unit: unit) }
    }

    func set(name: String, quantity: Double, unit: String) async {
        await apply { try await self.api.setInventory(name: name, quantity: quantity, unit: unit) }
    }

    func remove(name: String) async {
        await apply { try await self.api.removeInventory(name: name) }
    }

    // 所有写操作走同一条：成功用后端返回的整份账本替换，失败记错误、不动本地列表。
    private func apply(_ op: () async throws -> [InventoryItem]) async {
        errorText = nil
        do {
            items = try await op()
        } catch {
            errorText = error.localizedDescription
        }
    }
}

struct InventoryView: View {
    @StateObject private var vm = InventoryViewModel()
    @State private var editing: InventoryItem?   // 非 nil = 正在编辑这条
    @State private var addingNew = false

    var body: some View {
        NavigationStack {
            Group {
                if !vm.loaded {
                    ProgressView("加载中…")
                } else {
                    listContent
                }
            }
            .navigationTitle("家庭库存")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        addingNew = true
                    } label: {
                        Image(systemName: "plus")
                    }
                }
            }
            .sheet(item: $editing) { item in
                // 编辑现有：名字固定（name 是主键，改名请删了重加），只改份数/单位。
                InventoryEditorSheet(item: item) { _, qty, unit in
                    Task { await vm.set(name: item.name, quantity: qty, unit: unit) }
                }
            }
            .sheet(isPresented: $addingNew) {
                // 新增：走 add（累加语义——若已存在同名会加上去）。
                InventoryEditorSheet(item: nil) { name, qty, unit in
                    Task { await vm.add(name: name, quantity: qty, unit: unit) }
                }
            }
        }
        .task {
            if !vm.loaded { await vm.load() }
        }
    }

    @ViewBuilder
    private var listContent: some View {
        if vm.items.isEmpty && vm.errorText == nil {
            VStack(spacing: 12) {
                Image(systemName: "shippingbox")
                    .font(.largeTitle)
                    .foregroundStyle(.secondary)
                Text("库存是空的").font(.headline)
                Text("点右上角 ＋ 添加，或在聊天里让助手记账")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding()
        } else {
            List {
                if let err = vm.errorText {
                    Section {
                        Label(err, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                            .font(.callout)
                    }
                }
                Section {
                    ForEach(vm.items) { item in
                        Button {
                            editing = item
                        } label: {
                            HStack {
                                Text(item.name)
                                    .foregroundStyle(.primary)
                                Spacer()
                                Text("\(fmtQty(item.quantity)) \(item.unit)")
                                    .foregroundStyle(.secondary)
                                    .monospacedDigit()
                            }
                        }
                    }
                    .onDelete { offsets in
                        let names = offsets.map { vm.items[$0].name }
                        Task { for n in names { await vm.remove(name: n) } }
                    }
                } footer: {
                    Text("左滑删除，点条目改份数；改账和聊天里助手记的是同一本。")
                }
            }
            .refreshable { await vm.load() }
        }
    }
}

// 份数渲染：整数不带小数点（2），小数保留（0.5）。和后端 fmtQty 一个口径。
private func fmtQty(_ q: Double) -> String {
    q == q.rounded() ? String(Int(q)) : String(q)
}

// 库存编辑 sheet：新增（item==nil，名字可填）或编辑（名字固定，改份数/单位）。
private struct InventoryEditorSheet: View {
    let item: InventoryItem?
    let onSave: (_ name: String, _ quantity: Double, _ unit: String) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var name: String
    @State private var quantity: Double
    @State private var unit: String

    init(item: InventoryItem?, onSave: @escaping (String, Double, String) -> Void) {
        self.item = item
        self.onSave = onSave
        _name = State(initialValue: item?.name ?? "")
        _quantity = State(initialValue: item?.quantity ?? 1)
        _unit = State(initialValue: item?.unit ?? "份")
    }

    private var trimmedName: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var canSave: Bool { !trimmedName.isEmpty && quantity > 0 }

    var body: some View {
        NavigationStack {
            Form {
                Section("食材") {
                    if item == nil {
                        TextField("名称，如 鳕鱼", text: $name)
                    } else {
                        LabeledContent("名称", value: item!.name)
                    }
                }
                Section("数量") {
                    Stepper(value: $quantity, in: 0.5...999, step: 0.5) {
                        Text("\(fmtQty(quantity)) \(unit)")
                            .monospacedDigit()
                    }
                    TextField("单位，如 份 / 块 / 个 / 袋", text: $unit)
                }
            }
            .navigationTitle(item == nil ? "新增库存" : "编辑库存")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("保存") {
                        let u = unit.trimmingCharacters(in: .whitespacesAndNewlines)
                        onSave(trimmedName, quantity, u.isEmpty ? "份" : u)
                        dismiss()
                    }
                    .disabled(!canSave)
                }
            }
        }
        .presentationDetents([.medium])
    }
}
