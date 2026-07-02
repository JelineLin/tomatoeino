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
            for try await token in api.streamChat(messages: history) {
                messages[assistantIndex].text += token
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
            .onChange(of: vm.messages.last?.text) { _ in
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
struct MessageBubble: View {
    let message: ChatMessage

    private var isUser: Bool { message.role == .user }

    var body: some View {
        HStack {
            if isUser { Spacer(minLength: 40) }
            Text(message.text.isEmpty ? "…" : message.text)
                .padding(10)
                .background(isUser ? Color.accentColor.opacity(0.85) : Color(.secondarySystemBackground))
                .foregroundStyle(isUser ? .white : .primary)
                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                .frame(maxWidth: .infinity, alignment: isUser ? .trailing : .leading)
            if !isUser { Spacer(minLength: 40) }
        }
    }
}
