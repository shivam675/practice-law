CREATE UNIQUE INDEX sessions_assignment_stage_uniq ON sessions (assignment_id, stage_id);
CREATE UNIQUE INDEX session_events_client_id_uniq ON session_events (session_id, (payload->>'client_id')) WHERE payload ? 'client_id';
