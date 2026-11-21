package palace

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// kgSchemaSQL is migration 6: the bitemporal statement graph.
//
// It follows Tiramemsu's storage format (tmp/tiramemsu/lat.md/storage.md):
// one row per statement occurrence with its own eid, a transaction-time
// lifetime [t_add, t_ret) and a valid-time interval [v_from, v_to) in epoch
// milliseconds. The triggers make "never forget" a property of the file:
// nothing is deleted, a statement's content never changes, a statement is
// retracted at most once, and dates can only be written by the transaction
// that is currently open, so committed history cannot be rewritten.
const kgSchemaSQL = `
CREATE TABLE kg_meta (
	key   TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
INSERT INTO kg_meta (key, value) VALUES ('last_t', 0);

CREATE TABLE kg_tx (
	t       INTEGER PRIMARY KEY,
	instant INTEGER NOT NULL UNIQUE,
	author  TEXT NOT NULL DEFAULT '',
	source  TEXT NOT NULL DEFAULT '',
	reason  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE kg_term (
	id  INTEGER PRIMARY KEY AUTOINCREMENT,
	tag INTEGER NOT NULL,
	lex TEXT NOT NULL,
	num REAL,
	UNIQUE (tag, lex)
);

CREATE TABLE kg_stmt (
	eid      INTEGER PRIMARY KEY AUTOINCREMENT,
	s        INTEGER NOT NULL,
	p        INTEGER NOT NULL,
	o        INTEGER NOT NULL,
	t_add    INTEGER NOT NULL,
	t_ret    INTEGER,
	v_from   INTEGER,
	v_to     INTEGER,
	ret_kind INTEGER
);
CREATE INDEX kg_live_spo ON kg_stmt(s, p, o, v_from, v_to) WHERE t_ret IS NULL;
CREATE INDEX kg_live_pos ON kg_stmt(p, o, s, v_from, v_to) WHERE t_ret IS NULL;
CREATE INDEX kg_live_osp ON kg_stmt(o, s, p, v_from, v_to) WHERE t_ret IS NULL;
CREATE INDEX kg_hist_spo ON kg_stmt(s, p, o, t_add, t_ret);
CREATE INDEX kg_hist_pos ON kg_stmt(p, o, s, t_add, t_ret);
CREATE INDEX kg_hist_osp ON kg_stmt(o, s, p, t_add, t_ret);
CREATE INDEX kg_log_add ON kg_stmt(t_add);
CREATE INDEX kg_log_ret ON kg_stmt(t_ret, ret_kind) WHERE t_ret IS NOT NULL;

-- Original id and extraction time of each triple imported from the legacy
-- triples table.
CREATE TABLE kg_legacy_triple (
	legacy_id    TEXT PRIMARY KEY,
	eid          INTEGER NOT NULL UNIQUE,
	extracted_at TEXT NOT NULL
);

CREATE VIEW kg_event (t, eid, op, kind) AS
	SELECT t_add, eid, 'assert', NULL FROM kg_stmt
	UNION ALL
	SELECT t_ret, eid, 'retract', ret_kind FROM kg_stmt WHERE t_ret IS NOT NULL;

-- Standalone (not external-content) index: it holds exactly the entity and
-- string terms, so it never disagrees with a content table.
CREATE VIRTUAL TABLE kg_term_fts USING fts5(lex, tokenize = 'unicode61 remove_diacritics 2');
CREATE TRIGGER kg_term_fts_ai AFTER INSERT ON kg_term WHEN NEW.tag IN (1, 3)
BEGIN INSERT INTO kg_term_fts (rowid, lex) VALUES (NEW.id, NEW.lex); END;

-- Never forget: statements.
CREATE TRIGGER kg_stmt_no_delete BEFORE DELETE ON kg_stmt
BEGIN SELECT RAISE(ABORT, 'palace: kg statements are never deleted'); END;
CREATE TRIGGER kg_stmt_no_replace BEFORE INSERT ON kg_stmt
WHEN EXISTS (SELECT 1 FROM kg_stmt WHERE eid = NEW.eid)
BEGIN SELECT RAISE(ABORT, 'palace: kg statement ids are never reused'); END;
CREATE TRIGGER kg_stmt_add_date BEFORE INSERT ON kg_stmt
WHEN NEW.t_add <= (SELECT value FROM kg_meta WHERE key = 'last_t')
  OR NEW.t_add IS NOT (SELECT max(t) FROM kg_tx)
  OR NEW.t_ret IS NOT NULL OR NEW.ret_kind IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'palace: kg assertion must use the active transaction'); END;
CREATE TRIGGER kg_stmt_retract_once BEFORE UPDATE ON kg_stmt
WHEN OLD.t_ret IS NOT NULL
  OR NEW.eid IS NOT OLD.eid OR NEW.s IS NOT OLD.s OR NEW.p IS NOT OLD.p
  OR NEW.o IS NOT OLD.o OR NEW.t_add IS NOT OLD.t_add
  OR NEW.v_from IS NOT OLD.v_from OR NEW.v_to IS NOT OLD.v_to
  OR NEW.t_ret IS NULL OR NEW.ret_kind IS NULL
BEGIN SELECT RAISE(ABORT, 'palace: kg statement content is immutable and retracted once'); END;
CREATE TRIGGER kg_stmt_ret_date BEFORE UPDATE OF t_ret ON kg_stmt
WHEN NEW.t_ret <= (SELECT value FROM kg_meta WHERE key = 'last_t')
  OR NEW.t_ret IS NOT (SELECT max(t) FROM kg_tx)
  OR NEW.t_ret < OLD.t_add
BEGIN SELECT RAISE(ABORT, 'palace: kg retraction must use the active transaction'); END;

-- Never forget: terms.
CREATE TRIGGER kg_term_no_delete BEFORE DELETE ON kg_term
BEGIN SELECT RAISE(ABORT, 'palace: kg terms are never deleted'); END;
CREATE TRIGGER kg_term_no_update BEFORE UPDATE ON kg_term
BEGIN SELECT RAISE(ABORT, 'palace: kg terms are immutable'); END;
CREATE TRIGGER kg_term_no_replace BEFORE INSERT ON kg_term
WHEN EXISTS (SELECT 1 FROM kg_term WHERE id = NEW.id OR (tag = NEW.tag AND lex = NEW.lex))
BEGIN SELECT RAISE(ABORT, 'palace: kg terms are never replaced'); END;

-- Never forget: transactions. A new one must be the next after the committed
-- watermark, so only one can be open and none can be inserted into the past.
CREATE TRIGGER kg_tx_no_delete BEFORE DELETE ON kg_tx
BEGIN SELECT RAISE(ABORT, 'palace: kg transactions are never deleted'); END;
CREATE TRIGGER kg_tx_no_update BEFORE UPDATE ON kg_tx
BEGIN SELECT RAISE(ABORT, 'palace: kg transactions are immutable'); END;
CREATE TRIGGER kg_tx_no_replace BEFORE INSERT ON kg_tx
WHEN EXISTS (SELECT 1 FROM kg_tx WHERE t = NEW.t OR instant = NEW.instant)
BEGIN SELECT RAISE(ABORT, 'palace: kg transactions are never replaced'); END;
CREATE TRIGGER kg_tx_in_order BEFORE INSERT ON kg_tx
WHEN NEW.t IS NOT (SELECT value FROM kg_meta WHERE key = 'last_t') + 1
  OR NEW.instant <= COALESCE((SELECT max(instant) FROM kg_tx), -9223372036854775807)
BEGIN SELECT RAISE(ABORT, 'palace: kg transactions are allocated in order'); END;

-- The watermark only moves forward, to the open transaction.
CREATE TRIGGER kg_meta_no_delete BEFORE DELETE ON kg_meta
BEGIN SELECT RAISE(ABORT, 'palace: kg meta rows are never deleted'); END;
CREATE TRIGGER kg_meta_no_insert BEFORE INSERT ON kg_meta
BEGIN SELECT RAISE(ABORT, 'palace: kg meta rows are fixed'); END;
CREATE TRIGGER kg_meta_advance BEFORE UPDATE ON kg_meta
WHEN NEW.key IS NOT OLD.key OR NEW.key IS NOT 'last_t'
  OR NEW.value IS NOT (SELECT max(t) FROM kg_tx) OR NEW.value < OLD.value
BEGIN SELECT RAISE(ABORT, 'palace: kg watermark only advances to the open transaction'); END;
`

