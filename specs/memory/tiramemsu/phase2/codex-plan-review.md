# Codex review of the phase-2 plan (2026-11-21)

Run: `codex exec --sandbox read-only` over phase2/design.md, plan.md, the phase-1 design and `internal/palace/kg_*.go`, with the Tiramemsu specs. Verbatim findings; resolutions are in design.md, "Review resolution".

1. HIGH — [phase2/design.md:36](phase2/design.md:36) — Seal-key guards omit replacement protection; `INSERT OR REPLACE` bypasses UPDATE guards and normally DELETE triggers, allowing re-keying or revival — Add a BEFORE INSERT conflict guard and creation/destruction guards requiring `max(t) > last_t`; test raw REPLACE and backdating.

2. HIGH — [phase2/design.md:64](phase2/design.md:64) — `secure_delete` leaves recoverable key versions in WAL — Require a successful post-commit checkpoint/truncation before reporting physical erasure, with explicit busy-reader handling, or keep keys outside SQLite; test retained WAL bytes.

3. HIGH — [phase2/plan.md:40](phase2/plan.md:40) — Sealing integration omits existing plaintext-interning paths: `encode3` interns before schema checks, and Supersede encodes patches directly — Centralize sealing before any interning; inspect terms/FTS after Assert, Create, Derive, Supersede and bundle import.

4. HIGH — [phase2/design.md:130](phase2/design.md:130) — Volatile writes have no sensitivity policy; ordinary encoding permanently stores plaintext in immutable terms and FTS, surviving clear/erase — Seal sensitive volatile values before interning or reject sensitive keys; test overwrite, clear and erase.

5. HIGH — [phase2/design.md:55](phase2/design.md:55) — Saved query/result JSON preserves decrypted secrets after their subject key is destroyed; historical answers even remain “fresh” — Reject sensitive saved answers or encrypt their payloads with erasure-aware keys; invalidate them on erasure, including AsOf answers.

6. HIGH — [phase2/design.md:123](phase2/design.md:123) — Bundles export decoded literals without sensitivity metadata; fresh-target import stores them as plaintext and indexes them — Preserve sensitivity and require resealing before target interning, or reject sensitive bundles; test encrypted-at-rest round trips.

7. HIGH — [phase2/design.md:152](phase2/design.md:152) — Existing Match encodes bound literals as plaintext IDs, so sealed constants and joins miss matching statements — Use subject-aware encrypted lookup where possible and decrypted candidate comparison otherwise; test constant matches and equal literals across subjects.

8. HIGH — [phase2/design.md:343](phase2/design.md:343) — `Sensitive+Unique` breaks object-ID uniqueness: equal emails encrypted under different subject keys have different IDs; subjectless Upsert also cannot locate them — Reject this combination or compare decrypted canonical values during uniqueness checks/upsert; test both flag-setting orders.

9. HIGH — [phase2/design.md:290](phase2/design.md:290) — Stamping new vectors with current `last_t` changes an already committed AsOf result — Insert vectors in a new KG transaction using its open `t`; reject conflicting replacements and enforce date/immutability guards. Test backfill without an intervening graph write.

10. HIGH — [phase2/design.md:151](phase2/design.md:151) — Each public Match opens its own snapshot; the proposed evaluator can join incompatible committed states — Pass one querier through the entire operation, including saved-result evaluation/checkpoint capture; use `w.q` inside Materialize. Add concurrent-writer and single-connection tests.

11. HIGH — [phase2/design.md:274](phase2/design.md:274) — Retractions invalidate only positive dependencies. Removing a blocker from NOT EXISTS can create rows while an empty saved answer remains fresh — Conservatively invalidate relevant negative/optional/aggregate queries on retractions too; test deletion that changes an empty result.

12. HIGH — [phase2/design.md:268](phase2/design.md:268) — Saved-answer dependencies omit implicit graph-membership reads; adding/removing membership changes results without touching matched fact eids or predicates — Record membership dependencies and implicit predicate reads, persist graph scope, and test membership-only invalidation.

