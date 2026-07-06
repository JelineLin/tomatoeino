// 聊天界面：消息气泡列表 + 底部输入框。助手回复以流式「打字机」呈现。
import SwiftUI

@MainActor
final class ChatViewModel: ObservableObject {
    @Published var messages: [ChatMessage] = []
    @Published var input: String = ""
    @Published var isSending: Bool = false

    private let api = APIClient()

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
            for try await event in api.streamChat(messages: history) {
                switch event {
                case .thinking(let t): messages[assistantIndex].thinking += t
                case .answer(let t): messages[assistantIndex].text += t
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
                        Text("问问「最近吃了啥」「明天晚饭别重样」「上次鳕鱼怎么做的」吧～")
                            .foregroundStyle(.secondary)
                            .padding()
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

    private var inputBar: some View {
        HStack(spacing: 8) {
            TextField("说点什么…", text: $vm.input, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .lineLimit(1...4)
                .onSubmit { Task { await vm.send() } }

            Button {
                Task { await vm.send() }
            } label: {
                Image(systemName: "arrow.up.circle.fill")
                    .font(.title)
            }
            .disabled(vm.isSending || vm.input.trimmingCharacters(in: .whitespaces).isEmpty)
        }
        .padding()
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
            if isUser { Spacer(minLength: 40) }
            bubbleContent
                .padding(10)
                .background(isUser ? Color.accentColor.opacity(0.85) : Color(.secondarySystemBackground))
                .foregroundStyle(isUser ? .white : .primary)
                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                .frame(maxWidth: .infinity, alignment: isUser ? .trailing : .leading)
            if !isUser { Spacer(minLength: 40) }
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
                    Text("…")
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
            Text(message.thinking)
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.top, 4)
        } label: {
            Label(message.text.isEmpty ? "思考中…" : "思考过程",
                  systemImage: "brain")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .tint(.secondary)
    }
}
