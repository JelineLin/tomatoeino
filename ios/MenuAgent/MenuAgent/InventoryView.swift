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
        // 压缩 + base64 是 CPU 密集（大图解码/降采样/编码），挪出主线程避免卡 UI。
        let payload = await Task.detached(priority: .userInitiated) {
            compressForUpload(imageData).map { ($0.0.base64EncodedString(), $0.1) }
        }.value
        guard let (b64, mime) = payload else {
            parseError = "图片处理失败"
            return nil
        }
        do {
            return try await api.parseOrderImage(imageBase64: b64, mime: mime)
        } catch {
            parseError = error.localizedDescription
            return nil
        }
    }

    // parseSpoken 把家长的一句话（多为语音转写）解析成条目（不入库）。
    // 和扫订单汇到同一条确认路径——不管来源是图还是话，进账之前都要过一遍家长的眼睛。
    func parseSpoken(_ text: String) async -> [InventoryItem]? {
        parsing = true
        parseError = nil
        defer { parsing = false }
        do {
            return try await api.parseInventoryText(text)
        } catch {
            parseError = error.localizedDescription
            return nil
        }
    }

    // addAll 把确认后的多条【一次性】入库。
    // 原来是逐条发请求 + 累计失败项，问题是中途失败会留下「入了一半」的账，
    // 而家长在确认页看到的是完整一批，回头对不上。改走后端 add_batch：
    // 后端先全校验再全写入，要么整批进、要么整批不进，报错也只有一条。
    func addAll(_ items: [EditableInvItem]) async {
        guard !items.isEmpty else { return }
        do {
            self.items = try await api.addInventoryBatch(items.map {
                InventoryItem(name: $0.name, quantity: $0.quantity, unit: $0.unit)
            })
            errorText = nil
        } catch {
            errorText = "入库失败：\(error.localizedDescription)"
        }
    }

    func load() async {
        errorText = nil
        do {
            items = try await api.fetchInventory()
        } catch {
            // 手上已有旧账时刷新失败就静默保留旧账（切 tab 自动刷新在弱网下会偶发失败，
            // 不该拿横幅骚扰）；只有空账本拉不到才值得报错。
            if items.isEmpty {
                errorText = error.localizedDescription
            }
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

    // ---- 行内 ± 快速调份数 ----

    // 每个食材一个待提交任务：连点几下只发最后一次请求。
    private var pendingCommits: [String: Task<Void, Never>] = [:]

    // adjust 行内加减：本地先改（立即回显），去抖 0.6s 后按【最终值】提交 set——
    // 连点 +++ 合并成一次请求，也避免两次请求读到同一基数互相覆盖的丢步。
    // 地板是 1（后端 Set 拒绝 0）：减到 1 后 − 置灰，清空整条走左滑删除，防误删。
    func adjust(name: String, delta: Double) {
        guard let i = items.firstIndex(where: { $0.name == name }) else { return }
        let it = items[i]
        let newQ = max(1, it.quantity + delta)
        guard newQ != it.quantity else { return }
        // 只改数量，新鲜度沿用原值——本地回显不该顺手把徽章抹掉（真值等下一次 GET 回来）。
        items[i] = InventoryItem(name: it.name, quantity: newQ, unit: it.unit,
                                 updatedAt: it.updatedAt, freshness: it.freshness,
                                 days: it.days, shelfLife: it.shelfLife, category: it.category)

        pendingCommits[name]?.cancel()
        pendingCommits[name] = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 600_000_000)
            guard let self, !Task.isCancelled else { return }
            guard let cur = self.items.first(where: { $0.name == name }) else { return }
            await self.set(name: cur.name, quantity: cur.quantity, unit: cur.unit)
        }
    }
}

