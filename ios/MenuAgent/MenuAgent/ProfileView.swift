// 档案界面：展示并编辑宝宝档案——年龄（生日）、过敏源、忌口、其他要点。
//
// 数据来自后端 /api/profile：GET 读、POST 写。写路径和聊天里的 update_profile 工具
// 共用后端同一个合并/校验/落盘核心，界面改和 agent 改是一本账。
// 过敏源是「硬禁忌」：编辑回写后，下一轮 chat/brief 的人设注入即生效，推荐会绝对排除。
//
// 年龄编辑用生日 DatePicker（后端只存生日，月龄按当天现算），比让用户填月龄更省事、不会过期。
import SwiftUI

@MainActor
final class ProfileViewModel: ObservableObject {
    // 可编辑态（和 Form 双向绑定）。
    @Published var babyName = ""
    @Published var hasBirthDate = false   // 是否已记录生日；没记录就不往后端传 birthDate
    @Published var birthDate = Date()
    @Published var allergies: [String] = []
    @Published var dislikes: [String] = []
    @Published var notes = ""

    @Published var loaded = false         // 首次加载完成（用于区分「加载中」和「保存中」）
    @Published var isSaving = false
    @Published var errorText: String?
    @Published var savedNote: String?     // 保存成功的短暂提示

    private let api = APIClient()

    // 生日只认 yyyy-MM-dd，用固定 POSIX 口径解析/格式化，避免地区历法干扰。
    private static let dateFmt: DateFormatter = {
        let f = DateFormatter()
        f.calendar = Calendar(identifier: .gregorian)
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    func load() async {
        errorText = nil
        do {
            apply(try await api.fetchProfile())
        } catch {
            errorText = error.localizedDescription
        }
        loaded = true
    }

    // 把后端档案灌进可编辑态。
    private func apply(_ p: Profile) {
        babyName = p.babyName ?? ""
        if let bd = p.birthDate, let d = Self.dateFmt.date(from: bd) {
            birthDate = d
            hasBirthDate = true
        } else {
            hasBirthDate = false
        }
        allergies = p.allergies ?? []
        dislikes = p.dislikes ?? []
        notes = p.notes ?? ""
    }

    func save() async {
        isSaving = true
        errorText = nil
        savedNote = nil
        // 数组始终以非 nil 传：界面清空某项时，后端才能真正清掉（nil=保持原值，[]=清空）。
        // 生日仅在「已记录」时传，否则不动后端已有值。名字/要点留空则保持原值（不支持清空，够用）。
        let payload = Profile(
            babyName: babyName.trimmingCharacters(in: .whitespacesAndNewlines),
            birthDate: hasBirthDate ? Self.dateFmt.string(from: birthDate) : nil,
            allergies: allergies,
            dislikes: dislikes,
            notes: notes.trimmingCharacters(in: .whitespacesAndNewlines)
        )
        do {
            apply(try await api.updateProfile(payload))
            savedNote = "已保存 ✓"
        } catch {
            errorText = error.localizedDescription
        }
        isSaving = false
        if savedNote != nil {
            try? await Task.sleep(nanoseconds: 2_000_000_000)
            savedNote = nil
        }
    }
}

struct ProfileView: View {
    @StateObject private var vm = ProfileViewModel()

    var body: some View {
        NavigationStack {
            Group {
                if !vm.loaded {
                    ProgressView("加载中…")
                } else {
                    form
                }
            }
            .navigationTitle("宝宝档案")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    if vm.isSaving {
                        ProgressView()
                    } else {
                        Button("保存") { Task { await vm.save() } }
                    }
                }
            }
        }
        .task { if !vm.loaded { await vm.load() } }
    }

    private var form: some View {
        Form {
            if let note = vm.savedNote {
                Section {
                    Label(note, systemImage: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                        .font(.callout)
                }
            }
            if let err = vm.errorText {
                Section {
                    Label(err, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                        .font(.callout)
                }
            }

            Section("宝宝") {
                TextField("称呼（如 宝宝 / 朵朵）", text: $vm.babyName)
                Toggle("记录生日", isOn: $vm.hasBirthDate.animation())
                if vm.hasBirthDate {
                    DatePicker("生日", selection: $vm.birthDate, in: ...Date(), displayedComponents: .date)
                    Text(ageDescription(vm.birthDate))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            Section {
                TagListEditor(addPlaceholder: "添加过敏原，如 鸡蛋", items: $vm.allergies)
            } header: {
                Text("过敏源")
            } footer: {
                Text("硬禁忌：给宝宝推荐菜单时会【绝对排除】这些食材及其制品。")
            }

            Section {
                TagListEditor(addPlaceholder: "添加忌口，如 香菜", items: $vm.dislikes)
            } header: {
                Text("忌口 / 不爱吃")
            } footer: {
                Text("软偏好：推荐时尽量避开或换个做法。")
            }

            Section("其他要点") {
                TextField("咀嚼能力、口味倾向等（可留空）", text: $vm.notes, axis: .vertical)
                    .lineLimit(1...4)
            }
        }
    }

    // 按生日算一句人话年龄，给编辑时一个直观反馈（后端存生日、月龄现算，这里只为展示）。
    private func ageDescription(_ birth: Date) -> String {
        let c = Calendar.current.dateComponents([.year, .month], from: birth, to: Date())
        let y = max(c.year ?? 0, 0)
        let m = max(c.month ?? 0, 0)
        if y == 0 && m == 0 { return "还不满 1 个月" }
        if y == 0 { return "现在 \(m) 个月大" }
        let totalMonths = y * 12 + m
        let tail = m > 0 ? " \(m) 个月" : ""
        return "现在 \(y) 岁\(tail)（共 \(totalMonths) 个月）"
    }
}

// TagListEditor 是一组可增删的标签行（过敏源、忌口共用）：已有项可左滑删除，
// 底部一行输入 + 加号新增。去重后用字符串本身当 id，避免重复项导致行错位。
private struct TagListEditor: View {
    let addPlaceholder: String
    @Binding var items: [String]
    @State private var newItem = ""

    var body: some View {
        ForEach(items, id: \.self) { item in
            Text(item)
        }
        .onDelete { items.remove(atOffsets: $0) }

        HStack {
            TextField(addPlaceholder, text: $newItem)
                .onSubmit(add)
            Button(action: add) {
                Image(systemName: "plus.circle.fill")
            }
            .disabled(newItem.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
    }

    private func add() {
        let t = newItem.trimmingCharacters(in: .whitespacesAndNewlines)
        newItem = ""
        guard !t.isEmpty, !items.contains(t) else { return }
        items.append(t)
    }
}
