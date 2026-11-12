-- Events overlaid on the timeline (ADR-0009): camera status changes now,
-- motion later. Not an audit trail (that is action_log, ADR-0011).
CREATE TABLE event (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    camera    TEXT NOT NULL,
    kind      TEXT NOT NULL,            -- camera.online | camera.offline | storage.low | motion ...
    at_utc    TEXT NOT NULL,
    data_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX event_camera_time ON event(camera, at_utc);
