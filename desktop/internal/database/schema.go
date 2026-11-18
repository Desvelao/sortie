package database

// schema creates every table and index. It is idempotent: running it on an
// existing database changes nothing.
const schema = `
CREATE TABLE IF NOT EXISTS files (
	id INTEGER PRIMARY KEY,
	source_path TEXT NOT NULL UNIQUE,
	filename TEXT NOT NULL,
	extension TEXT,
	mime_type TEXT,
	file_size INTEGER,
	modified_time REAL,
	status TEXT DEFAULT 'PENDING'
);
CREATE TABLE IF NOT EXISTS tags (
	id INTEGER PRIMARY KEY,
	profile TEXT NOT NULL DEFAULT 'default',
	name TEXT NOT NULL,
	shortcut TEXT,
	destination TEXT NOT NULL,
	enabled INTEGER DEFAULT 1,
	sort_order INTEGER DEFAULT 0,
	UNIQUE (profile, name),
	UNIQUE (profile, shortcut)
);
CREATE TABLE IF NOT EXISTS file_tags (
	file_id INTEGER NOT NULL,
	tag_id INTEGER NOT NULL,
	assigned_at TEXT NOT NULL,
	PRIMARY KEY (file_id, tag_id),
	FOREIGN KEY (file_id) REFERENCES files(id),
	FOREIGN KEY (tag_id) REFERENCES tags(id)
);
CREATE TABLE IF NOT EXISTS session (
	id INTEGER PRIMARY KEY,
	input_directory TEXT NOT NULL,
	current_file_id INTEGER,
	last_opened TEXT
);
-- tag_id becomes NULL when a tag is removed, so the history survives; tag_name keeps the label.
CREATE TABLE IF NOT EXISTS copy_history (
	id INTEGER PRIMARY KEY,
	file_id INTEGER NOT NULL,
	tag_id INTEGER,
	tag_name TEXT,
	source_path TEXT NOT NULL,
	destination_path TEXT NOT NULL,
	source_hash TEXT,
	destination_hash TEXT,
	status TEXT NOT NULL,
	copied_at TEXT,
	error_message TEXT,
	FOREIGN KEY (file_id) REFERENCES files(id),
	FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_copy_history_file ON copy_history(file_id);
CREATE INDEX IF NOT EXISTS idx_copy_history_status ON copy_history(status);
-- per-profile manual values of variables
CREATE TABLE IF NOT EXISTS file_vars (
	file_id INTEGER NOT NULL,
	profile TEXT NOT NULL,
	name TEXT NOT NULL,
	value TEXT NOT NULL,
	PRIMARY KEY (file_id, profile, name),
	FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);
`
