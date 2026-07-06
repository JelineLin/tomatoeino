// 聊天界面：消息气泡列表 + 底部输入框。助手回复以流式「打字机」呈现。
import SwiftUI

@MainActor
final class ChatViewModel: ObservableObject {
    @Published var messages: [ChatMessage] = []
    @Published var input: String = ""
    @Published var isSending: Bool = false

    private let api = APIClient()
    // 会话钥匙（L2）：后端每轮流末尾发回，下一轮带上可命中服务端的全保真历史。
    // 只存内存——App 重启丢了也没关系，messages 全量重发会自动降级 L1 再开新会话。
    private var sessionID = ""

    func send() async {
        let text = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !isSending else { return }

        input = ""
        isSending = true

        messages.append(ChatMessage(role: .user, text: text))
        // 把「到此为止」的对话快照发给后端（不含下面这条空的助手占位）。
        let history = messages
        messages.append(ChatMessage(role: .assistant, text: ""))
        let assistantIndex = messages.count - 1

        do {
            for try await event in api.streamChat(messages: history, sessionID: sessionID) {
                switch event {
                case .thinking(let t): messages[assistantIndex].thinking += t
                case .answer(let t): messages[assistantIndex].text += t
                // 工具备忘：流末尾整体到一次，存到这条助手消息上，下一轮随历史带回。
                case .context(let t): messages[assistantIndex].context = t
                // 会话钥匙：存下，下一轮带回（后端可能换新——过期重开时以最新的为准）。
                case .session(let id): sessionID = id
                }
            }
        } catch {
            messages[assistantIndex].text += "\n⚠️ \(error.localizedDescription)"
        }

        isSending = false
    }
}

struct ChatView: View {
    @StateObject private var vm = ChatViewModel()

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                messageList
                inputBar
            }
            .navigationTitle("备餐助手")
            .navigationBarTitleDisplayMode(.inline)
        }
    }

    private var messageList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    if vm.messages.isEmpty {
                        emptyState
                    }
                    ForEach(vm.messages) { msg in
                        MessageBubble(message: msg)
                            .id(msg.id)
                    }
                }
                .padding()
            }
            // 思考流和答案流都会让气泡长高，两者任一有增量就跟着滚到底。
            .onChange(of: vm.messages.last.map { $0.thinking + $0.text }) { _ in
                if let last = vm.messages.last {
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }
        }
    }

    // 空态：一句引导 + 三个可直接点的示例问题，点了立刻发送——
    // 比一行灰字「问问xxx吧」的转化率高得多，第一次打开就知道这 app 能干嘛。
    private var emptyState: some View {
        VStack(spacing: 18) {
            // 用 SF Symbol 而非 emoji：部分新装模拟器的 emoji 字体缓存未就绪会渲染成豆腐块，
            // 系统符号没有这个问题，且跟随主题色。
            Image(systemName: "fork.knife.circle.fill")
                .font(.system(size: 56))
                .foregroundStyle(.orange)
            Text("今天吃点啥？")
                .font(.title3.bold())
            Text("我会先翻宝宝的吃饭历史和时令表，再给建议")
                .font(.footnote)
                .foregroundStyle(.secondary)
            VStack(spacing: 8) {
                suggestionChip("最近三天吃了啥？")
                suggestionChip("明天三餐帮我配一下，别和最近重样")
                suggestionChip("这个月应季的水果有哪些？")
            }
            .padding(.top, 4)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 48)
    }

    private func suggestionChip(_ text: String) -> some View {
        Button {
            vm.input = text
            Task { await vm.send() }
        } label: {
            Text(text)
                .font(.callout)
                .foregroundStyle(.orange)
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .background(Capsule().fill(Color.orange.opacity(0.1)))
        }
        .buttonStyle(.plain)
    }

    private var canSend: Bool {
        !vm.isSending && !vm.input.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private var inputBar: some View {
        HStack(spacing: 10) {
            TextField("说点什么…", text: $vm.input, axis: .vertical)
                .lineLimit(1...4)
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
                .background(
                    RoundedRectangle(cornerRadius: 20, style: .continuous)
                        .fill(Color(.secondarySystemBackground))
                )
                .onSubmit { Task { await vm.send() } }

            Button {
                Task { await vm.send() }
            } label: {
                Group {
                    if vm.isSending {
                        ProgressView().tint(.white)
                    } else {
                        Image(systemName: "arrow.up")
                            .font(.body.weight(.semibold))
                            .foregroundStyle(.white)
                    }
                }
                .frame(width: 36, height: 36)
                .background(Circle().fill(canSend ? Color.orange : Color.gray.opacity(0.35)))
            }
            .disabled(!canSend)
            .animation(.easeInOut(duration: 0.15), value: canSend)
        }
        .padding(.horizontal)
        .padding(.vertical, 10)
        .background(.bar)
    }
}

