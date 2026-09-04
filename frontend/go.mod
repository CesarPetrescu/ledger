// Marks the frontend tree as a separate, empty Go module so that Go tooling
// invoked with ./... from the repository root does not descend into
// node_modules (some npm packages ship Go sources).
module github.com/cesarpetrescu/ledger/frontend

go 1.25.0
