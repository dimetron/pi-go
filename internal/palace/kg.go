package palace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// KnowledgeGraph is the palace's bitemporal statement graph, a pure-Go port
// of Tiramemsu's core model (specs/memory/tiramemsu). Every fact is a
// statement with its own eid, so facts about facts — confidence, source,
// derivations, confirmations — are statements too ("layers"). Every change is
// kept with the transaction that made it (transaction time) and the interval
// during which the fact held (valid time); nothing is deleted.
//
// Writes go through Transact and the KGTx verbs (Assert, Create, Retract,
// Supersede, Confirm, Derive, SetPredicate). Reads go through views (Now,
// AsOf, AsOfInstant, History, ValidAt). The triple-shaped methods below
// (Add, Query, Invalidate, Timeline) keep the original palace API on top.
type KnowledgeGraph struct {
	store PalaceStore
	now   func() time.Time
}

// NewKnowledgeGraph creates a KnowledgeGraph on the store's database. The
// database is resolved on first use, so a store that is never asked for
// knowledge-graph data (a test stub) need not provide one.
func NewKnowledgeGraph(store PalaceStore) *KnowledgeGraph {
	return &KnowledgeGraph{store: store, now: time.Now}
}

// entityID normalises a name into an entity ID: lowercase, spaces to underscores, strip apostrophes.
func entityID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// Add records a fact, auto-creating entities. An identical live fact with an
// overlapping valid interval is returned instead of a duplicate (and
// confirmed when ConfirmExisting is set). Confidence and Source become layers
// on the fact; About annotates an existing statement instead of an entity;
// Support derives the fact from existing statements.
func (kg *KnowledgeGraph) Add(ctx context.Context, input TripleInput) (*Triple, error) {
	if (input.Subject == "" && input.About == 0) || input.Predicate == "" || input.Object == "" {
		return nil, fmt.Errorf("palace: kg: subject, predicate, and object are required")
	}
	if input.Confidence != nil && (*input.Confidence < 0 || *input.Confidence > 1) {
		return nil, fmt.Errorf("palace: kg: confidence must be between 0 and 1")
	}
	now := kg.now().UTC()

	subj := Ent(input.Subject)
	if input.About != 0 {
		subj = StmtRef(input.About)
	} else if err := kg.store.InsertEntity(ctx, &Entity{ID: subj.Text, Name: input.Subject, CreatedAt: now}); err != nil {
		return nil, fmt.Errorf("palace: kg: create subject entity: %w", err)
	}
	obj := Ent(input.Object)
	if input.Literal {
		obj = Str(input.Object)
	} else if err := kg.store.InsertEntity(ctx, &Entity{ID: obj.Text, Name: input.Object, CreatedAt: now}); err != nil {
		return nil, fmt.Errorf("palace: kg: create object entity: %w", err)
	}

	var (
		eid   int64
		isNew bool
	)
	_, err := kg.Transact(ctx, input.txOptions(), func(tx *KGTx) error {
		var err error
		v := Valid{From: input.ValidFrom, To: input.ValidTo}
		if len(input.Support) > 0 {
			eid, isNew, err = tx.Derive(subj, Ent(input.Predicate), obj, v, input.Support)
		} else {
			eid, isNew, err = tx.Assert(subj, Ent(input.Predicate), obj, v)
		}
		if err != nil {
			return err
		}
		if !isNew && input.ConfirmExisting {
			if _, err := tx.Confirm(eid); err != nil {
				return err
			}
		}
		return annotateEvidence(tx, eid, input.Confidence, input.Source, input.SourceFile)
	})
	if err != nil {
		return nil, err
	}
	t, err := kg.tripleByEID(ctx, eid)
	if err != nil {
		return nil, err
	}
	t.New = isNew
	return t, nil
}

