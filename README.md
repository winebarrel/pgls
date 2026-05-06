# pgls

A PostgreSQL language server. Provides completion, hover, and
diagnostics for SQL — both inside `.sql` files and inside SQL string
literals embedded in `.go` source.

Schema is read from a directory of `CREATE TABLE` files using
[libpg_query](https://github.com/pganalyze/libpg_query) (via
[wasilibs/go-pgquery](https://github.com/wasilibs/go-pgquery), so no
CGO is required).

<img src="https://github.com/user-attachments/assets/f5334ec5-f3d7-427b-aee3-2d0be91347df" />

## Install

### Pre-built binaries

Download an archive for your platform from the
[releases page](https://github.com/winebarrel/pgls/releases),
extract it, and drop `pgls` somewhere on your `$PATH`. Builds
are produced for darwin / linux / windows × amd64 / arm64.

### From source

Requires Go 1.25 or later:

```sh
go install github.com/winebarrel/pgls@v0.3.1
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

If the editor doesn't expose a clean way to pass `initializationOptions`
(classic Vim, plain CLI), drop a `.pgls.json` at the workspace root and
pgls will pick it up automatically:

```json
{
  "schemaDir": "db/schema",
  "sqlFunctions": [
    { "name": "Query",        "argIndex": 0 },
    { "name": "QueryContext", "argIndex": 1 },
    { "name": "Get",          "argIndex": 1 },
    { "name": "Select",       "argIndex": 1 }
  ]
}
```

Each entry names a Go function or method (matched by selector, no
type resolution) and `argIndex` (0-origin) tells pgls which positional
argument carries the SQL string. **Both `name` and `argIndex` are
required per entry** — a missing `argIndex` is rejected at validation
time rather than silently defaulting to 0 (which would be wrong for
`*Context` methods, where the SQL lives at arg 1). Method names are
matched without a receiver, so `"Query"` covers `db.Query`, `tx.Query`,
`*sql.DB.Query` all together.

`sqlFunctions` itself is optional: omit the whole field to inherit
the default `database/sql` set (`Query`, `QueryRow`, `Exec`,
`Prepare` at arg 0, plus their `*Context` variants at arg 1), or set
it to `[]` to disable function-call detection so only marker comments
fire. Per-entry omission of `argIndex` is **not** the way to express
either — use one of those two switches instead.

`.pgls.json` is the project's authoritative schema location and
wins over `initializationOptions` when both are present —
editors can't accidentally point a colleague's pgls at the wrong
directory by leaking a stale per-machine setting. Use
`initializationOptions.schemaDir` only as an ad-hoc override for
projects that don't ship a `.pgls.json`.

The `schemaDir` field of `.pgls.json` must resolve to a path inside
the workspace — absolute paths and `..` escapes are rejected, so
cloning an unfamiliar repo can't make pgls walk arbitrary `.sql`
files elsewhere on disk. The `-schema` CLI flag stays unrestricted
because the user supplies it explicitly.

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

### Vim (with [vim-lsp](https://github.com/prabirshrestha/vim-lsp))

```vim
if executable('pgls')
  augroup pgls_register
    autocmd!
    autocmd User lsp_setup call lsp#register_server({
      \ 'name': 'pgls',
      \ 'cmd': {server_info -> ['pgls']},
      \ 'allowlist': ['go', 'sql'],
      \ 'initialization_options': { 'schemaDir': 'db/schema' },
      \ 'root_uri': {server_info -> lsp#utils#path_to_uri(
      \   lsp#utils#find_nearest_parent_file_directory(
      \     lsp#utils#get_buffer_path(), ['.git/', 'go.mod']))},
      \ })
  augroup END
endif
```

For `coc.nvim` add an entry to `:CocConfig`:

```json
{
  "languageserver": {
    "pgls": {
      "command": "pgls",
      "filetypes": ["go", "sql"],
      "rootPatterns": ["go.mod", ".git/"],
      "initializationOptions": { "schemaDir": "db/schema" }
    }
  }
}
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

Install [pgls](https://marketplace.visualstudio.com/items?itemName=winebarrel.pgls-vscode)
from the VS Code Marketplace
([source](https://github.com/winebarrel/pgls-vscode)). The extension
spawns the `pgls` binary on `.go` and `.sql` files; configure the
schema directory per-workspace in `.vscode/settings.json`:

```json
{ "pgls.schemaDir": "db/schema" }
```

## Features

- **Completion** — table and column names, scoped to the cursor's
  SQL clause:
  - after `FROM`/`JOIN`/`INTO`/`UPDATE`: tables only
  - after `SELECT`/`WHERE`/`SET`/`ON`: columns from FROM-tables
  - `alias.` resolves the alias and offers that table's columns only
- **Goto-definition** — jump from a table reference to its
  `CREATE TABLE` line, or from a qualified column (`u.email`) to the
  column's row in the DDL. Aliases are resolved to the underlying
  table.
- **Hover** — markdown summary of the identifier under the cursor:
  table layout for tables, `table.column \`type\`` for columns,
  alias resolution for `u.email`-style references.
- **Diagnostics** — flags `FROM`/`JOIN` references to unknown
  tables, qualifiers that resolve to neither a table nor an alias,
  and qualified columns missing from the resolved table.
- **Go-aware** — inside `.go` files, pgls treats a string literal as
  SQL when one of:
  1. It carries a JetBrains-style `language=sql` (or
     `language=postgresql`) marker comment on the line directly above.
     Block-comment form (`/* language=sql */`) and any case works.
  2. It's passed to a recognized SQL method. Defaults cover
     `database/sql` (`Query`, `QueryRow`, `QueryContext`,
     `QueryRowContext`, `Exec`, `ExecContext`, `Prepare`,
     `PrepareContext`); override with `sqlFunctions` in `.pgls.json`
     or `initializationOptions` to add your own (e.g. sqlx's `Get` /
     `Select` / `NamedExec`) or set it to `[]` to disable function-call
     detection entirely.

  ```go
  // marker form
  // language=sql
  q := `SELECT id, email FROM users WHERE id = $1`

  // function-call form (no comment needed)
  rows, err := db.Query(`SELECT id, email FROM users`)
  ```

  Without either, pgls leaves the string alone — no completion, no
  diagnostics — so non-SQL strings never get false hits.
- **Hot reload** — the schema directory is watched; editing or adding
  `.sql` files triggers a reload (debounced 200 ms) and republishes
  diagnostics for all open documents.

## Limitations

- **CTE / subquery columns are not validated** — names introduced by
  `WITH` or `(SELECT ...) alias` are recognized as visible tables (so
  `FROM cte` and `cte.foo` don't false-flag), but `cte.foo` is silently
  accepted because pgls does not analyze the CTE body to extract its
  output column list. Inner unknown-table typos (`WITH a AS (SELECT *
  FROM nope)`) are still flagged.
- **Scope leakage between subqueries** — aliases defined inside a
  subquery are visible from the outer query. A v1 trade-off: yields
  occasional false negatives, never false positives.
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
