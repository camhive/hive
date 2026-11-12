-- Export jobs (#59): the list survives a restart; the retention sweeper
-- removes old jobs and their files. A job that was running when the hive
-- stopped is marked failed at the next start.
CREATE TABLE export_job (
    id           TEXT PRIMARY KEY,
    owner        TEXT NOT NULL,             -- account id that asked; jobs are listed per owner
    camera       TEXT NOT NULL,
    stream       TEXT NOT NULL CHECK (stream IN ('main', 'sub')),
    start_utc    TEXT NOT NULL,
    duration_ms  INTEGER NOT NULL CHECK (duration_ms > 0),
    status       TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed')),
    error        TEXT NOT NULL DEFAULT '',
    bytes        INTEGER NOT NULL DEFAULT 0,
    file_name    TEXT NOT NULL,
    created_utc  TEXT NOT NULL,
    done_utc     TEXT NOT NULL DEFAULT ''   -- empty while running
);
CREATE INDEX export_job_owner ON export_job(owner, created_utc);
