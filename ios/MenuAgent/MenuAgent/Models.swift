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
}
