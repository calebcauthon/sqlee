package main

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "strings"
)

func openDB() (*sql.DB, error) {
    // Use modernc.org/sqlite (pure Go) so user doesn't need CGO
    path := resolveDBPath()
    if path == "" {
        return nil, fmt.Errorf("no SQLite .db file found. Provide a path: 'go run . <db path>' or set DB_PATH, or place a .db in current directory or in 'instance/'")
    }
    return sql.Open("sqlite", path)
}

func resolveDBPath() string {
    // 1) CLI arg: go run . <db path>
    if len(os.Args) > 1 && os.Args[1] != "" {
        return os.Args[1]
    }
    // 2) Env override
    if p := os.Getenv("DB_PATH"); p != "" {
        return p
    }
    // 3) Auto-discover newest .db in current working directory
    if p, ok := newestDBInDir("."); ok {
        return p
    }
    // 4) Otherwise, look under instance/
    if p, ok := newestDBInDir("instance"); ok {
        return p
    }
    // 5) Nothing found
    return ""
}

// newestDBInDir returns the most recently modified .db file in dir, not recursive.
// It returns (path, true) if found, otherwise ("", false).
func newestDBInDir(dir string) (string, bool) {
    entries, err := os.ReadDir(dir)
    if err != nil {
        return "", false
    }
    var newestPath string
    var newestModNano int64
    for _, entry := range entries {
        if entry.IsDir() { continue }
        name := entry.Name()
        if !strings.HasSuffix(strings.ToLower(name), ".db") {
            continue
        }
        info, err := entry.Info()
        if err != nil {
            continue
        }
        mod := info.ModTime().UnixNano()
        if newestPath == "" || mod > newestModNano {
            newestPath = filepath.Join(dir, name)
            newestModNano = mod
        }
    }
    if newestPath == "" {
        return "", false
    }
    return newestPath, true
}

func listTables(db *sql.DB) ([]string, error) {
    rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []string
    for rows.Next() {
        var name string
        if err := rows.Scan(&name); err != nil {
            return nil, err
        }
        out = append(out, name)
    }
    return out, rows.Err()
}

// getTableInfo returns column info for the given table
func getTableInfo(db *sql.DB, table string) ([]colInfo, error) {
    q := fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table))
    rows, err := db.Query(q)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []colInfo
    for rows.Next() {
        // cid, name, type, notnull, dflt_value, pk
        var cid int
        var name, ctype string
        var notnull int
        var dflt sql.NullString
        var pk int
        if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
            return nil, err
        }
        out = append(out, colInfo{Name: name, Type: ctype, PKOrder: pk, NotNull: notnull == 1, Default: dflt})
    }
    return out, rows.Err()
}

func getUniqueIndexes(db *sql.DB, table string) ([]uniqueIndex, error) {
    q := fmt.Sprintf("PRAGMA index_list(%s)", quoteIdent(table))
    rows, err := db.Query(q)
    if err != nil { return nil, err }
    defer rows.Close()
    type idxRow struct{ name string; unique int; origin string }
    var idxs []idxRow
    for rows.Next() {
        var seq int
        var name string
        var unique int
        var origin string
        var partial int
        if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil { return nil, err }
        if unique == 1 { idxs = append(idxs, idxRow{name: name, unique: unique, origin: origin}) }
    }
    if err := rows.Err(); err != nil { return nil, err }
    // For each index, get cols
    out := make([]uniqueIndex, 0, len(idxs))
    for _, ix := range idxs {
        // skip implicit PK unique index if any
        if strings.EqualFold(ix.origin, "pk") { continue }
        qi := fmt.Sprintf("PRAGMA index_info(%s)", quoteIdent(ix.name))
        r2, err := db.Query(qi)
        if err != nil { return nil, err }
        var cols []string
        for r2.Next() {
            var seqno, cid int
            var cname string
            if err := r2.Scan(&seqno, &cid, &cname); err != nil { r2.Close(); return nil, err }
            if cname != "" { cols = append(cols, cname) }
        }
        r2.Close()
        if len(cols) > 0 { out = append(out, uniqueIndex{Name: ix.name, Columns: cols}) }
    }
    return out, nil
}

func getObjectType(db *sql.DB, name string) (string, error) {
    var typ string
    err := db.QueryRow(`SELECT type FROM sqlite_schema WHERE name = ? LIMIT 1`, name).Scan(&typ)
    if err != nil { return "", err }
    if typ != "table" && typ != "view" { typ = "table" }
    return typ, nil
}

