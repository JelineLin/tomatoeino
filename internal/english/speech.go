package english

import "context"

type SpeechRecognizer interface {
	Transcribe(context.Context, string) (Transcript, error)
}

type UnavailableSpeechRecognizer struct{ Reason string }

func (u UnavailableSpeechRecognizer) Transcribe(context.Context, string) (Transcript, error) {
	return Transcript{}, &SpeechUnavailableError{Reason: u.Reason}
}

type SpeechUnavailableError struct{ Reason string }

func (e *SpeechUnavailableError) Error() string { return "语音识别暂不可用：" + e.Reason }