// MessageBubble 一条消息的气泡。用户靠右、助手靠左。
// 助手回复分两段：思考过程（灰色、可折叠，流式期间自动展开）+ 答案（Markdown 富文本）。
// 用户输入是随手打的字，原样展示。
struct MessageBubble: View {
    let message: ChatMessage

    // 思考区的展开状态：默认展开（正在思考时让用户看到进度），
    // 答案一开始出现就自动收起——此时用户的注意力应该转向结论。
    @State private var thinkingExpanded = true

    private var isUser: Bool { message.role == .user }

    var body: some View {
        HStack {
            if isUser { Spacer(minLength: 48) }
            bubbleContent
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .background(bubbleBackground)
                .foregroundStyle(isUser ? .white : .primary)
                .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
                .shadow(color: .black.opacity(isUser ? 0 : 0.06), radius: 5, y: 2)
                .frame(maxWidth: .infinity, alignment: isUser ? .trailing : .leading)
            if !isUser { Spacer(minLength: 48) }
        }
    }

    // 用户气泡上暖橙渐变（和全局主色一脉），助手气泡是浅底卡片加淡投影。
    @ViewBuilder
    private var bubbleBackground: some View {
        if isUser {
            LinearGradient(
                colors: [Color.orange, Color(red: 0.95, green: 0.45, blue: 0.2)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        } else {
            Color(.secondarySystemBackground)
        }
    }

    @ViewBuilder
    private var bubbleContent: some View {
        if isUser {
            Text(message.text)
        } else {
            VStack(alignment: .leading, spacing: 8) {
                if !message.thinking.isEmpty {
                    thinkingSection
                }
                if !message.text.isEmpty {
                    MarkdownText(text: message.text)
                } else if message.thinking.isEmpty {
                    TypingDots()
                }
            }
            .onChange(of: message.text.isEmpty) { isEmpty in
                if !isEmpty {
                    withAnimation { thinkingExpanded = false }
                }
            }
        }
    }

    private var thinkingSection: some View {
        DisclosureGroup(isExpanded: $thinkingExpanded) {
            // 左侧一条橙色细线做「引用」视觉——一眼区分过程和结论。
            HStack(alignment: .top, spacing: 8) {
                Capsule()
                    .fill(Color.orange.opacity(0.45))
                    .frame(width: 3)
                Text(message.thinking)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.top, 6)
        } label: {
            HStack(spacing: 6) {
                if message.text.isEmpty {
                    ProgressView()
                        .controlSize(.mini)
                    Text("思考中…")
                } else {
                    Image(systemName: "brain")
                    Text("思考过程")
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .tint(.secondary)
    }
}

// TypingDots 三个小圆点的「对方正在输入」动画——请求刚发出、
// 思考流还没到达的零点几秒里，比一个静止的「…」更有活着的感觉。
struct TypingDots: View {
    @State private var on = false

    var body: some View {
        HStack(spacing: 4) {
            ForEach(0..<3, id: \.self) { i in
                Circle()
                    .frame(width: 6, height: 6)
                    .opacity(on ? 1 : 0.25)
                    .animation(
                        .easeInOut(duration: 0.55).repeatForever().delay(Double(i) * 0.18),
                        value: on
                    )
            }
        }
        .foregroundStyle(.secondary)
        .padding(.vertical, 4)
        .onAppear { on = true }
    }
}