struct InventoryView: View {
    @StateObject private var vm = InventoryViewModel()
    @State private var editing: InventoryItem?   // 非 nil = 正在编辑这条
    @State private var addingNew = false
    @State private var photoItem: PhotosPickerItem?   // 选中的订单截图
    @State private var parsedOrder: ParsedOrder?      // 解析结果（非 nil 打开确认 sheet）
    @State private var quickAdding = false            // 一句话入库 sheet
    @State private var pendingParsed: [InventoryItem]? // 一句话解析结果，等 sheet 关掉再转交确认页

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
                    // 一句话入库（主路径）：说一句「买了两块鳕鱼、一个西兰花」就完事。
                    // 排在扫订单前面是有意的——截图那条路要切 App、等解析，还在相册里
                    // 堆垃圾图；入库端的摩擦正是账本失准的源头。
                    Button {
                        quickAdding = true
                    } label: {
                        Image(systemName: "mic.badge.plus")
                    }
                }
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
                // 识别结果预览：可改可删，确认后全部入库。图片和一句话共用这一个确认页。
                ParsedOrderSheet(items: order.items) { confirmed in
                    Task { await vm.addAll(confirmed) }
                }
            }
            // 解析结果先寄存，等这个 sheet 真正关掉了再打开确认页——
            // 前一个 sheet 还在收尾时直接开下一个，iOS 会把第二个吞掉。
            .sheet(isPresented: $quickAdding, onDismiss: {
                if let pending = pendingParsed {
                    pendingParsed = nil
                    parsedOrder = ParsedOrder(items: pending.map {
                        EditableInvItem(name: $0.name, quantity: $0.quantity, unit: $0.unit)
                    })
                }
            }) {
                QuickAddSheet { text in
                    guard let parsed = await vm.parseSpoken(text), !parsed.isEmpty else { return false }
                    pendingParsed = parsed
                    return true
                }
            }
            .alert(
                "没识别成功",
                isPresented: Binding(get: { vm.parseError != nil }, set: { if !$0 { vm.parseError = nil } })
            ) {
                Button("好", role: .cancel) {}
            } message: {
                Text(vm.parseError ?? "")
            }
        }
        // 每次 tab 出现都重拉：聊天里让助手记的账（add/consume_inventory）改的是服务端，
        // 只加载一次的话切过来看到的永远是首屏旧快照。家庭规模一次 GET 很便宜。
        .task {
            await vm.load()
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
                // 订单截图和一句话共用这个遮罩，文案别写死成「订单」。
                Text("正在识别…").font(.footnote).foregroundStyle(.secondary)
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
                        HStack(spacing: 8) {
                            // 点名称打开完整编辑（改单位/精确小数），低频操作收进去。
                            Button {
                                editing = item
                            } label: {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(item.name).foregroundStyle(.primary)
                                    // 新鲜度徽章：只标「该吃了」和「可能没了」——全都标一遍等于没标。
                                    // 后端已按紧急度排序，所以要处理的东西天然聚在列表顶部。
                                    if let badge = item.freshnessBadge {
                                        Text(badge.text)
                                            .font(.caption2)
                                            .foregroundStyle(badge.isUrgent ? Color.red : Color.orange)
                                    }
                                }
                            }
                            .buttonStyle(.plain)

                            Spacer(minLength: 8)

                            // 行内 ±：高频的「用掉一份/买回一份」不进任何页面直接调。
                            // List 行里多个按钮必须 .borderless，否则点哪都触发整行。
                            Button {
                                vm.adjust(name: item.name, delta: -1)
                            } label: {
                                Image(systemName: "minus.circle.fill").font(.title2)
                            }
                            .buttonStyle(.borderless)
                            .foregroundStyle(item.quantity > 1 ? Color.orange : Color.gray.opacity(0.35))
                            .disabled(item.quantity <= 1)

                            Text("\(fmtQty(item.quantity)) \(item.unit)")
                                .monospacedDigit()
                                .frame(minWidth: 52)
                                .multilineTextAlignment(.center)

                            Button {
                                vm.adjust(name: item.name, delta: 1)
                            } label: {
                                Image(systemName: "plus.circle.fill").font(.title2)
                            }
                            .buttonStyle(.borderless)
                            .foregroundStyle(.orange)
                        }
                    }
                    .onDelete { offsets in
                        let names = offsets.map { vm.items[$0].name }
                        Task { for n in names { await vm.remove(name: n) } }
                    }
                } footer: {
                    Text("± 直接调份数；点名称改单位/精确值；左滑删除整条。改账和聊天里助手记的是同一本。")
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

// compressForUpload 把相册原图降采样 + JPEG 压缩再上传：截图通常几 MB，
// 压到最长边 1600px、质量 0.7，既够视觉模型看清文字，又不撑爆明文 HTTP 上传。
// 订单截图（本文件）和导入历史（ImportHistoryView）共用，故为 internal。
func compressForUpload(_ data: Data, maxDimension: CGFloat = 1600, quality: CGFloat = 0.7) -> (Data, String)? {
    guard let img = UIImage(data: data) else { return nil }
    let longest = max(img.size.width, img.size.height)
    let scale = longest > maxDimension ? maxDimension / longest : 1
    let size = CGSize(width: img.size.width * scale, height: img.size.height * scale)
    let format = UIGraphicsImageRendererFormat.default()
    format.opaque = true
    format.scale = 1 // 必须！否则按屏幕 @2x/@3x 渲染，实际像素是 size 的 2~3 倍，压不下来
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

// QuickAddSheet 是「一句话入库」的输入层：家长说一句（或打一句）买了什么，
// 交给后端解析成条目，再走和扫订单同一个确认页。
//
// 为什么这条路值得存在：入库原本只有截图那一条，要切到买菜 App、截图、回来上传、
// 等视觉模型，末了相册里还多一张永远不会再看的图。摩擦全堆在入库这一端，
// 家长自然就不记了——账本失准的根不在算法，在这儿。说一句话是最短的入库动作。
//
// onParse 返回 true 表示解析成功（外层已打开确认页），本 sheet 随即自行关闭。
private struct QuickAddSheet: View {
    let onParse: (String) async -> Bool

    @Environment(\.dismiss) private var dismiss
    @StateObject private var dictator = SpeechDictator()
    @State private var text = ""
    @State private var working = false
    @FocusState private var focused: Bool

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 14) {
                Text("说说买了什么，比如「买了两块鳕鱼、一个西兰花、一盒鸡蛋」")
                    .font(.footnote)
                    .foregroundStyle(.secondary)

                HStack(alignment: .bottom, spacing: 10) {
                    // 麦克风：录音中变红+实心，和聊天页同一套视觉语言。
                    Button(action: toggleDictation) {
                        Image(systemName: dictator.isRecording ? "mic.fill" : "mic")
                            .font(.body.weight(.medium))
                            .foregroundStyle(dictator.isRecording ? Color.red : Color.secondary)
                            .frame(width: 36, height: 36)
                            .background(
                                Circle().fill(dictator.isRecording
                                              ? Color.red.opacity(0.12)
                                              : Color(.secondarySystemBackground))
                            )
                    }
                    .disabled(working)
                    .accessibilityLabel(dictator.isRecording ? "停止语音输入" : "语音输入")
                    .animation(.easeInOut(duration: 0.15), value: dictator.isRecording)

                    TextField("买了…", text: $text, axis: .vertical)
                        .lineLimit(1...4)
                        .textFieldStyle(.roundedBorder)
                        .focused($focused)
                        .disabled(working)
                }

                Button(action: parse) {
                    if working {
                        HStack(spacing: 6) {
                            ProgressView().controlSize(.small)
                            Text("识别中…")
                        }
                        .frame(maxWidth: .infinity)
                    } else {
                        Text("识别").frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(working || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                Spacer()
            }
            .padding()
            .navigationTitle("一句话入库")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") {
                        dictator.stop()
                        dismiss()
                    }
                }
            }
        }
        .presentationDetents([.medium])
        // 离开时务必停掉录音：识别任务还挂着会一直占着麦克风和音频会话。
        .onDisappear { dictator.stop() }
        .alert(
            "语音输入不可用",
            isPresented: Binding(
                get: { dictator.errorText != nil },
                set: { if !$0 { dictator.errorText = nil } }
            )
        ) {
            Button("知道了", role: .cancel) { dictator.errorText = nil }
        } message: {
            Text(dictator.errorText ?? "")
        }
    }

    // 录着就停，没录就开；开录前记下已有文字当前缀，识别结果拼在后面。
    private func toggleDictation() {
        if dictator.isRecording {
            dictator.stop()
            return
        }
        focused = false
        let existing = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let prefix = existing.isEmpty ? "" : existing + " "
        dictator.start { recognized in
            text = prefix + recognized
        }
    }

    // 话音未落就点识别，得先把录音停掉——否则收尾的识别结果会盖掉正在解析的这句。
    private func parse() {
        dictator.stop()
        let payload = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !payload.isEmpty, !working else { return }
        working = true
        Task {
            let ok = await onParse(payload)
            working = false
            if ok { dismiss() }
        }
    }
}
