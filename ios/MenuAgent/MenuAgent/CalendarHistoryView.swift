// 日历视图：把吃饭历史按月历表格展示。
//
// 布局分两层，像对账单的「汇总 + 明细」：
//   上半是月历网格（7 列，周一开头），有记录的日子加圆点、可点；
//   下半是选中那天的三餐明细表，行是 午餐/水果/晚餐。
// 数据仍然来自 HistoryViewModel 拉回来的 []Day，这里只做重新排版，不发新请求。
import SwiftUI

// 一个「年-月」。日历翻页的最小单位，用它做 Picker/翻页的稳定 id。
private struct YearMonth: Hashable, Comparable {
    let year: Int
    let month: Int

    static func < (lhs: YearMonth, rhs: YearMonth) -> Bool {
        (lhs.year, lhs.month) < (rhs.year, rhs.month)
    }

    var title: String { String(format: "%d年%d月", year, month) }
}

struct CalendarHistoryView: View {
    let days: [Day]

    // 当前展示的月份；nil 表示还没初始化（首次出现时跳到最近有记录的月份）。
    @State private var current: YearMonth?
    @State private var selectedDate: String?

    // date 字符串是后端定死的 "yyyy-MM-dd"，直接按字典序就是时间序，
    // 所以这里可以用字符串前缀/排序做月份归组，不必先转 Date。
    private var dayByDate: [String: Day] {
        Dictionary(uniqueKeysWithValues: days.map { ($0.date, $0) })
    }

    // 有记录的所有月份，升序。翻页范围限制在这个区间内，避免翻到空白年份。
    private var months: [YearMonth] {
        let set = Set(days.compactMap { day -> YearMonth? in
            let parts = day.date.split(separator: "-")
            guard parts.count == 3, let y = Int(parts[0]), let m = Int(parts[1]) else { return nil }
            return YearMonth(year: y, month: m)
        })
        return set.sorted()
    }

    var body: some View {
        let months = self.months
        ScrollView {
            VStack(spacing: 16) {
                if let cur = current ?? months.last {
                    header(cur, months: months)
                    monthGrid(cur)
                    Divider()
                    detail
                } else {
                    Text("暂无记录")
                        .foregroundStyle(.secondary)
                        .padding(.top, 40)
                }
            }
            .padding()
        }
        .onAppear {
            // 默认停在最近有记录的月份，并选中最近的一天——打开就能看到「最近吃了啥」。
            if current == nil {
                current = months.last
                selectedDate = days.map(\.date).max()
            }
        }
    }

    // 月份标题 + 左右翻页箭头。翻到头就把箭头置灰。
    private func header(_ cur: YearMonth, months: [YearMonth]) -> some View {
        HStack {
            Button {
                if let prev = months.last(where: { $0 < cur }) { current = prev }
            } label: {
                Image(systemName: "chevron.left")
            }
            .disabled(!months.contains { $0 < cur })

            Spacer()
            Text(cur.title).font(.headline)
            Spacer()

            Button {
                if let next = months.first(where: { cur < $0 }) { current = next }
            } label: {
                Image(systemName: "chevron.right")
            }
            .disabled(!months.contains { cur < $0 })
        }
        .padding(.horizontal, 4)
    }

    // 月历网格本体。周一开头（贴合国内习惯），前面用空格子补齐首日偏移。
    private func monthGrid(_ cur: YearMonth) -> some View {
        let columns = Array(repeating: GridItem(.flexible()), count: 7)
        return LazyVGrid(columns: columns, spacing: 8) {
            ForEach(["一", "二", "三", "四", "五", "六", "日"], id: \.self) { w in
                Text(w).font(.caption).foregroundStyle(.secondary)
            }
            ForEach(0..<leadingBlanks(cur), id: \.self) { _ in
                Color.clear.frame(height: 40)
            }
            ForEach(1...dayCount(cur), id: \.self) { dayNum in
                dayCell(cur, dayNum: dayNum)
            }
        }
    }