// annotateEvidence adds the confidence, source and source-file layers. A
// confidence is cardinality one: a new value replaces the old.
func annotateEvidence(tx *KGTx, eid int64, confidence *float64, source, sourceFile string) error {
	if confidence != nil {
		if err := tx.SetPredicate(Ent(predConfidence), PredicateSchema{One: true}); err != nil {
			return err
		}
		if _, _, err := tx.Annotate(eid, Ent(predConfidence), Num(*confidence)); err != nil {
			return err
		}
	}
	if source != "" {
		if _, _, err := tx.Annotate(eid, Ent(predSource), Str(source)); err != nil {
			return err
		}
	}
	if sourceFile != "" {
		if _, _, err := tx.Annotate(eid, Ent(predSourceFile), Str(sourceFile)); err != nil {
			return err
		}
	}
	return nil
}

// KGQuery selects facts for QueryWith.
type KGQuery struct {
	// Entity whose facts to return, or Text to search entity names and string
	// values instead.
	Entity, Text string
	// AsOf narrows to facts valid at that time (RFC3339 or YYYY-MM-DD).
	AsOf string
	// TxAsOf shows what was believed then: a transaction number or a time.
	TxAsOf string
	// Direction is "subject", "object" or "" (both).
	Direction string
	Limit     int
}

// View resolves the query's transaction-time and valid-time selection.
func (kg *KnowledgeGraph) View(ctx context.Context, txAsOf, asOf string) (View, error) {
	v := kg.Now()
	if txAsOf != "" {
		if t, err := strconv.ParseInt(strings.TrimPrefix(txAsOf, "tx"), 10, 64); err == nil {
			v = kg.AsOf(t)
		} else {
			at, err := parseFlexibleTime(txAsOf)
			if err != nil {
				return View{}, fmt.Errorf("palace: kg: tx_as_of must be a transaction number or a time: %w", err)
			}
			if v, err = kg.AsOfInstant(ctx, at); err != nil {
				return View{}, err
			}
		}
	}
	if asOf != "" {
		at, err := parseFlexibleTime(asOf)
		if err != nil {
			return View{}, fmt.Errorf("palace: kg: invalid as_of: %w", err)
		}
		v = v.ValidAt(at)
	}
	return v, nil
}

// Query returns facts involving the named entity. If asOf is non-empty, only
// facts valid at that time ([valid_from, valid_to)) are returned. Direction can
// be "subject", "object", or "" (both).
func (kg *KnowledgeGraph) Query(ctx context.Context, entity string, asOf string, direction string) ([]*Triple, error) {
	return kg.QueryWith(ctx, KGQuery{Entity: entity, AsOf: asOf, Direction: direction})
}

// QueryWith runs a KGQuery, newest knowledge last.
func (kg *KnowledgeGraph) QueryWith(ctx context.Context, in KGQuery) ([]*Triple, error) {
	if in.Entity == "" && in.Text == "" {
		return nil, fmt.Errorf("palace: kg: entity is required")
	}
	v, err := kg.View(ctx, in.TxAsOf, in.AsOf)
	if err != nil {
		return nil, err
	}
	var out []*Triple
	err = kg.read(ctx, func(q querier) error {
		var sts []Statement
		var err error
		if in.Text != "" {
			sts, err = v.textSearch(ctx, q, in.Text, in.Limit)
		} else {
			sts, err = v.about(ctx, q, in.Entity, in.Direction)
		}
		if err != nil {
			return err
		}
		if in.Limit > 0 && len(sts) > in.Limit {
			sts = sts[len(sts)-in.Limit:]
		}
		out, err = v.triples(ctx, q, sts)
		return err
	})
	return out, err
}

// Invalidate ends every live, still-open fact (subject, predicate, object) now:
// each is superseded by the same fact with valid_to = now, so its layers carry
// over and the open version stays visible in history. Facts whose interval is
// already closed are left alone.
func (kg *KnowledgeGraph) Invalidate(ctx context.Context, subject, predicate, object string) error {
	return kg.InvalidateWith(ctx, subject, predicate, object, TxOptions{})
}

