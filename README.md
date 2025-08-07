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
# optionally point to a different db
# export DB_PATH=/absolute/path/to/neverlost.db

go run .
```

## Keybindings
- j / down: move down
- k / up: move up
- r: reload table list
- q / ctrl+c: quit

## Notes
- Shows tables and views. Preview shows up to 10 rows, truncates long cells.
- Uses `modernc.org/sqlite` (pure Go driver), no CGO needed.

