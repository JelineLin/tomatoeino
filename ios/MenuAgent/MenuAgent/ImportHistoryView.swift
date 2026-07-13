// 导入历史界面：把家长手上的历史（粘贴文字 或 菜单/记录截图）解析成结构化历史，
// 预览核对后一键写入。解析→预览→确认三步，防坏解析静默污染历史（同日同餐是覆盖）。
//
// 文字走后端 chat 模型，图片走视觉模型；两条路都返回 [Day] 预览，确认后走 /api/history/import。
import SwiftUI
import PhotosUI

@MainActor
final class ImportHistoryViewModel: ObservableObject {
    enum Mode: Hashable { case text, image }

    @Published var mode: Mode = .text
    @Published var pastedText = ""
    @Published var photoItem: PhotosPickerItem?

    @Published var parsing = false
    @Published var importing = false
    @Published var preview: [Day] = []          // 解析结果（非空则进预览态）
    @Published var errorText: String?
    @Published var importedSummary: String?      // 非 nil = 导入成功（弹提示后收尾）

    private let api = APIClient()

    func parseText() async {
        parsing = true
        errorText = nil
        defer { parsing = false }
        do {
            preview = try await api.parseHistoryText(text: pastedText)
        } catch {
            errorText = error.localizedDescription
        }
    }

    func parseImage(_ data: Data) async {
        parsing = true
        errorText = nil
        defer { parsing = false }
        // 压缩 + base64 挪出主线程（大图 CPU 密集）。
        let payload = await Task.detached(priority: .userInitiated) {
            compressForUpload(data).map { ($0.0.base64EncodedString(), $0.1) }
        }.value
        guard let (b64, mime) = payload else {
            errorText = "图片处理失败"
            return
        }
        do {
            preview = try await api.parseHistoryImage(imageBase64: b64, mime: mime)
        } catch {
            errorText = error.localizedDescription
        }
    }

    func confirmImport() async {
        guard !preview.isEmpty else { return }
        importing = true
        errorText = nil
        defer { importing = false }
        do {
            let res = try await api.importHistory(preview)
            var msg = "新增 \(res.added) 餐"
            if res.replaced > 0 { msg += "，覆盖 \(res.replaced) 餐" }
            importedSummary = msg
        } catch {
            errorText = error.localizedDescription
        }
    }
}

struct ImportHistoryView: View {
    @Environment(\.dismiss) private var dismiss
    @StateObject private var vm = ImportHistoryViewModel()
    let onImported: () -> Void   // 导入成功后通知父视图刷新历史

    var body: some View {
        NavigationStack {
            Group {
                if vm.preview.isEmpty {
                    inputForm
                } else {
                    previewList
                }
            }
            .navigationTitle("导入历史")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
            }
            .overlay {
                if vm.parsing || vm.importing {
                    busyOverlay(vm.importing ? "正在写入历史…" : "正在解析…")
                }
            }
            .alert(
                "没解析成功",
                isPresented: Binding(get: { vm.errorText != nil }, set: { if !$0 { vm.errorText = nil } })
            ) {
                Button("好", role: .cancel) {}
            } message: {
                Text(vm.errorText ?? "")
            }
            .alert(
                "导入成功",
                isPresented: Binding(get: { vm.importedSummary != nil }, set: { if !$0 { vm.importedSummary = nil } })
            ) {
                Button("好") { onImported(); dismiss() }
            } message: {
                Text(vm.importedSummary ?? "")
            }
            .onChange(of: vm.photoItem) { item in
                Task { await handlePhoto(item) }
            }
        }
    }

    // MARK: - 输入

    private var inputForm: some View {
        Form {
            Picker("方式", selection: $vm.mode) {
                Text("粘贴文字").tag(ImportHistoryViewModel.Mode.text)
                Text("选图片").tag(ImportHistoryViewModel.Mode.image)
            }
            .pickerStyle(.segmented)

            if vm.mode == .text {
                Section {
                    TextEditor(text: $vm.pastedText)
                        .frame(minHeight: 180)
                } header: {
                    Text("粘贴历史（微信记录 / 备忘录都行）")
                } footer: {
                    Text("带上日期和三餐，格式随意——模型会自动整理成结构化历史。")
                }
                Button {
                    Task { await vm.parseText() }
                } label: {
                    Label("解析", systemImage: "wand.and.stars")
                }
                .disabled(vm.pastedText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            } else {
                Section {
                    PhotosPicker(selection: $vm.photoItem, matching: .images) {
                        Label("选一张菜单 / 记录截图", systemImage: "photo")
                    }
                } footer: {
                    Text("选图后自动解析。截图里带日期和三餐即可。")
                }
            }
        }
    }

    private func handlePhoto(_ item: PhotosPickerItem?) async {
        guard let item else { return }
        defer { vm.photoItem = nil } // 始终重置：否则同一张读失败后再选它，onChange 不触发、卡死
        guard let data = try? await item.loadTransferable(type: Data.self) else {
            vm.errorText = "读取图片失败，换一张试试"
            return
        }
        await vm.parseImage(data)
    }

    // MARK: - 预览确认

    private var previewList: some View {
        VStack(spacing: 0) {
            List {
                Section {
                    ForEach(vm.preview) { day in
                        VStack(alignment: .leading, spacing: 6) {
                            HStack(spacing: 6) {
                                Text(day.date).font(.subheadline.bold())
                                if let wd = DateFmt.weekdayName(day.date) {
                                    Text(wd).font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            // MealRowView 不传 date/field/onFeedback → 纯只读展示。
                            MealRowView(label: "午餐", meal: day.lunch)
                            MealRowView(label: "水果", meal: day.fruit)
                            MealRowView(label: "晚餐", meal: day.dinner)
                        }
                        .padding(.vertical, 2)
                    }
                    .onDelete { vm.preview.remove(atOffsets: $0) }
                } header: {
                    Text("解析出 \(vm.preview.count) 天 · 左滑删掉不对的")
                } footer: {
                    Text("确认后写入历史；同一天同一餐会覆盖已有记录，请核对。")
                }
            }

            Button {
                Task { await vm.confirmImport() }
            } label: {
                Text("确认导入 \(vm.preview.count) 天")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .padding()
            .disabled(vm.preview.isEmpty)
        }
    }

    private func busyOverlay(_ text: String) -> some View {
        ZStack {
            Color.black.opacity(0.15).ignoresSafeArea()
            VStack(spacing: 12) {
                ProgressView()
                Text(text).font(.footnote).foregroundStyle(.secondary)
            }
            .padding(24)
            .background(.regularMaterial)
            .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
        }
    }
}
