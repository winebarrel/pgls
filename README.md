# pgls

A PostgreSQL language server. Provides completion, hover, and
diagnostics for SQL — both inside `.sql` files and inside SQL string
literals embedded in `.go` source.

Schema is read from a directory of `CREATE TABLE` files using
[libpg_query](https://github.com/pganalyze/libpg_query) (via
[wasilibs/go-pgquery](https://github.com/wasilibs/go-pgquery), so no
CGO is required).

## Install

```sh
go install github.com/winebarrel/pgls@latest
```

## Quickstart

Point pgls at a directory containing your DDL:

```sh
pgls -schema ./db/schema
```

Or, when launched from an editor, configure it via LSP
`initializationOptions` (relative paths resolve against the workspace
root):

```json
{ "initializationOptions": { "schemaDir": "db/schema" } }
```

## Editor setup

### Neovim (built-in LSP)

```lua
vim.lsp.start({
  name = 'pgls',
  cmd = { 'pgls' },
  root_dir = vim.fn.getcwd(),
  init_options = { schemaDir = 'db/schema' },
  filetypes = { 'go', 'sql' },
})
```

### Helix (`languages.toml`)

```toml
[language-server.pgls]
command = "pgls"
config = { schemaDir = "db/schema" }

[[language]]
name = "go"
language-servers = ["gopls", "pgls"]

[[language]]
name = "sql"
language-servers = ["pgls"]
```

### VSCode

Launch via a thin extension that wraps `vscode-languageclient`:

```js
const client = new LanguageClient('pgls', 'pgls', {
  run: { command: 'pgls' },
  debug: { command: 'pgls' },
}, {
  documentSelector: [{ language: 'go' }, { language: 'sql' }],
  initializationOptions: { schemaDir: 'db/schema' },
});
client.start();
```

## Features

- **Completion** — table and column names, scoped to the cursor's
  SQL clause:
  - after `FROM`/`JOIN`/`INTO`/`UPDATE`: tables only
  - after `SELECT`/`WHERE`/`SET`/`ON`: columns from FROM-tables
  - `alias.` resolves the alias and offers that table's columns only
- **Hover** — markdown summary of the identifier under the cursor:
  table layout for tables, `table.column \`type\`` for columns,
  alias resolution for `u.email`-style references.
- **Diagnostics** — flags `FROM`/`JOIN` references to unknown
  tables, qualifiers that resolve to neither a table nor an alias,
  and qualified columns missing from the resolved table.
- **Go-aware** — inside `.go` files, all of the above only fires
  when the cursor sits inside a string literal whose contents look
  like SQL (begins with `SELECT`/`INSERT`/`UPDATE`/...).

## Limitations

- **CTEs / subqueries** are not yet scoped — `WITH cte AS (...)
  SELECT * FROM cte` reports `cte` as an unknown table.
- **`ALTER TABLE` is not parsed** — only `CREATE TABLE` contributes
  to the schema.
- **Unqualified column references are not validated** — too many
  false positives from SELECT-list aliases, CTE columns, and
  function arguments. Qualified references (`u.email`) are.
- **LSP 3.17 `PositionEncodingKind` negotiation is not implemented**
  — pgls assumes UTF-16, the LSP default.

## Development

```sh
go build ./...
go test ./...
go test -bench . ./internal/sqlctx/
```

## License

MIT
