# TODO

Loosely ordered by impact. Items are independent unless noted.

## High — feature gaps blocking real-world use

- [ ] **`ALTER TABLE` support** — parse `ADD COLUMN`, `DROP COLUMN`,
  `ALTER COLUMN ... TYPE` so the schema reflects migration files.
  Without this, projects that store DDL as a sequence of migrations
  see incomplete schemas.
- [ ] **`CREATE VIEW` recognition** — treat views as queryable tables
  for completion, hover, and diagnostics. Today view references are
  reported as `unknown table`.
- [ ] **Collect SELECT-list aliases** — `SELECT id AS user_id FROM
  users ORDER BY user_id`. Needed before unqualified column
  diagnostics (below) can land without false positives.

## Medium — known false positives / negatives

- [ ] **CTE column extraction** — parse the SELECT list of each CTE
  body so `WITH cte AS (SELECT id, name FROM users) SELECT cte.<TAB>`
  offers `id` and `name`. Currently `cte.anything` is silently
  accepted because the CTE body isn't analysed.
- [ ] **Subquery scope isolation** — aliases declared inside a
  subquery currently leak to the outer query (false negatives).
  Switch the alias map to a stack instead of a flat map.
- [ ] **Self-join completions** — `FROM users u1, users u2` only
  yields one column set. `Context.FromTables` dedupes by real table
  name; track each FROM occurrence separately to fix.
- [ ] **Unqualified column diagnostics** — currently disabled to
  avoid false positives from SELECT-list aliases / CTE columns /
  outer-scope refs. Re-enable once the two items above land.

## Medium — additional LSP features

- [ ] **Goto-definition** — jump from a table reference to its
  `CREATE TABLE` line in the schema directory.
- [ ] **Document symbols** — outline of `CREATE TABLE` statements in
  `.sql` files for the editor's outline pane.
- [ ] **Snippet-style completion** — emit `SELECT $1 FROM $2` instead
  of plain text for top-level templates.
- [ ] **LSP 3.17 `PositionEncodingKind` negotiation** — declare and
  honour `utf-8` when the client supports it. Requires upgrading
  glsp to 3.17 or maintaining a small fork.

## Medium — distribution

- [ ] **GitHub Actions release workflow** — cross-compile for
  `linux|darwin|windows` × `amd64|arm64`, attach to `gh release`.
- [ ] **VSCode extension** — minimal `vscode-languageclient` wrapper
  in a sibling repo (`pgls-vscode`).
- [ ] **Build-time version stamping** — replace the hard-coded
  `version = "0.0.1"` with `-ldflags "-X .../version=$TAG"`.

## Low — quality / internals

- [ ] **Go-level tests for `internal/lsp`** — current verification is
  Python-driven E2E; faster ones could call the glsp Handler
  directly.
- [ ] **AST-based `sqlctx`** — switch from token-walker to
  `pg_query.Parse` AST traversal where possible (still falling back
  to the lexer for incomplete SQL during editing).
- [ ] **`schema/` edge cases** — array types (`bigint[]`), ENUMs,
  generated columns, `CHECK` constraints, schema-qualified table
  names from DDL.

## Low — PostgreSQL surface

- [ ] **JSON / containment operators** — verify that `->`, `->>`,
  `#>`, `#>>`, `@>`, `<@` are tokenised correctly by `pg_query.Scan`
  and don't disrupt the analyser.
- [ ] **Multi-statement SQL files** — split on top-level `;` and
  analyse each statement independently. Today the whole file is
  one analysis unit; this hides issues across statements.
- [ ] **Workspace auto-detection** — when no `schemaDir` is
  configured, look for `db/schema/`, `migrations/`, `sql/` in the
  workspace root.
