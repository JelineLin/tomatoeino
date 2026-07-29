// 语音听写：把「说的话」变成输入框里的字，全程不弹软键盘。
//
// 为什么自己搭而不用键盘上那个 🎤：那个听写键是【键盘的一部分】，要用它必须先把键盘弹出来——
// 而厨房场景恰恰是「手上湿的、抱着娃、不想看屏幕」，键盘一弹就占掉半屏、还挡住刚出的回答。
// SFSpeechRecognizer + AVAudioEngine 是一条【独立于键盘】的音频→文字管线：
// 麦克风按钮和输入框是两个控件，点它不会让输入框获得焦点，所以键盘从头到尾不出现。
//
// 分层照搬 APIClient 的路子：AVFoundation/Speech 的所有细节收敛在这个类里，
// 界面只管调 start/stop、读 isRecording——像 RPC 客户端把连接池、重试、编解码全包起来，
// 上层只看见「发一个请求、拿一个结果」。

import AVFoundation
import Speech
import SwiftUI

@MainActor
final class SpeechDictator: ObservableObject {
    /// 是否正在录音——界面据此换图标/上色。
    @Published private(set) var isRecording = false
    /// 出错原因（权限被拒、识别不可用等）。界面弹提示用，nil = 无错。
    @Published var errorText: String?

    /// 识别器认死中文。语音识别是【按语言】加载模型的，不指定就跟着系统语言走，
    /// 系统是英文时说中文会得到一串音译乱码。
    private let recognizer = SFSpeechRecognizer(locale: Locale(identifier: "zh-CN"))

    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var task: SFSpeechRecognitionTask?
    private let engine = AVAudioEngine()

    /// 优先本地识别：音频不出手机，也不依赖网络。
    /// 代价是中文本地模型的准确率通常不如苹果服务端（尤其菜名这种低频词）——
    /// 想换成「一律走服务端、准头优先」，把这里改成 false 即可，其余代码不用动。
    /// 注意：设备没下载中文听写模型时 supportsOnDeviceRecognition 为 false，
    /// 那时会自动退回服务端识别（音频送到苹果，不经过我们的服务器、也不经过豆包）。
    private let preferOnDevice = true

    /// start 开录。onText 会被【多次】调用——每识别出一点就回调一次当前的完整文本
    /// （不是增量），界面直接整体替换即可，这是「边说边出字」的来源。
    func start(onText: @escaping (String) -> Void) {
        guard !isRecording else { return }
        errorText = nil

        guard let recognizer else {
            errorText = "这台设备不支持中文语音识别。"
            return
        }
        guard recognizer.isAvailable else {
            errorText = "语音识别暂时不可用，稍后再试。"
            return
        }

        // 两道权限缺一不可：能听（麦克风）+ 能认（语音识别）。
        // 都是首次使用弹一次的系统弹窗，用户点过就记住了。
        requestPermissions { [weak self] ok, reason in
            guard let self else { return }
            guard ok else {
                self.errorText = reason
                return
            }
            do {
                try self.beginSession(recognizer: recognizer, onText: onText)
                self.isRecording = true
            } catch {
                self.errorText = "启动录音失败：\(error.localizedDescription)"
                self.teardown()
            }
        }
    }

    /// stop 停录。文字留在输入框里等家长确认——不自动发送，因为中文菜名很容易识别错
    /// （「鳕鱼」听成「雪鱼」、「羊肚菌」听成「羊肚菇」），错字直接进宝宝的历史记录就麻烦了。
    func stop() {
        guard isRecording else { return }
        isRecording = false
        teardown()
    }

    // MARK: - 内部

    /// beginSession 搭起「麦克风 → 缓冲 → 识别器」这条流水线。
    private func beginSession(recognizer: SFSpeechRecognizer, onText: @escaping (String) -> Void) throws {
        // 音频会话：录音模式 + 压低别家的声音（家长可能正放着儿歌）。
        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.record, mode: .measurement, options: .duckOthers)
        try session.setActive(true, options: .notifyOthersOnDeactivation)

        let req = SFSpeechAudioBufferRecognitionRequest()
        // 这一行就是「边说边出字」的开关：关掉的话要等整段说完才给结果。
        req.shouldReportPartialResults = true
        if preferOnDevice && recognizer.supportsOnDeviceRecognition {
            req.requiresOnDeviceRecognition = true
        }
        request = req

        // 把麦克风的音频缓冲源源不断喂给识别请求。format 必须问 inputNode 要——
        // 硬编码采样率在不同设备/接了蓝牙耳机时会直接崩。
        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { buffer, _ in
            req.append(buffer)
        }
        engine.prepare()
        try engine.start()

        // 识别回调在任意队列上来，跳回主线程再碰 UI 状态。
        task = recognizer.recognitionTask(with: req) { [weak self] result, error in
            Task { @MainActor in
                guard let self else { return }
                if let result {
                    onText(result.bestTranscription.formattedString)
                    // isFinal：识别器认为这一段结束了。单次识别有大约 1 分钟上限，
                    // 厨房里都是短句够用；真要连续长录，得在这里重开一段接力。
                    if result.isFinal { self.stop() }
                }
                if error != nil && self.isRecording {
                    // 正常停止也会走到这里（endAudio 后识别器报「取消」），
                    // 所以不弹错误，安静收摊即可——真正的启动失败在 start 里已经报过了。
                    self.stop()
                }
            }
        }
    }

    /// teardown 拆流水线。顺序有讲究：先停引擎摘 tap（不再进新数据），
    /// 再 endAudio 让识别器把手上剩的收尾，最后取消 task 并把音频会话还回去——
    /// 会话不还，别家 App 的声音会一直被压着。
    private func teardown() {
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        request?.endAudio()
        task?.cancel()
        request = nil
        task = nil
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    /// requestPermissions 串行要两道权限，任一被拒就带着人话原因回调 false。
    private func requestPermissions(_ done: @escaping (Bool, String?) -> Void) {
        SFSpeechRecognizer.requestAuthorization { status in
            Task { @MainActor in
                guard status == .authorized else {
                    done(false, "没有语音识别权限。到「设置 → 备餐助手」里打开「语音识别」即可。")
                    return
                }
                Self.requestMic { granted in
                    done(granted, granted ? nil : "没有麦克风权限。到「设置 → 备餐助手」里打开「麦克风」即可。")
                }
            }
        }
    }

    /// 麦克风授权的 API 在 iOS 17 换了地方（AVAudioSession → AVAudioApplication），
    /// 部署目标是 16.0，两边都得留。
    private static func requestMic(_ done: @escaping (Bool) -> Void) {
        if #available(iOS 17.0, *) {
            AVAudioApplication.requestRecordPermission { granted in
                Task { @MainActor in done(granted) }
            }
        } else {
            AVAudioSession.sharedInstance().requestRecordPermission { granted in
                Task { @MainActor in done(granted) }
            }
        }
    }
}
