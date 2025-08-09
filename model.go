package main

import (
    "database/sql"
    "fmt"
    "sort"
    "strings"
)

type model struct {
    db              *sql.DB
    allTables       []string
    tables          []string
    cursor          int
    preview         [][]string
    previewColumns  []string
    tableCols       []colInfo
    previewRowIDs   []int64
    status          string
    width           int
    height          int
    searchActive    bool
    searchQuery     string
    focusPreview    bool
    selRow          int
    selCol          int
    // inline cell edit state
    editingActive   bool
    editBuffer      string
    // table deletion confirm state
    confirmDeleteActive bool
    confirmDeleteTarget string
    confirmDeleteType   string // "table" or "view"
    // AI prompt state
    aiPromptActive  bool
    aiPromptText    string
    aiThinking      bool
    aiOutput        string
}

type colInfo struct {
    Name    string
    Type    string
    PKOrder int // 0 means not PK; 1..N for composite order
    NotNull bool
    Default sql.NullString
}

type uniqueIndex struct {
    Name    string
    Columns []string
}

func initialModel() model {
    db, err := openDB()
    m := model{db: db, status: ""}
    if err != nil {
        m.status = fmt.Sprintf("db open error: %v", err)
        return m
    }
    tables, err := listTables(db)
    if err != nil {
        m.status = fmt.Sprintf("list tables error: %v", err)
    }
    sort.Strings(tables)
    m.allTables = tables
    m.applyFilter()
    return m
}

func (m *model) refreshPreview() {
    m.preview = nil
    m.previewColumns = nil
    m.previewRowIDs = nil
    // Load table info for PK detection
    m.tableCols = nil
    if len(m.tables) > 0 && m.cursor >= 0 && m.cursor < len(m.tables) && m.db != nil {
        if ti, err := getTableInfo(m.db, m.tables[m.cursor]); err == nil {
            m.tableCols = ti
        } else {
            m.status = fmt.Sprintf("table info error: %v", err)
        }
    }
    if m.db == nil || len(m.tables) == 0 || m.cursor < 0 || m.cursor >= len(m.tables) {
        return
    }
    tbl := m.tables[m.cursor]
    // Preview: limit 10 rows; include rowid if no explicit PK present
    needsRowid := !hasExplicitPK(m.tableCols)
    q := ""
    if needsRowid {
        q = fmt.Sprintf("SELECT rowid, * FROM %s LIMIT 10", quoteIdent(tbl))
    } else {
        q = fmt.Sprintf("SELECT * FROM %s LIMIT 10", quoteIdent(tbl))
    }
    rows, err := m.db.Query(q)
    if err != nil {
        m.status = fmt.Sprintf("preview error: %v", err)
        return
    }
    defer rows.Close()

    cols, err := rows.Columns()
    if err != nil {
        m.status = fmt.Sprintf("columns error: %v", err)
        return
    }
    if needsRowid && len(cols) > 0 && strings.EqualFold(cols[0], "rowid") {
        m.previewColumns = cols[1:]
    } else {
        m.previewColumns = cols
        needsRowid = false // defensive
    }

    for rows.Next() {
        raw := make([]any, len(cols))
        dest := make([]any, len(cols))
        for i := range raw {
            dest[i] = &raw[i]
        }
        if err := rows.Scan(dest...); err != nil {
            m.status = fmt.Sprintf("scan error: %v", err)
            return
        }
        start := 0
        if needsRowid {
            // capture rowid and skip it in display
            m.previewRowIDs = append(m.previewRowIDs, asInt64(raw[0]))
            start = 1
        }
        rec := make([]string, len(cols)-start)
        for i := start; i < len(raw); i++ {
            rec[i-start] = formatValue(raw[i])
        }
        m.preview = append(m.preview, rec)
    }
    if err := rows.Err(); err != nil {
        m.status = fmt.Sprintf("rows error: %v", err)
    }
    // Clamp selection indexes
    if m.selRow >= len(m.preview) {
        m.selRow = max(0, len(m.preview)-1)
    }
    if m.selCol >= len(m.previewColumns) {
        m.selCol = max(0, len(m.previewColumns)-1)
    }
}

func (m *model) applyFilter() {
    if m.searchQuery == "" {
        // show all
        m.tables = append([]string(nil), m.allTables...)
    } else {
        q := strings.ToLower(m.searchQuery)
        filtered := make([]string, 0, len(m.allTables))
        for _, t := range m.allTables {
            if strings.Contains(strings.ToLower(t), q) {
                filtered = append(filtered, t)
            }
        }
        m.tables = filtered
    }
    if m.cursor >= len(m.tables) {
        m.cursor = max(0, len(m.tables)-1)
    }
    if m.cursor < 0 {
        m.cursor = 0
    }
    m.refreshPreview()
}

func hasExplicitPK(cols []colInfo) bool {
    for _, c := range cols {
        if c.PKOrder > 0 {
            return true
        }
    }
    return false
}
