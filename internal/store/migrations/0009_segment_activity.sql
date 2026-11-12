-- Activity profile of a segment (ADR-0015): JSON {step_s, video_kbps[],
-- sound_db[]} computed when the index reads the file; NULL when unread or
-- the file has no video.
ALTER TABLE segment ADD COLUMN activity TEXT;
