BEGIN;

DROP TABLE IF EXISTS english.job_runs;
DROP TABLE IF EXISTS english.plan_versions;
DROP TABLE IF EXISTS english.weekly_reports;
DROP TABLE IF EXISTS english.weak_points;
DROP TABLE IF EXISTS english.word_results;
DROP TABLE IF EXISTS english.speaking_attempts;
DROP TABLE IF EXISTS english.reading_attempts;
DROP TABLE IF EXISTS english.lessons;
DROP TABLE IF EXISTS english.learning_profiles;
DROP SCHEMA IF EXISTS english;

COMMIT;
