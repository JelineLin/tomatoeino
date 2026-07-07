// MarkdownText：把助手回复按 Markdown 富文本渲染。
//
// 思路是「块级自己拆，行内交给系统」：
//   - 块级结构（标题、列表、代码块、表格、段落）用手写的行扫描器拆开——
//     像解析对账文件一样逐行断块，规则简单、行为可预期；
//   - 行内样式（**粗体**、*斜体*、`代码`、[链接](url)）交给
//     AttributedString(markdown:)，这是 Foundation 自带的，不用引第三方库。
// 流式场景下每来一个 token 就整体重渲一次；备餐回复就几百字，性能无压力。
import SwiftUI

// 一个块级元素。先拆成这个中间表示，再统一交给视图层渲染，
// 解析和展示分离——想换渲染样式时不用动解析逻辑。
private enum MarkdownBlock: Identifiable {
    case heading(level: Int, text: String)
    case paragraph(String)
    case bullet(String)          // 无序列表的一项
    case ordered(index: String, text: String)  // 有序列表的一项，index 保留原文（"1."）
    case code(String)            // ``` 围栏代码块，原样等宽展示
    case table(rows: [[String]]) // 管道表格，rows[0] 是表头

    // 块没有天然 id，用内容哈希 + 枚举位置兜底即可；列表是一次性重建的，不求 diff 稳定。
    var id: String {
        switch self {
        case .heading(let l, let t): return "h\(l):\(t)"
        case .paragraph(let t): return "p:\(t)"
        case .bullet(let t): return "b:\(t)"
        case .ordered(let i, let t): return "o:\(i):\(t)"
        case .code(let t): return "c:\(t)"
        case .table(let rows): return "t:\(rows.map { $0.joined(separator: "|") }.joined(separator: ";"))"
        }
    }
}

// 逐行扫描，把整段 markdown 拆成块。规则覆盖 LLM 回复里最常见的写法，
// 认不出的行一律当普通段落，保证永远有东西可显示（宁可平淡，不可吞字）。
private func parseBlocks(_ text: String) -> [MarkdownBlock] {
    var blocks: [MarkdownBlock] = []
    var paragraph: [String] = []   // 正在积累的段落行
    var codeLines: [String]?      // 非 nil 表示正在代码块里
    var tableRows: [[String]] = []

    func flushParagraph() {
        if !paragraph.isEmpty {
            blocks.append(.paragraph(paragraph.joined(separator: "\n")))
            paragraph = []
        }
    }
    func flushTable() {
        if !tableRows.isEmpty {
            blocks.append(.table(rows: tableRows))
            tableRows = []
        }
    }

    for rawLine in text.split(separator: "\n", omittingEmptySubsequences: false) {
        let line = String(rawLine)
        let trimmed = line.trimmingCharacters(in: .whitespaces)

        // 代码块内：只认结束围栏，其余原样收集（代码里的 # - | 都不是语法）。
        if var lines = codeLines {
            if trimmed.hasPrefix("```") {
                blocks.append(.code(lines.joined(separator: "\n")))
                codeLines = nil
            } else {
                lines.append(line)
                codeLines = lines
            }
            continue
        }

        if trimmed.hasPrefix("```") {
            flushParagraph(); flushTable()
            codeLines = []
            continue
        }

        // 表格行：| a | b |。对齐行（|---|---|）直接丢弃，只是分隔符。
        if trimmed.hasPrefix("|") {
            flushParagraph()
            let cells = trimmed
                .trimmingCharacters(in: CharacterSet(charactersIn: "|"))
                .split(separator: "|", omittingEmptySubsequences: false)
                .map { $0.trimmingCharacters(in: .whitespaces) }
            let isDivider = cells.allSatisfy { $0.allSatisfy { "-: ".contains($0) } && !$0.isEmpty }
            if !isDivider { tableRows.append(cells) }
            continue
        }
        flushTable()

        // 标题：#{1..4} + 空格。
        if let (level, rest) = headingLine(trimmed) {
            flushParagraph()
            blocks.append(.heading(level: level, text: rest))
            continue
        }

        // 列表项。
        if let rest = prefixDropped(trimmed, prefixes: ["- ", "* ", "+ "]) {
            flushParagraph()
            blocks.append(.bullet(rest))
            continue
        }
        if let (index, rest) = orderedItem(trimmed) {
            flushParagraph()
            blocks.append(.ordered(index: index, text: rest))
            continue
        }

        // 空行断段；其余积累进当前段落。
        if trimmed.isEmpty {
            flushParagraph()
        } else {
            paragraph.append(trimmed)
        }
    }

    // 收尾：文本在流式传输中可能停在任何位置，把没闭合的都吐出来。
    if let lines = codeLines { blocks.append(.code(lines.joined(separator: "\n"))) }
    flushParagraph()
    flushTable()
    return blocks
}

