// 时令界面：查某个月的应季食材（蔬菜/水果/水产）+ 备餐提示。
//
// 数据来自后端 /api/seasonal，和 agent 的 seasonal_produce 工具同一张表——
// 聊天里 agent 说的应季，和这个 tab 展示的应季，永远一个口径。
// 默认展示「当前月」（当前几月由后端说了算，客户端不自己看表），
// 左右箭头可跨月翻看（12 月往右回到 1 月，环形）。
import SwiftUI

@MainActor
final class SeasonalViewModel: ObservableObject {
    @Published var season: Season?
    @Published var isLoading = false
    @Published var errorText: String?

    private let api = APIClient()

    // month 为 nil = 让后端按当前月返回；翻页后变成明确的月份。
    func load(month: Int? = nil) async {
        isLoading = true
        errorText = nil
        do {
            season = try await api.fetchSeasonal(month: month)
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}

struct SeasonalView: View {
    @StateObject private var vm = SeasonalViewModel()

    var body: some View {
        NavigationStack {
            Group {
                if vm.isLoading && vm.season == nil {
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
                } else if let season = vm.season {
                    content(season)
                }
            }
            .navigationTitle("时令")
        }
        .task {
            if vm.season == nil { await vm.load() }
        }
    }

    private func content(_ season: Season) -> some View {
        ScrollView {
            VStack(spacing: 16) {
                heroHeader(season)
                categoryCard(title: "应季蔬菜", icon: "🥬", items: season.veg, color: .green)
                categoryCard(title: "应季水果", icon: "🍎", items: season.fruit, color: .pink)
                categoryCard(title: "应季水产", icon: "🐟", items: season.aquatic, color: .blue)
                tipCard(season.tip)
            }
            .padding()
        }
        .refreshable { await vm.load(month: vm.season?.month) }
    }

    // 四季各配一套主题：颜色渐变 + emoji + 季名。月份一换，整个头卡的气氛跟着换。
    private func theme(_ month: Int) -> (emoji: String, name: String, colors: [Color]) {
        switch month {
        case 3...5:
            return ("🌸", "春", [Color(red: 0.35, green: 0.7, blue: 0.4), Color(red: 0.55, green: 0.82, blue: 0.45)])
        case 6...8:
            return ("☀️", "夏", [Color(red: 0.1, green: 0.6, blue: 0.75), Color(red: 0.25, green: 0.75, blue: 0.7)])
        case 9...11:
            return ("🍂", "秋", [Color(red: 0.85, green: 0.5, blue: 0.15), Color(red: 0.92, green: 0.65, blue: 0.25)])
        default:
            return ("❄️", "冬", [Color(red: 0.3, green: 0.45, blue: 0.75), Color(red: 0.45, green: 0.6, blue: 0.85)])
        }
    }

    // 头卡：季节渐变底 + 大号月份 + 环形翻页（12 月再往右是 1 月，全年随便翻）。
    private func heroHeader(_ season: Season) -> some View {
        let t = theme(season.month)
        return HStack {
            pageButton("chevron.left") {
                Task { await vm.load(month: season.month == 1 ? 12 : season.month - 1) }
            }
            Spacer()
            VStack(spacing: 2) {
                Text("\(t.emoji) \(season.month)月")
                    .font(.largeTitle.bold())
                Text("\(t.name)季时令")
                    .font(.subheadline)
                    .opacity(0.85)
            }
            Spacer()
            pageButton("chevron.right") {
                Task { await vm.load(month: season.month == 12 ? 1 : season.month + 1) }
            }
        }
        .foregroundStyle(.white)
        .padding(.horizontal, 16)
        .padding(.vertical, 22)
        .background(
            LinearGradient(colors: t.colors, startPoint: .topLeading, endPoint: .bottomTrailing)
        )
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .animation(.easeInOut(duration: 0.25), value: season.month)
    }

    private func pageButton(_ icon: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon)
                .font(.body.weight(.semibold))
                .frame(width: 34, height: 34)
                .background(Circle().fill(.white.opacity(0.22)))
        }
        .buttonStyle(.plain)
    }

    // 一类食材一张卡：标题行 + 胶囊标签流（自适应换行），每类一个专属色。
    private func categoryCard(title: String, icon: String, items: [String], color: Color) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                Text(icon)
                Text(title).font(.headline)
                Spacer()
                Text("\(items.count) 种")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 76), spacing: 8)], spacing: 8) {
                ForEach(items, id: \.self) { item in
                    Text(item)
                        .font(.callout)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .frame(maxWidth: .infinity)
                        .background(color.opacity(0.12))
                        .clipShape(Capsule())
                }
            }
        }
        .padding()
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemBackground))
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    private func tipCard(_ tip: String) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Text("💡")
            Text(tip)
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .padding()
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.yellow.opacity(0.12))
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}
