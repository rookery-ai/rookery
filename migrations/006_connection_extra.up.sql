-- Per-connection resolved values (JSON), e.g. Jira's cloud id resolved once at connect
-- time. Exposed to action request templates as {{conn.<key>}}.
ALTER TABLE service_connections ADD COLUMN extra TEXT NOT NULL DEFAULT '';
