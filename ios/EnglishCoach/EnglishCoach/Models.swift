import Foundation

struct VocabularyItem: Codable, Identifiable, Hashable {
    var id: String { word }
    var displayPhrase: String {
        guard let phrase, !phrase.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return word }
        return phrase
    }
    let phrase: String?
    let phraseMeaning: String?
    let word: String
    let meaning: String
    let example: String
    let pronunciation: String?

    enum CodingKeys: String, CodingKey {
        case phrase, word, meaning, example, pronunciation
        case phraseMeaning = "phrase_meaning"
    }
}

struct ReadingQuestion: Codable, Identifiable, Hashable {
    let id: String
    let type: String?
    let prompt: String
    let options: [String]
}

struct Lesson: Codable, Identifiable, Hashable {
    let id: Int64
    let date: String
    let title: String
    let passage: String
    let vocabulary: [VocabularyItem]
    let questions: [ReadingQuestion]
    let difficulty: Int
    let generationReason: String
    let estimatedMinutes: Int
    let contentMode: String
    let exerciseStyle: String
    let syllabusFocus: String
    let sourceName: String
    let sourceTitle: String
    let sourceURL: String
    let sourcePublishedAt: String
    let adaptationNote: String

    enum CodingKeys: String, CodingKey {
        case id, date, title, passage, vocabulary, questions, difficulty
        case generationReason = "generation_reason"
        case estimatedMinutes = "estimated_minutes"
        case contentMode = "content_mode"
        case exerciseStyle = "exercise_style"
        case syllabusFocus = "syllabus_focus"
        case sourceName = "source_name"
        case sourceTitle = "source_title"
        case sourceURL = "source_url"
        case sourcePublishedAt = "source_published_at"
        case adaptationNote = "adaptation_note"
    }
}

struct ReadingAnswer: Codable, Hashable {
    let questionID: String
    let value: String

    enum CodingKeys: String, CodingKey {
        case questionID = "question_id"
        case value
    }
}

struct ReadingReview: Codable, Identifiable {
    var id: String { questionID }
    let questionID: String
    let correctAnswer: String
    let isCorrect: Bool
    let explanation: String

    enum CodingKeys: String, CodingKey {
        case questionID = "question_id"
        case correctAnswer = "correct_answer"
        case isCorrect = "is_correct"
        case explanation
    }
}

struct ReadingAttempt: Codable, Identifiable {
    let id: Int64
    let userID: String
    let lessonID: Int64
    let answers: [ReadingAnswer]
    let review: [ReadingReview]?
    let correct: Int
    let total: Int
    let accuracy: Double
    let completedAt: String

    enum CodingKeys: String, CodingKey {
        case id, answers, review, correct, total, accuracy
        case userID = "user_id"
        case lessonID = "lesson_id"
        case completedAt = "completed_at"
    }
}

struct SpeakingIssue: Codable, Identifiable {
    var id: String { "\(expected)-\(actual ?? "")-\(kind)" }
    let expected: String
    let actual: String?
    let kind: String
}

struct SpeakingAttempt: Codable, Identifiable {
    let id: Int64
    let lessonID: Int64
    let durationSeconds: Double
    let transcript: String
    let wpm: Double
    let accuracy: Double
    let score: Double
    let feedback: String
    let issues: [SpeakingIssue]?
    let completedAt: String

    enum CodingKeys: String, CodingKey {
        case id, transcript, wpm, accuracy, score, feedback, issues
        case lessonID = "lesson_id"
        case durationSeconds = "duration_seconds"
        case completedAt = "completed_at"
    }
}

struct Progress: Codable {
    let level: String
    let difficulty: Int
    let completionRate4W: Double
    let readingAccuracy4W: Double
    let speakingAccuracy4W: Double
    let speakingSpeedWPM: Double
    let recurringErrors: [String]?
    let weakPatterns: [String]?
    let lessonsAssigned: Int
    let lessonsCompleted: Int
    let recommendedDifficulty: Int

    enum CodingKeys: String, CodingKey {
        case level, difficulty
        case completionRate4W = "completion_rate_4w"
        case readingAccuracy4W = "reading_accuracy_4w"
        case speakingAccuracy4W = "speaking_accuracy_4w"
        case speakingSpeedWPM = "speaking_speed_wpm"
        case recurringErrors = "recurring_errors"
        case weakPatterns = "weak_patterns"
        case lessonsAssigned = "lessons_assigned"
        case lessonsCompleted = "lessons_completed"
        case recommendedDifficulty = "recommended_difficulty"
    }
}

struct WeeklyReport: Codable, Identifiable {
    let id: Int64
    let weekStart: String
    let completionRate: Double
    let readingAccuracy: Double
    let speakingWPM: Double
    let problemWords: [String]?
    let summary: String
    let nextFocus: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id, summary
        case weekStart = "week_start"
        case completionRate = "completion_rate"
        case readingAccuracy = "reading_accuracy"
        case speakingWPM = "speaking_wpm"
        case problemWords = "problem_words"
        case nextFocus = "next_focus"
        case createdAt = "created_at"
    }
}

struct Profile: Codable {
    let userID: String
    var cet4Score: Int
    var level: String
    var dailyMinutes: Int
    var goal: String
    var difficulty: Int
    var navigationPosition: String
    var contentMode: String
    var ieltsTrack: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case level, goal, difficulty
        case userID = "user_id"
        case cet4Score = "cet4_score"
        case dailyMinutes = "daily_minutes"
        case navigationPosition = "navigation_position"
        case contentMode = "content_mode"
        case ieltsTrack = "ielts_track"
        case updatedAt = "updated_at"
    }
}

struct ProfileUpdate: Encodable {
    let cet4Score: Int
    let level: String
    let dailyMinutes: Int
    let goal: String
    let difficulty: Int
    let navigationPosition: String
    let contentMode: String
    let ieltsTrack: String

    enum CodingKeys: String, CodingKey {
        case level, goal, difficulty
        case cet4Score = "cet4_score"
        case dailyMinutes = "daily_minutes"
        case navigationPosition = "navigation_position"
        case contentMode = "content_mode"
        case ieltsTrack = "ielts_track"
    }
}
