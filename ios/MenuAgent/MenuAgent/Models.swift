// 数据模型：和 Go 后端的 JSON 一一对应。
//
// 后端 /api/history 返回 []Day，字段全小写（date/lunch/fruit/dinner/time/dishes/name/detail），
// 和这里的属性名一致，所以 Codable 不用写 CodingKeys。三餐可能缺席（某天没午餐），用可选类型。
import Foundation

// Day 是一天的备餐记录。用 date 作为列表稳定 id。
struct Day: Codable, Identifiable {
    let date: String
    let lunch: Meal?
    let fruit: Meal?
    let dinner: Meal?

    var id: String { date }
}

// Meal 是某一餐：几点吃 + 一组菜。
struct Meal: Codable {
    let time: String
    let dishes: [Dish]
}

// Dish 是一道菜：菜名 + 做法/分量明细。
struct Dish: Codable, Hashable {
    let name: String
    let detail: String
}

// Season 是某个月的时令清单，对应后端 /api/seasonal 的 JSON。
struct Season: Codable {
    let month: Int
    let veg: [String]
    let fruit: [String]
    let aquatic: [String]
    let tip: String
    // 数据来源说明。设为可选：旧后端不返回该字段时，整个时令 tab 不会因缺字段解码失败。
    let source: String?
}

// Profile 是宝宝档案，对应后端 /api/profile 的 JSON。字段全可空——空档案也合法。
// 字段名和后端一致（babyName/birthDate/allergies/dislikes/notes），Codable 不用 CodingKeys。
// 用 var 是为了「档案」tab 能就地编辑；提交更新时整份 POST 回后端合并落盘。
struct Profile: Codable {
    var babyName: String?
    var birthDate: String?     // 出生日期 "YYYY-MM-DD"（后端只存生日，月龄按当天现算）
    var allergies: [String]?   // 过敏原（硬禁忌，建议时绝对排除）
    var dislikes: [String]?    // 不吃/不爱吃（软偏好）
    var notes: String?         // 其他要点
}

// DailyBrief 是后端定时生成的「今日备餐简报」，对应 /api/brief 的 JSON。
// content 是 agent 写的 Markdown，直接交给 MarkdownText 渲染。
struct DailyBrief: Codable {
    let date: String        // 简报对应的日期 yyyy-MM-dd
    let content: String
    let generatedAt: Date   // 生成时刻（后端 RFC3339，需 ISO8601 解码策略）
}

// ChatMessage 是聊天界面里的一条消息。text/thinking 设计成 var，
// 因为助手回复是流式的——边收 token 边往同一条消息里追加。
struct ChatMessage: Identifiable {
    enum Role: String {
        case user
        case assistant
    }

    let id = UUID()
    let role: Role
    var text: String
    // 思考过程（模型推理 + 工具调用轨迹）。只有助手消息才有；发给后端的历史里不带它。
    var thinking: String = ""
    // 工具备忘（L1 轨迹回灌）：后端在流末尾发来的本轮工具轨迹完整记录。
    // 只展示不渲染，下一轮随历史原样带回——后端注入上下文后，追问就不必重查同样的数据。
    // 状态由客户端保管，后端保持无状态。
    var context: String = ""
}