// InvalidateWith is Invalidate with transaction metadata.
func (kg *KnowledgeGraph) InvalidateWith(ctx context.Context, subject, predicate, object string, opts TxOptions) error {
	if subject == "" || predicate == "" || object == "" {
		return fmt.Errorf("palace: kg: subject, predicate, and object are required")
	}
	if opts.Reason == "" {
		opts.Reason = "invalidate"
	}
	_, err := kg.Transact(ctx, opts, func(tx *KGTx) error {
		now := time.UnixMilli(tx.instant).UTC()
		s, p, o := Ent(subject), Ent(predicate), Ent(object)
		sts, err := kg.Now().match(tx.ctx, tx.q, Pattern{Subject: &s, Predicate: &p, Object: &o})
		if err != nil {
			return err
		}
		for _, st := range sts {
			if st.ValidTo != nil {
				continue
			}
			if st.ValidFrom != nil && !st.ValidFrom.Before(now) {
				// It never held: there is no interval left to close.
				if _, err := tx.Retract(st.EID); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Supersede(st.EID, Patch{ValidTo: &now}); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Timeline returns every fact ever recorded for the named entity, including
// ended, superseded and retracted ones, in the order they were learned.
func (kg *KnowledgeGraph) Timeline(ctx context.Context, entity string) ([]*Triple, error) {
	if entity == "" {
		return nil, fmt.Errorf("palace: kg: entity is required")
	}
	v := kg.History()
	var out []*Triple
	err := kg.read(ctx, func(q querier) error {
		sts, err := v.about(ctx, q, entity, "")
		if err != nil {
			return err
		}
		out, err = v.triples(ctx, q, sts)
		return err
	})
	return out, err
}

func (kg *KnowledgeGraph) tripleByEID(ctx context.Context, eid int64) (*Triple, error) {
	var out *Triple
	v := kg.History()
	err := kg.read(ctx, func(q querier) error {
		st, err := getStatement(ctx, q, eid)
		if err != nil {
			return err
		}
		if st == nil {
			return fmt.Errorf("palace: kg: statement e%d not found", eid)
		}
		ts, err := v.triples(ctx, q, []Statement{*st})
		if err != nil {
			return err
		}
		out = ts[0]
		return nil
	})
	return out, err
}

// triples renders statements in the legacy Triple shape, with their evidence,
// status and original extraction time.
func (v View) triples(ctx context.Context, q querier, sts []Statement) ([]*Triple, error) {
	ev, err := v.evidenceFor(ctx, q, sts)
	if err != nil {
		return nil, err
	}
	out := make([]*Triple, 0, len(sts))
	for _, st := range sts {
		t := &Triple{
			ID:          "e" + strconv.FormatInt(st.EID, 10),
			EID:         st.EID,
			SubjectID:   st.Subject.String(),
			PredicateID: st.Predicate.String(),
			ObjectID:    st.Object.String(),
			ValidFrom:   st.ValidFrom,
			ValidTo:     st.ValidTo,
			ExtractedAt: st.AddedAt,
			TAdd:        st.TAdd,
		}
		if st.Object.Kind != KindEntity {
			t.ObjectKind = kindName(st.Object.Kind)
		}
		if e := ev[st.EID]; e != nil {
			t.Confidence = e.Confidence
			t.Sources = e.Sources
			t.Confirmations = e.Confirmations
			if len(e.SourceFiles) > 0 {
				t.SourceFile = e.SourceFiles[0]
			}
		}
		var legacyAt string
		switch err := q.QueryRowContext(ctx, "SELECT extracted_at FROM kg_legacy_triple WHERE eid = ?", st.EID).Scan(&legacyAt); {
		case err == nil:
			if at, perr := time.Parse(time.RFC3339, legacyAt); perr == nil {
				t.ExtractedAt = at
			}
		case !errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("palace: kg: legacy time: %w", err)
		}
		t.Status = "active"
		if st.ValidTo != nil {
			t.Status = "ended"
		}
		if st.RetKind != nil {
			t.Status = st.RetKind.String()
			if *st.RetKind == RetSupersede {
				_, newer, err := lineage(ctx, q, st.EID)
				if err != nil {
					return nil, err
				}
				if len(newer) > 0 {
					t.SupersededBy = newer[0]
				}
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func kindName(k ValueKind) string {
	switch k {
	case KindEntity:
		return "entity"
	case KindStatement:
		return "statement"
	case KindString:
		return "string"
	case KindInt:
		return "integer"
	case KindNumber:
		return "number"
	case KindBool:
		return "bool"
	case KindTime:
		return "time"
	case KindTx:
		return "transaction"
	}
	return ""
}