// legacyFreezeSQL closes the legacy triples table once it has been imported.
// A pi from before migration 6 that opens this file would otherwise write
// facts the new graph never reads, and report success.
const legacyFreezeSQL = `
CREATE TRIGGER triples_frozen_insert BEFORE INSERT ON triples
BEGIN SELECT RAISE(ABORT, 'palace: the knowledge graph moved to kg_stmt (schema 6); upgrade pi'); END;
CREATE TRIGGER triples_frozen_update BEFORE UPDATE ON triples
BEGIN SELECT RAISE(ABORT, 'palace: the knowledge graph moved to kg_stmt (schema 6); upgrade pi'); END;
CREATE TRIGGER triples_frozen_delete BEFORE DELETE ON triples
BEGIN SELECT RAISE(ABORT, 'palace: the knowledge graph moved to kg_stmt (schema 6); upgrade pi'); END;
`

type legacyTriple struct {
	id, subj, pred, obj string
	validFrom, validTo  sql.NullString
	sourceFile          string
	extractedAt         string
}

// importLegacyTriples copies every legacy triple into the statement graph, one
// transaction per triple in extraction order, so tx-time views before a
// triple's extraction do not show it. The triple keeps its valid time (a
// legacy invalidation becomes a closed interval, which is what it meant), its
// source file as a layer, and its original id and timestamp in
// kg_legacy_triple. The legacy table is then frozen, never deleted.
func importLegacyTriples(sqlTx *sql.Tx) error {
	ctx := context.Background()
	rows, err := sqlTx.QueryContext(ctx, `SELECT id, subject_id, predicate_id, object_id,
		valid_from, valid_to, source_file, extracted_at
		FROM triples ORDER BY extracted_at, rowid`)
	if err != nil {
		return fmt.Errorf("read legacy triples: %w", err)
	}
	var legacy []legacyTriple
	for rows.Next() {
		var l legacyTriple
		if err := rows.Scan(&l.id, &l.subj, &l.pred, &l.obj, &l.validFrom, &l.validTo, &l.sourceFile, &l.extractedAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy triple: %w", err)
		}
		legacy = append(legacy, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read legacy triples: %w", err)
	}

	var prevInstant int64
	for _, l := range legacy {
		instant := time.Now().UnixMilli()
		if at, err := time.Parse(time.RFC3339, l.extractedAt); err == nil {
			instant = at.UnixMilli()
		}
		instant = max(instant, prevInstant+1)
		prevInstant = instant
		if err := importLegacyTriple(ctx, sqlTx, l, instant); err != nil {
			return fmt.Errorf("import legacy triple %s: %w", l.id, err)
		}
	}
	if _, err := sqlTx.ExecContext(ctx, legacyFreezeSQL); err != nil {
		return fmt.Errorf("freeze legacy triples: %w", err)
	}
	return nil
}

func importLegacyTriple(ctx context.Context, sqlTx *sql.Tx, l legacyTriple, instant int64) error {
	reason := "import legacy triple " + l.id
	v := Valid{From: parseLegacyTime(l.validFrom), To: parseLegacyTime(l.validTo)}
	if v.From != nil && v.To != nil && !v.From.Before(*v.To) {
		// An interval the old store accepted but which is empty: keep the
		// start, record the anomaly on the transaction.
		v.To = nil
		reason += " (empty valid interval dropped its end)"
	}
	w, err := beginWriter(ctx, sqlTx, TxOptions{Author: "migration", Reason: reason}, instant)
	if err != nil {
		return err
	}
	// Legacy ids are already normalized; use them verbatim.
	s := Value{Kind: KindEntity, Text: l.subj}
	p := Value{Kind: KindEntity, Text: l.pred}
	o := Value{Kind: KindEntity, Text: l.obj}
	eid, err := w.create(s, p, o, v, writeLegacy)
	if err != nil {
		return err
	}
	if l.sourceFile != "" {
		if _, _, err := w.Assert(StmtRef(eid), Ent(predSourceFile), Str(l.sourceFile), Valid{}); err != nil {
			return err
		}
	}
	if _, err := sqlTx.ExecContext(ctx,
		"INSERT INTO kg_legacy_triple (legacy_id, eid, extracted_at) VALUES (?, ?, ?)",
		l.id, eid, l.extractedAt); err != nil {
		return fmt.Errorf("record legacy id: %w", err)
	}
	return w.finish()
}

func parseLegacyTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := parseFlexibleTime(s.String)
	if err != nil {
		return nil
	}
	return &t
}
