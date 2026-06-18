-- backend_type overrides the name-based backend detection for a coder profile.
-- Empty string means auto-detect (default behaviour): any binary whose name
-- contains "claude" uses the Claude backend; everything else uses generic.
-- Allowed values: '' (auto), 'claude', 'generic'.
ALTER TABLE coders ADD COLUMN backend_type TEXT NOT NULL DEFAULT '';
