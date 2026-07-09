// 库存界面：展示并增删改家庭库存（食材 + 份数 + 单位）。
//
// 数据来自后端 /api/inventory：GET 读、POST 写（op=set/add/remove）。写路径和聊天里的
// add/consume_inventory 工具共用同一本账（同一个 store、同一把锁），界面改和 agent 改不打架。
// 还支持「扫订单」：上传订单截图 → 视觉模型解析 → 预览确认后入库。
import SwiftUI
import PhotosUI
import UIKit

@MainActor
final class InventoryViewModel: ObservableObject {
    @Published var items: [InventoryItem] = []
    @Published var loaded = false
    @Published var errorText: String?

    // 扫订单相关：
    @Published var parsing = false        // 正在上传+解析截图
    @Published var parseError: String?    // 解析失败（弹 alert）

    private let api = APIClient()

    // parseOrder 压缩图片 → 上传解析 → 返回识别到的条目（不入库）。失败记 parseError。
    func parseOrder(imageData: Data) async -> [InventoryItem]? {
        parsing = true
        parseError = nil
        defer { parsing = false }
        guard let (jpeg, mime) = compressForUpload(imageData) else {
            parseError = "图片处理失败"
            return nil
        }
        do {
            return try await api.parseOrderImage(imageBase64: jpeg.base64EncodedString(), mime: mime)
        } catch {
            parseError = error.localizedDescription
            return nil
        }
    }

    // addAll 把确认后的多条依次入库（每条走 add，累加语义）。
    func addAll(_ items: [EditableInvItem]) async {
        for it in items {
            await add(name: it.name, quantity: it.quantity, unit: it.unit)
        }
    }

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
    @State private var photoItem: PhotosPickerItem?   // 选中的订单截图
    @State private var parsedOrder: ParsedOrder?      // 解析结果（非 nil 打开确认 sheet）

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
                ToolbarItem(placement: .topBarLeading) {
                    // 扫订单：选一张订单截图，视觉模型解析成条目。
                    PhotosPicker(selection: $photoItem, matching: .images) {
                        Image(systemName: "doc.text.viewfinder")
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        addingNew = true
                    } label: {
                        Image(systemName: "plus")
                    }
                }
            }
            .overlay {
                if vm.parsing {
                    parsingOverlay
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
            .sheet(item: $parsedOrder) { order in
                // 订单识别结果预览：可改可删，确认后全部入库。
                ParsedOrderSheet(items: order.items) { confirmed in
                    Task { await vm.addAll(confirmed) }
                }
            }
            .alert(
                "订单没识别成功",
                isPresented: Binding(get: { vm.parseError != nil }, set: { if !$0 { vm.parseError = nil } })
            ) {
                Button("好", role: .cancel) {}
            } message: {
                Text(vm.parseError ?? "")
            }
        }
        .task {
            if !vm.loaded { await vm.load() }
        }
        .onChange(of: photoItem) { newItem in
            Task { await handlePickedPhoto(newItem) }
        }
    }

    // 选中截图后：读原图 Data → 解析 → 打开预览确认 sheet。
    private func handlePickedPhoto(_ item: PhotosPickerItem?) async {
        guard let item else { return }
        defer { photoItem = nil }  // 重置，允许再次选同一张
        guard let data = try? await item.loadTransferable(type: Data.self) else {
            vm.parseError = "读取图片失败"
            return
        }
        if let parsed = await vm.parseOrder(imageData: data), !parsed.isEmpty {
            parsedOrder = ParsedOrder(items: parsed.map {
                EditableInvItem(name: $0.name, quantity: $0.quantity, unit: $0.unit)
            })
        } else if vm.parseError == nil {
            vm.parseError = "没识别到食材，换张清晰点的订单截图试试"
        }
    }

    private var parsingOverlay: some View {
        ZStack {
            Color.black.opacity(0.15).ignoresSafeArea()
            VStack(spacing: 12) {
                ProgressView()
                Text("正在识别订单…").font(.footnote).foregroundStyle(.secondary)
            }
            .padding(24)
            .background(.regularMaterial)
            .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
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

// EditableInvItem 是订单识别结果里一条可编辑的条目。
struct EditableInvItem: Identifiable {
    let id = UUID()
    var name: String
    var quantity: Double
    var unit: String
}

// ParsedOrder 包一层给 sheet(item:) 用（数组本身不是 Identifiable）。
struct ParsedOrder: Identifiable {
    let id = UUID()
    var items: [EditableInvItem]
}

// compressForUpload 把相册原图降采样 + JPEG 压缩再上传：订单截图通常几 MB，
// 压到最长边 1600px、质量 0.7，既够视觉模型看清文字，又不撑爆明文 HTTP 上传。
private func compressForUpload(_ data: Data, maxDimension: CGFloat = 1600, quality: CGFloat = 0.7) -> (Data, String)? {
    guard let img = UIImage(data: data) else { return nil }
    let longest = max(img.size.width, img.size.height)
    let scale = longest > maxDimension ? maxDimension / longest : 1
    let size = CGSize(width: img.size.width * scale, height: img.size.height * scale)
    let format = UIGraphicsImageRendererFormat.default()
    format.opaque = true
    let resized = UIGraphicsImageRenderer(size: size, format: format).image { _ in
        img.draw(in: CGRect(origin: .zero, size: size))
    }
    guard let jpeg = resized.jpegData(compressionQuality: quality) else { return nil }
    return (jpeg, "image/jpeg")
}

// ParsedOrderSheet：订单识别结果预览——每条可改名字/份数/单位、可左滑删，确认后全部入库。
private struct ParsedOrderSheet: View {
    let onConfirm: (_ items: [EditableInvItem]) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var items: [EditableInvItem]

    init(items: [EditableInvItem], onConfirm: @escaping ([EditableInvItem]) -> Void) {
        _items = State(initialValue: items)
        self.onConfirm = onConfirm
    }

    private var cleaned: [EditableInvItem] {
        items
            .map {
                EditableInvItem(
                    name: $0.name.trimmingCharacters(in: .whitespacesAndNewlines),
                    quantity: $0.quantity,
                    unit: $0.unit.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "份" : $0.unit
                )
            }
            .filter { !$0.name.isEmpty }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    ForEach($items) { $item in
                        VStack(alignment: .leading, spacing: 6) {
                            TextField("名称", text: $item.name)
                            HStack {
                                Stepper(value: $item.quantity, in: 0.5...999, step: 0.5) {
                                    Text("\(fmtQty(item.quantity)) \(item.unit)")
                                        .monospacedDigit()
                                }
                                TextField("单位", text: $item.unit)
                                    .frame(width: 64)
                                    .multilineTextAlignment(.trailing)
                            }
                        }
                    }
                    .onDelete { items.remove(atOffsets: $0) }
                } header: {
                    Text("识别到 \(items.count) 项 · 可改可删")
                } footer: {
                    Text("识别可能有误，务必核对份数/单位再入库。")
                }
            }
            .navigationTitle("订单识别结果")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("全部入库") {
                        onConfirm(cleaned)
                        dismiss()
                    }
                    .disabled(cleaned.isEmpty)
                }
            }
        }
    }
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
