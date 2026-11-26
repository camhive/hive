-- #172: one row the recording watchdog rewrites once a minute, so a
-- database on a vanished or broken disk answers with its error at once.
CREATE TABLE probe (
    id  INTEGER PRIMARY KEY CHECK (id = 1),
    at  TEXT NOT NULL
);