private func headingLine(_ s: String) -> (Int, String)? {
    var level = 0
    var rest = Substring(s)
    while rest.first == "#", level < 4 {
        level += 1
        rest = rest.dropFirst()
    }
    guard level > 0, rest.first == " " else { return nil }
    return (level, rest.trimmingCharacters(in: .whitespaces))
}

private func prefixDropped(_ s: String, prefixes: [String]) -> String? {
    for p in prefixes where s.hasPrefix(p) {
        return String(s.dropFirst(p.count))
    }
    return nil
}

// "1. xxx" / "12. xxx" → ("1.", "xxx")
private func orderedItem(_ s: String) -> (String, String)? {
    guard let dot = s.firstIndex(of: "."), s[..<dot].allSatisfy(\.isNumber), !s[..<dot].isEmpty else { return nil }
    let after = s.index(after: dot)
    guard after < s.endIndex, s[after] == " " else { return nil }
    return (String(s[...dot]), s[s.index(after: after)...].trimmingCharacters(in: .whitespaces))
}

// 行内 markdown → AttributedString。解析失败（理论上不会）就原文纯文本兜底。
private func inline(_ s: String) -> AttributedString {
    (try? AttributedString(
        markdown: s,
        options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)
    )) ?? AttributedString(s)
}

struct MarkdownText: View {
    let text: String

    var body: some View {
        let blocks = parseBlocks(text)
        VStack(alignment: .leading, spacing: 8) {
            ForEach(blocks) { block in
                render(block)
            }
        }
    }

    @ViewBuilder
    private func render(_ block: MarkdownBlock) -> some View {
        switch block {
        case .heading(let level, let text):
            Text(inline(text))
                .font(level <= 1 ? .title3.bold() : level == 2 ? .headline : .subheadline.bold())
                .padding(.top, 2)

        case .paragraph(let text):
            Text(inline(text))

        case .bullet(let text):
            HStack(alignment: .top, spacing: 6) {
                Text("•")
                Text(inline(text))
            }

        case .ordered(let index, let text):
            HStack(alignment: .top, spacing: 6) {
                Text(index).monospacedDigit()
                Text(inline(text))
            }

        case .code(let code):
            Text(code)
                .font(.callout.monospaced())
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.primary.opacity(0.06))
                .clipShape(RoundedRectangle(cornerRadius: 8))

        case .table(let rows):
            table(rows)
        }
    }

    // 管道表格 → Grid。第一行加粗当表头，行间加细分隔线。
    private func table(_ rows: [[String]]) -> some View {
        Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
            ForEach(Array(rows.enumerated()), id: \.offset) { rowIndex, row in
                GridRow {
                    ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                        Text(inline(cell))
                            .font(rowIndex == 0 ? .callout.bold() : .callout)
                    }
                }
                if rowIndex == 0 {
                    Divider()
                }
            }
        }
        .padding(8)
        .background(Color.primary.opacity(0.04))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}