    @ViewBuilder
    private func dayCell(_ cur: YearMonth, dayNum: Int) -> some View {
        let dateStr = String(format: "%04d-%02d-%02d", cur.year, cur.month, dayNum)
        let hasRecord = dayByDate[dateStr] != nil
        let isSelected = selectedDate == dateStr

        Button {
            withAnimation(.snappy) { selectedDate = dateStr }
        } label: {
            VStack(spacing: 4) {
                // 选中 = 橙色实心圆 + 白字；今天 = 橙色描边圈；平日裸数字。
                ZStack {
                    if isSelected {
                        Circle().fill(Color.orange).frame(width: 32, height: 32)
                    } else if dateStr == DateFmt.todayString {
                        Circle()
                            .stroke(Color.orange.opacity(0.55), lineWidth: 1.5)
                            .frame(width: 32, height: 32)
                    }
                    Text("\(dayNum)")
                        .font(.callout)
                        .fontWeight(isSelected ? .bold : .regular)
                        .foregroundStyle(
                            isSelected ? .white
                                : hasRecord ? Color.primary : Color.secondary.opacity(0.45)
                        )
                }
                // 圆点标记「这天有记录」；没记录的日子点也留白，保持格子等高。
                Circle()
                    .fill(hasRecord ? Color.orange : .clear)
                    .frame(width: 5, height: 5)
            }
            .frame(maxWidth: .infinity, minHeight: 44)
        }
        .buttonStyle(.plain)
        .disabled(!hasRecord)
    }

    // 选中日期的三餐明细。没选中或那天没记录时给一句提示，不留空白。
    @ViewBuilder
    private var detail: some View {
        if let dateStr = selectedDate, let day = dayByDate[dateStr] {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 8) {
                    Text(dateStr).font(.headline)
                    if let wd = DateFmt.weekdayName(dateStr) {
                        Text(wd).font(.subheadline).foregroundStyle(.secondary)
                    }
                }
                VStack(alignment: .leading, spacing: 12) {
                    MealRowView(label: "午餐", meal: day.lunch)
                    MealRowView(label: "水果", meal: day.fruit)
                    MealRowView(label: "晚餐", meal: day.dinner)
                }
                .padding()
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color(.secondarySystemBackground))
                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        } else {
            Text("点选日历上带圆点的日子，查看当天吃了啥")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }

    // ---- 日期计算：只在渲染网格时用 Calendar，数据归组仍然走字符串 ----

    private func firstOfMonth(_ ym: YearMonth) -> Date {
        Calendar.current.date(from: DateComponents(year: ym.year, month: ym.month, day: 1)) ?? .now
    }

    private func dayCount(_ ym: YearMonth) -> Int {
        Calendar.current.range(of: .day, in: .month, for: firstOfMonth(ym))?.count ?? 30
    }

    // 1 号前面要空几个格子。Calendar 的 weekday 是 1=周日…7=周六，换算成周一开头的偏移。
    private func leadingBlanks(_ ym: YearMonth) -> Int {
        let weekday = Calendar.current.component(.weekday, from: firstOfMonth(ym))
        return (weekday + 5) % 7
    }
}

// 一餐的渲染：图标 + 标签 + 时间徽章 + 菜品清单。列表和日历两个视图共用，保证两边样式一致。
struct MealRowView: View {
    let label: String
    let meal: Meal?

    // 每餐一个专属图标和颜色：午餐日头橙、水果叶子绿、晚餐月亮蓝紫——扫一眼就分清。
    private var style: (icon: String, color: Color) {
        switch label {
        case "午餐": return ("sun.max.fill", .orange)
        case "水果": return ("leaf.fill", .green)
        case "晚餐": return ("moon.stars.fill", .indigo)
        default: return ("fork.knife", .gray)
        }
    }

    var body: some View {
        if let meal, !meal.dishes.isEmpty {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: style.icon)
                    .font(.footnote)
                    .foregroundStyle(style.color)
                    .frame(width: 28, height: 28)
                    .background(Circle().fill(style.color.opacity(0.12)))

                VStack(alignment: .leading, spacing: 5) {
                    HStack(spacing: 6) {
                        Text(label).font(.subheadline.bold())
                        if !meal.time.isEmpty {
                            Text(meal.time)
                                .font(.caption2)
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 2)
                                .background(Capsule().fill(Color.primary.opacity(0.06)))
                        }
                    }
                    ForEach(meal.dishes, id: \.self) { dish in
                        VStack(alignment: .leading, spacing: 1) {
                            Text(dish.name).font(.callout)
                            if !dish.detail.isEmpty {
                                Text(dish.detail)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .padding(.vertical, 2)
        }
    }
}

// DateFmt 集中放「yyyy-MM-dd 字符串 ↔ 日期」的小工具。
// DateFormatter 创建很贵，做成静态常量全 app 复用。
enum DateFmt {
    static let ymd: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    static var todayString: String { ymd.string(from: .now) }

    // "2025-11-03" → "周一"
    static func weekdayName(_ s: String) -> String? {
        guard let d = ymd.date(from: s) else { return nil }
        let names = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"]
        return names[Calendar.current.component(.weekday, from: d) - 1]
    }
}
