package palace

// kgSchema2SQL is migration 7: the side tables of the phase-2 capabilities
// (specs/memory/tiramemsu/phase2/design.md). Nothing here changes a phase-1
// table, and only kg_volatile and kg_saved_answer are mutable: volatile state
// is by definition outside history, and saved answers are derived records.
const kgSchema2SQL = `
-- Volatile state: high-churn values with no history.
CREATE TABLE kg_volatile (
	s          INTEGER NOT NULL,
	key        INTEGER NOT NULL,
	o          INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (s, key)
);

-- Crypto-shredding: one key per data subject. Destroying the key is the only
-- way a value leaves the file; the row itself is never deleted or re-keyed.
CREATE TABLE kg_seal_key (
	subject     INTEGER PRIMARY KEY,
	key         BLOB NOT NULL,
	created_t   INTEGER NOT NULL,
	destroyed_t INTEGER
);
CREATE TRIGGER kg_seal_key_no_delete BEFORE DELETE ON kg_seal_key
BEGIN SELECT RAISE(ABORT, 'palace: kg seal keys are never deleted'); END;
CREATE TRIGGER kg_seal_key_no_replace BEFORE INSERT ON kg_seal_key
WHEN EXISTS (SELECT 1 FROM kg_seal_key WHERE subject = NEW.subject)
BEGIN SELECT RAISE(ABORT, 'palace: kg seal keys are never replaced'); END;
CREATE TRIGGER kg_seal_key_create_date BEFORE INSERT ON kg_seal_key
WHEN NEW.created_t <= (SELECT value FROM kg_meta WHERE key = 'last_t')
  OR NEW.created_t IS NOT (SELECT max(t) FROM kg_tx)
  OR NEW.destroyed_t IS NOT NULL OR length(NEW.key) IS NOT 64
BEGIN SELECT RAISE(ABORT, 'palace: kg seal key must be created by the active transaction'); END;
CREATE TRIGGER kg_seal_key_destroy_once BEFORE UPDATE ON kg_seal_key
WHEN OLD.destroyed_t IS NOT NULL
  OR NEW.subject IS NOT OLD.subject OR NEW.created_t IS NOT OLD.created_t
  OR NEW.destroyed_t IS NULL
  OR NEW.destroyed_t <= (SELECT value FROM kg_meta WHERE key = 'last_t')
  OR NEW.destroyed_t IS NOT (SELECT max(t) FROM kg_tx)
  OR NEW.key IS NOT zeroblob(64)
BEGIN SELECT RAISE(ABORT, 'palace: kg seal key can only be destroyed once, by the active transaction'); END;

-- Statement vectors: derived records, immutable once written, visible from
-- the transaction that stored them.
CREATE TABLE kg_stmt_vec (
	eid   INTEGER NOT NULL,
	model TEXT NOT NULL,
	dim   INTEGER NOT NULL,
	vec   BLOB NOT NULL,
	t     INTEGER NOT NULL,
	PRIMARY KEY (eid, model)
);
CREATE TRIGGER kg_stmt_vec_no_delete BEFORE DELETE ON kg_stmt_vec
BEGIN SELECT RAISE(ABORT, 'palace: kg vectors are never deleted'); END;
CREATE TRIGGER kg_stmt_vec_no_update BEFORE UPDATE ON kg_stmt_vec
BEGIN SELECT RAISE(ABORT, 'palace: kg vectors are immutable'); END;
CREATE TRIGGER kg_stmt_vec_no_replace BEFORE INSERT ON kg_stmt_vec
WHEN EXISTS (SELECT 1 FROM kg_stmt_vec WHERE eid = NEW.eid AND model = NEW.model)
BEGIN SELECT RAISE(ABORT, 'palace: kg vectors are never replaced'); END;
CREATE TRIGGER kg_stmt_vec_date BEFORE INSERT ON kg_stmt_vec
WHEN NEW.t <= (SELECT value FROM kg_meta WHERE key = 'last_t')
  OR NEW.t IS NOT (SELECT max(t) FROM kg_tx)
BEGIN SELECT RAISE(ABORT, 'palace: kg vector must be written by the active transaction'); END;

-- Saved answers: derived records. Saving, checking and refreshing one never
-- writes a transaction, a statement or a term.
CREATE TABLE kg_saved_answer (
	name       TEXT PRIMARY KEY,
	lang       TEXT NOT NULL,
	query      TEXT NOT NULL,
	view       TEXT NOT NULL,
	result     TEXT NOT NULL,
	deps       TEXT NOT NULL,
	preds      TEXT NOT NULL,
	negative   INTEGER NOT NULL,
	checkpoint INTEGER NOT NULL,
	stale      TEXT
);
`
