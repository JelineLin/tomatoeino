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

// Meal 是某一餐：几点吃 + 一组菜 + 旧数据遗留的餐级反馈（只读展示，新反馈在菜上）。
struct Meal: Codable {
    let time: String
    let dishes: [Dish]
    let feedback: Feedback?   // 旧粒度（整餐一条），保留展示；新反馈记在 Dish.feedback
}

// Feedback 是家长记的食用反馈。现在挂在 Dish 上（菜级粒度）；Meal 上的是旧数据。
struct Feedback: Codable, Hashable {
    let rating: String   // like（爱吃）/ dislike（不爱吃）/ ok（一般）
    let note: String?
}

// Dish 是一道菜：菜名 + 做法/分量明细 + 可选的菜级食用反馈。
struct Dish: Codable, Hashable {
    let name: String
    let detail: String
    let feedback: Feedback?   // 旧后端/没反馈时缺字段，正常解码
    // 这道菜吃掉的库存（推荐时由后端 propose_menu 登记，采纳时照它自动出库）。
    // 历史里的旧菜没有这个字段 → nil，正常解码。
    let uses: [IngredientUse]?
}

// IngredientUse 是「这道菜用掉哪样库存、用掉多少」。不带单位——单位以账本为准。
struct IngredientUse: Codable, Hashable {
    let name: String
    let qty: Double
}

// ConsumedItem 是采纳一餐后自动出库的一条结果，用来提示家长「刚扣了什么」。
// 出库是家长没点过的副作用，不明说等于偷改账本。
struct ConsumedItem: Codable, Hashable {
    let name: String
    let qty: Double
    let unit: String
    let remaining: Double
    let depleted: Bool
}

// ReplaceDishResult 是 /api/brief/dish 的响应：新菜 + 换完的整份菜单 + agent 的一句说明。
// 直接用 menu 替换本地状态，不用自己拼——服务端已经把简报缓存改好了，两边保持一致。
struct ReplaceDishResult: Codable {
    let dish: EditDish
    let menu: RecommendedMenu?
    let note: String
}

// ApplyResult 是 /api/history/apply 的响应：新历史 + 这次自动扣了什么。
struct ApplyResult: Codable {
    let history: [Day]
    let consumed: [ConsumedItem]?
    let missed: [String]?
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

// InventoryItem 是家庭库存的一条，对应后端 /api/inventory 的 JSON。用 name 作稳定 id。
//
// 后端在 GET/写后响应里【一并下发新鲜度】（派生字段，服务端现算不落盘），
// 且已按「越该吃越靠前」排好序——前端照序渲染就是家长最该先处理的顺序。
// 新鲜度相关字段全部可选：旧后端不发时整个库存页不该因缺字段解码失败。
struct InventoryItem: Codable, Identifiable {
    let name: String
    let quantity: Double
    let unit: String
    let updatedAt: String?   // 最近一次补货时刻（RFC3339）
    let freshness: String?   // fresh / use_soon / stale / unknown
    let days: Int?           // 放了几天；-1 或 nil = 不详
    let shelfLife: Int?      // 这个品类的保鲜期（天）
    let category: String?    // 判定用的品类，空串=没匹配到

    var id: String { name }

    // 行内 ± 快速调份数会本地先构造一条回显，那条只关心数量，新鲜度沿用原值。
    init(name: String, quantity: Double, unit: String,
         updatedAt: String? = nil, freshness: String? = nil,
         days: Int? = nil, shelfLife: Int? = nil, category: String? = nil) {
        self.name = name
        self.quantity = quantity
        self.unit = unit
        self.updatedAt = updatedAt
        self.freshness = freshness
        self.days = days
        self.shelfLife = shelfLife
        self.category = category
    }

    // 新鲜度徽章：只有「该吃了」和「可能没了」值得占用视觉，新鲜和不详都不显示——
    // 全都标一遍等于没标。
    var freshnessBadge: (text: String, isUrgent: Bool)? {
        switch freshness {
        case "use_soon":
            return (days.map { "该吃了 · \($0)天" } ?? "该吃了", false)
        case "stale":
            return (days.map { "可能没了 · \($0)天" } ?? "可能没了", true)
        default:
            return nil
        }
    }
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
    // 偏好规律（后端依据菜级反馈自动归纳，随 GET/POST 响应下发）。
    // 只读派生数据：展示用，编辑保存时保持 nil 不回传（后端也不收它）。
    var rules: [PrefRule]? = nil
}

// PrefRule 是后端归纳的一条口味规律：某道菜的反馈计数 + 一句人话建议。
struct PrefRule: Codable, Identifiable {
    let name: String
    let likes: Int
    let dislikes: Int
    let oks: Int
    let advice: String

    var id: String { name }
}

// DailyBrief 是后端定时生成的「今日备餐简报」，对应 /api/brief 的 JSON。
// content 是 agent 写的 Markdown，直接交给 MarkdownText 渲染。
struct DailyBrief: Codable {
    let date: String        // 简报对应的日期 yyyy-MM-dd
    let content: String
    // menu 是 var：采纳成功后要把「家长编辑过的版本」写回卡片（后端简报缓存也同步回写了，
    // 两边一致）；不然卡片一直显示原推荐，编辑像是没保存。
    var menu: RecommendedMenu?  // agent 经 propose_menu 登记的结构化推荐（可能 nil，只显示文字）
    let generatedAt: Date   // 生成时刻（后端 RFC3339，需 ISO8601 解码策略）
}

// RecommendedMenu 是 agent 登记的结构化推荐，随简报下发，供「今日推荐」页逐项编辑 + 一键采纳入库。
struct RecommendedMenu: Codable {
    let date: String
    var meals: [ProposedMeal]
}

// ProposedMeal 是推荐里的一餐。meal（lunch/fruit/dinner）不编辑、当稳定 id；dishes 可编辑。
struct ProposedMeal: Codable, Identifiable {
    var meal: String
    var time: String
    var dishes: [EditDish]
    // 备选菜（后端约定每餐 2 道）：家长不满意主推时直接顶替，零请求零延迟。
    // 换菜就是把 dishes 里的某道和 alternatives 里的某道对调——所以它是 var。
    // 声明成可选而不是给默认值：Swift 合成的 Codable 不吃默认值，旧后端不发这个字段时
    // 非可选属性会让【整份简报】解码失败。用 optional + ?? [] 是本文件一贯的兼容写法。
    var alternatives: [EditDish]?
    var reason: String
    // 后端回写的「已采纳」标记（采纳成功后简报缓存里就带上）——重进 App 拉简报，
    // 已采纳的餐直接恢复徽章和编辑后的菜品，不再回到原推荐。旧后端缺字段 → nil。
    var applied: Bool? = nil

    var id: String { meal }
}

// EditDish 是可编辑的一道菜。id 用 UUID 供 ForEach 稳定标识（编辑菜名时不错行），
// 不参与编解码——后端 JSON 只有 name/detail。
struct EditDish: Codable, Identifiable {
    let id = UUID()
    var name: String
    var detail: String
    // 这道菜吃掉的库存。采纳时原样回传给后端 → 自动出库。
    // 换备选菜时整个 EditDish 被替换，uses 自然跟着换，不用单独同步。
    var uses: [IngredientUse]? = nil

    enum CodingKeys: String, CodingKey { case name, detail, uses }
}

// ImportResult 是导入历史的结果，对应后端 /api/history/import 的 JSON。
struct ImportResult: Codable {
    let added: Int
    let replaced: Int
    let history: [Day]
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
