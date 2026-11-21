# Rough Idea: port Tiramemsu's memory model into the palace knowledge graph

Source: `tmp/tiramemsu` (Rust, 0.12.0, about 95k lines without tests). Read-only reference mount.

The palace knowledge graph (`internal/palace/kg.go`) is a plain `triples` table:
one row per `(subject, predicate, object)` with `valid_from`/`valid_to` text
columns. It cannot say how sure the agent is, where a fact came from, what a
conclusion rests on, or what the agent believed before a correction.
`Invalidate` overwrites `valid_to` in place, so the old state is lost.

Tiramemsu solves exactly this for agents: every fact is a statement with its own
id, so facts about facts (confidence, source, beliefs) are ordinary statements,
and every change is kept with when it was made (transaction time) and when it
was true (valid time). SQLite triggers make "never forget" a property of the
file.

The goal is not to port Tiramemsu. It is to port the parts of its **core data
model and memory verbs** that make agent memory trustworthy, in pure Go, on the
`modernc.org/sqlite` palace database pi-go already ships, and to keep every
existing palace KG caller (tools, `pi memory kg` CLI, status) working.

Out of scope, because each is a project of its own and the agent works through
typed tools rather than a query language: SPARQL, openCypher, Datalog, N3, ELPI,
the redb engine, WASM, saved answers, statement vectors, crypto-shredding.
