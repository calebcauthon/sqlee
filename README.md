# tui-sql

Simple Bubble Tea TUI to browse the SQLite DB at `instance/neverlost.db`.

## Requirements
- Go 1.22+

On macOS (Homebrew):

```sh
brew install go
```

## Run

From this directory:

```sh
# 1) Pass a DB path explicitly (highest priority)
go run . ./path/to/your.db

# 2) Or via environment variable
DB_PATH=./path/to/your.db go run .

# 3) Or rely on auto-discovery
#    Picks the newest *.db in the current directory; if none, the newest in ./instance/
go run .
```

### DB path resolution (priority order)
- **CLI arg**: `go run . <db path>`
- **Env var**: `DB_PATH=/path/to/db.sqlite`
- **Auto-discovery (cwd)**: newest `*.db` in the current directory
- **Auto-discovery (instance/)**: newest `*.db` in `instance/`
- **If none found**: prints a helpful message and exits with status code 2

## Keybindings
- j / down: move down
- k / up: move up
- r: reload table list
- q / ctrl+c: quit

## Notes
- Shows tables and views. Preview shows up to 10 rows, truncates long cells.
- Uses `modernc.org/sqlite` (pure Go driver), no CGO needed.

