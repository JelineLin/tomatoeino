// 聊天界面：消息气泡列表 + 底部输入框。助手回复以流式「打字机」呈现。
// 消息支持长按操作：复制 / 编辑并重发（用户）/ 重新回答（助手）/ 复制思考过程。
import SwiftUI
import UIKit

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

    // editMessage 编辑某条用户消息：把对话回卷到这条之前，原文放回输入框待改。
    // 语义和主流聊天 app 一致——编辑=从那个时间点重来，之后的问答全部作废。
    func editMessage(_ id: UUID) {
        guard !isSending,
              let idx = messages.firstIndex(where: { $0.id == id }),
              messages[idx].role == .user else { return }
        input = messages[idx].text
        messages.removeSubrange(idx...)
        // 客户端历史回卷后，服务端 L2 会话里存的还是完整旧历史——必须丢掉钥匙，
        // 让下一轮走 L1 全量重建，否则编辑对模型不生效。
        sessionID = ""
    }

    // regenerate 对某条助手回复不满意：作废它，拿它前面那条用户消息原样重问。
    func regenerate(_ id: UUID) async {
        guard !isSending,
              let idx = messages.firstIndex(where: { $0.id == id }),
              messages[idx].role == .assistant,
              let userIdx = messages[..<idx].lastIndex(where: { $0.role == .user }) else { return }
        let text = messages[userIdx].text
        messages.removeSubrange(userIdx...)
        sessionID = "" // 同 editMessage：回卷即弃钥匙
        input = text
        await send()
    }
}

struct ChatView: View {
    @StateObject private var vm = ChatViewModel()
    // 输入框焦点。有了它键盘才收得回：下拉列表、发送、点空白都靠把它置 false。
    @FocusState private var inputFocused: Bool
    // 语音听写。注意全篇【没有任何一处】因为它去改 inputFocused——
    // 这正是「说话时不弹键盘」的全部秘诀：麦克风和输入框是两个独立控件。
    @StateObject private var dictator = SpeechDictator()
    // 开录那一刻输入框里已有的文字。识别结果每次回调都是「整段重来」，
    // 拼在这个前缀后面，手打了半句再改用说的才不会被覆盖掉。
    @State private var dictationPrefix = ""
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                messageList
                inputBar
            }
            .navigationTitle("备餐助手")
            .navigationBarTitleDisplayMode(.inline)
        }
        // 切走这个 tab 就收麦，别让它在后台一直占着麦克风。
        .onDisappear { dictator.stop() }
        // 退到后台时系统会打断音频会话，主动收摊比等它被打断干净。
        .onChange(of: scenePhase) { phase in
            if phase != .active { dictator.stop() }
        }
        .alert("语音输入", isPresented: Binding(
            get: { dictator.errorText != nil },
            set: { if !$0 { dictator.errorText = nil } }
        )) {
            Button("知道了", role: .cancel) { dictator.errorText = nil }
        } message: {
            Text(dictator.errorText ?? "")
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
                        MessageBubble(
                            message: msg,
                            onEdit: {
                                inputFocused = true
                                vm.editMessage(msg.id)
                            },
                            onRegenerate: {
                                Task { await vm.regenerate(msg.id) }
                            }
                        )
                        .id(msg.id)
                    }
                }
                .padding()
            }
            // 下拉滚动时跟手收起键盘——这是聊天类 app 收键盘最自然的手势。
            .scrollDismissesKeyboard(.interactively)
            // 点消息区空白也收键盘（滚不动的短对话里，下拉手势用不上，得有个兜底）。
            .contentShape(Rectangle())
            .onTapGesture { inputFocused = false }
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
            inputFocused = false
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

    // send 统一入口：先收键盘再发，避免发送后键盘赖着不走、挡住刚冒出来的回答。
    // 还在录音就先停——不然话音未落，识别结果会往已经清空的输入框里回填半句。
    private func send() {
        dictator.stop()
        inputFocused = false
        Task { await vm.send() }
    }

    // toggleDictation 麦克风按钮的动作：录着就停，没录就开。
    // 开录前记下当前文字当前缀，之后识别出的整段拼在它后面。
    private func toggleDictation() {
        if dictator.isRecording {
            dictator.stop()
            return
        }
        let existing = vm.input.trimmingCharacters(in: .whitespacesAndNewlines)
        dictationPrefix = existing.isEmpty ? "" : existing + " "
        dictator.start { text in
            vm.input = dictationPrefix + text
        }
    }

    private var inputBar: some View {
        HStack(spacing: 10) {
            // 麦克风：录音中变红+实心，让家长一眼看出「在听」。
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
            .disabled(vm.isSending)
            .accessibilityLabel(dictator.isRecording ? "停止语音输入" : "语音输入")
            .animation(.easeInOut(duration: 0.15), value: dictator.isRecording)

            TextField("说点什么…", text: $vm.input, axis: .vertical)
                .lineLimit(1...4)
                .focused($inputFocused)
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
                .background(
                    RoundedRectangle(cornerRadius: 20, style: .continuous)
                        .fill(Color(.secondarySystemBackground))
                )
                .onSubmit { send() }

            Button {
                send()
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
    // 长按菜单的动作。nil = 该动作对这条消息不可用（角色不匹配或正在流式中）。
    var onEdit: (() -> Void)? = nil
    var onRegenerate: (() -> Void)? = nil

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
                .contextMenu { menuItems }
            if !isUser { Spacer(minLength: 48) }
        }
    }

    // 长按菜单：复制永远有；编辑限用户消息；重新回答限助手消息；
    // 助手带思考过程时可单独复制思考。
    @ViewBuilder
    private var menuItems: some View {
        Button {
            UIPasteboard.general.string = message.text
        } label: {
            Label("复制", systemImage: "doc.on.doc")
        }
        if isUser, let onEdit {
            Button {
                onEdit()
            } label: {
                Label("编辑并重发", systemImage: "pencil")
            }
        }
        if !isUser, let onRegenerate {
            Button {
                onRegenerate()
            } label: {
                Label("重新回答", systemImage: "arrow.clockwise")
            }
        }
        if !isUser && !message.thinking.isEmpty {
            Button {
                UIPasteboard.general.string = message.thinking
            } label: {
                Label("复制思考过程", systemImage: "brain")
            }
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
