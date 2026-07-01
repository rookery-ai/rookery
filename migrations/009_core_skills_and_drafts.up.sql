-- Core skills pivot: drop the admin-curated skill library infrastructure.
-- Core skills now ship embedded in the binary and are always-on for every user;
-- users create/import their own skills only (no admin gate). See internal/skilllibrary.

-- The admin catalog table (created by 008) is gone. Skill content is read from the
-- embed at runtime, never from this table.
DROP TABLE IF EXISTS skill_library;

-- These columns tracked which library skill a user skill was installed from. With no
-- library, they are meaningless. (SQLite 3.35+ DROP COLUMN.)
ALTER TABLE skills DROP COLUMN library_slug;
ALTER TABLE skills DROP COLUMN library_version;

-- Skill design drafts: persist in-progress skill-creator sessions so a page reload,
-- browser close, or server restart doesn't lose the conversation (and, for
-- StateVerifying, the generated skill content). One draft per user. Mirrors agent_drafts.
CREATE TABLE IF NOT EXISTS skill_drafts (
    user_id             TEXT PRIMARY KEY,
    skill_name          TEXT NOT NULL,
    state               TEXT NOT NULL,               -- "designing" or "verifying"
    history_json        TEXT NOT NULL DEFAULT '[]',
    pending_skill_md    TEXT NOT NULL DEFAULT '',
    pending_scripts_json TEXT NOT NULL DEFAULT '{}',
    vetting_report      TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at           DATETIME NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);