13. HIGH — [phase2/design.md:228](phase2/design.md:228) — Keeping only the first proof’s interval loses valid periods supplied by alternative proofs and breaks downstream derivation — Propagate interval sets with proof-specific supports; materialize separate support fragments. Test disjoint/adjacent proofs and reordered input.

14. HIGH — [phase2/design.md:240](phase2/design.md:240) — Reconciliation is program-global although Materialize accepts restricted views; rerunning on g1 retracts still-supported g2 conclusions — Restrict reconciliation to full current views initially, or track reconciliation domains and preserve outside fragments; test successive graph/time scopes.

15. HIGH — [phase2/design.md:180](phase2/design.md:180) — SPARQL and Cypher require different semantics: duplicate eids count once versus twice, and Cypher forbids reusing one relationship within a MATCH — Add dialect settings for triple sets, eid bags, relationship isomorphism and missing-value joins; test expected differences.

16. MEDIUM — [phase2/design.md:154](phase2/design.md:154) — Treating unknown comparisons as false breaks negation: `!(?age > 30)` incorrectly accepts missing age — Preserve unknown through Boolean evaluation and reject it only at filter boundaries; add full truth-table tests.

17. MEDIUM — [phase2/design.md:194](phase2/design.md:194) — Time-respecting paths incorrectly require ordered interval starts and overlap; upstream permits earlier-start edges and waiting — Carry arrival τ, require `v_to > τ`, then advance to `max(τ,v_from)`; test `[1,5)→[0,2)` and `[1,2)→[5,6)`.

18. MEDIUM — [phase2/design.md:202](phase2/design.md:202) — Per-node visited state loses qualifying minimum-hop paths: A→B suppresses A→C→B when `MinHops=2` — Include the remaining minimum-hop requirement in visited state; test early arrivals below the minimum.

19. MEDIUM — [phase2/design.md:116](phase2/design.md:116) — Bundle closure omits object-side dependents and downward subject references, unlike upstream — Start with visible Dependents(root), then close over both endpoint references without expanding dependents of reference-only members; test supporting beliefs and annotation roots.

20. MEDIUM — [phase2/design.md:124](phase2/design.md:124) — Importing included `sys:InGraph` statements through Assert fails reserved-predicate checks; lowercasing instead silently loses membership — Route memberships through AddToGraph, preserve their eid mappings, and reject other reserved members before writing; test membership round trips.

21. MEDIUM — [kg_view.go:180](internal/palace/kg_view.go:180) — AsOf rows expose later `TRet`, RetKind and RetractedAt values, so historical rows change after future retractions — Mask retraction metadata beyond the view’s transaction; test complete historical row equality before/after later writes.

22. MEDIUM — [phase2/design.md:272](phase2/design.md:272) — Future AsOf bounds are classified as permanently fixed, although subsequent commits below that bound change results — Freeze the bound to the save snapshot or keep invalidating until the requested transaction exists; test `AsOf(last_t+1)`.

23. MEDIUM — [phase2/plan.md:40](phase2/plan.md:40) — Privacy tests never inspect embedder requests, despite decoded `Value.String` rendering conflicting with SEALED exclusion — Filter using stored tags before rendering; add a spy embedder covering live sealed values, erased values and concurrent erasure.

24. MEDIUM — [phase2/plan.md:26](phase2/plan.md:26) — Budget tests omit MaxBytes and shared accounting across composite lookups — Add oversized-literal tests and sibling lookups individually below the limit but collectively above it; require one operation-wide budget.

25. MEDIUM — [phase2/plan.md:37](phase2/plan.md:37) — Vector validation/conflict tests are absent, leaving model-dimension mismatches, non-finite/zero vectors and historical overwrites unspecified — Require dimension consistency, finite nonzero cosine inputs, identical-write no-ops and conflicting-write rejection; test each through PutVector.
