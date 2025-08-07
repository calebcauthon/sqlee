package main

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "runtime"
    "strconv"
    "sort"
    "strings"
    "os/exec"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/google/uuid"
    _ "modernc.org/sqlite"
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
}

type colInfo struct {
    Name    string
    Type    string
    PKOrder int // 0 means not PK; 1..N for composite order
}

type uniqueIndex struct {
    Name    string
    Columns []string
}

func openDB() (*sql.DB, error) {
    // Use modernc.org/sqlite (pure Go) so user doesn't need CGO
    path := resolveDBPath()
    return sql.Open("sqlite", path)
}

func resolveDBPath() string {
    if p := os.Getenv("DB_PATH"); p != "" {
        return p
    }
    // Try common locations relative to executable and cwd
    candidates := []string{
        "instance/neverlost.db",
        "../instance/neverlost.db",
        // Absolute path for this workspace for convenience
        "/Users/caleb/Code/neverlost_api/instance/neverlost.db",
    }
    if exe, err := os.Executable(); err == nil {
        base := filepath.Dir(exe)
        candidates = append(candidates,
            filepath.Join(base, "instance/neverlost.db"),
            filepath.Join(base, "../instance/neverlost.db"),
        )
    }
    for _, c := range candidates {
        if _, err := os.Stat(c); err == nil {
            return c
        }
    }
    // Fallback
    return "instance/neverlost.db"
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

func truncateCell(s string, max int) string {
    if max <= 0 {
        return ""
    }
    if len(s) <= max {
        return s
    }
    if max <= 1 {
        return s[:max]
    }
    return s[:max-1] + "…"
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

func quoteIdent(id string) string {
    // Basic quoting for identifiers; double any embedded quotes.
    return "\"" + strings.ReplaceAll(id, "\"", "\"\"") + "\""
}

func formatValue(v any) string {
    if v == nil {
        return "NULL"
    }
    switch t := v.(type) {
    case []byte:
        // Try interpret as UTF-8 text; otherwise show hex length
        s := string(t)
        if isMostlyPrintable(s) {
            return s
        }
        return fmt.Sprintf("<blob %dB>", len(t))
    default:
        return fmt.Sprint(t)
    }
}

func asInt64(v any) int64 {
    switch t := v.(type) {
    case int64:
        return t
    case int32:
        return int64(t)
    case int:
        return int64(t)
    case []byte:
        if n, err := strconv.ParseInt(string(t), 10, 64); err == nil {
            return n
        }
    case string:
        if n, err := strconv.ParseInt(t, 10, 64); err == nil {
            return n
        }
    }
    return 0
}

func isMostlyPrintable(s string) bool {
    printable := 0
    for _, r := range s {
        if r == '\n' || r == '\t' || r == '\r' || (r >= 32 && r < 127) || r >= 128 {
            printable++
        }
    }
    return float64(printable) >= 0.9*float64(len([]rune(s)))
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        // If currently searching, handle input editing first
        if m.searchActive {
            switch msg.Type {
            case tea.KeyRunes:
                if m.focusPreview {
                    return m, nil
                }
                if len(msg.Runes) > 0 {
                    m.searchQuery += string(msg.Runes)
                    m.applyFilter()
                }
                return m, nil
            case tea.KeyBackspace:
                if m.focusPreview {
                    return m, nil
                }
                r := []rune(m.searchQuery)
                if len(r) > 0 {
                    m.searchQuery = string(r[:len(r)-1])
                    m.applyFilter()
                }
                return m, nil
            case tea.KeyEnter:
                m.searchActive = false
                return m, nil
            case tea.KeyEsc:
                m.searchActive = false
                if m.searchQuery != "" {
                    m.searchQuery = ""
                    m.applyFilter()
                }
                return m, nil
            }
            // allow navigation and quitting while in search
            switch msg.String() {
            case "ctrl+c", "q":
                if m.db != nil {
                    _ = m.db.Close()
                }
                return m, tea.Quit
            case "up", "k":
                if !m.focusPreview {
                    if m.cursor > 0 { m.cursor--; m.refreshPreview() }
                } else {
                    if m.selRow > 0 { m.selRow-- }
                }
                return m, nil
            case "down", "j":
                if !m.focusPreview {
                    if m.cursor < len(m.tables)-1 { m.cursor++; m.refreshPreview() }
                } else {
                    if m.selRow+1 < len(m.preview) { m.selRow++ }
                }
                return m, nil
            case "left":
                if m.focusPreview {
                    if m.selCol > 0 { m.selCol-- } else { m.focusPreview = false }
                }
                return m, nil
            case "right":
                if !m.focusPreview {
                    m.focusPreview = true
                    m.searchActive = false
                    if m.selRow >= len(m.preview) { m.selRow = 0 }
                    if m.selCol >= len(m.previewColumns) { m.selCol = 0 }
                } else if m.selCol+1 < len(m.previewColumns) {
                    m.selCol++
                }
                return m, nil
            }
            return m, nil
        }
        switch msg.String() {
        case "ctrl+c", "q":
            if m.db != nil {
                _ = m.db.Close()
            }
            return m, tea.Quit
        case "left", "h":
            if m.focusPreview {
                if msg.String() == "h" {
                    // treat like left as well
                }
                if m.selCol > 0 {
                    m.selCol--
                } else {
                    m.focusPreview = false
                }
            }
        case "right", "l":
            if !m.focusPreview {
                m.focusPreview = true
                if m.selRow >= len(m.preview) { m.selRow = 0 }
                if m.selCol >= len(m.previewColumns) { m.selCol = 0 }
            } else if m.selCol+1 < len(m.previewColumns) {
                m.selCol++
            }
        case "/":
            if !m.focusPreview {
                m.searchActive = true
            }
            return m, nil
        case "up", "k":
            if !m.focusPreview {
                if m.cursor > 0 { m.cursor--; m.refreshPreview() }
            } else {
                if m.selRow > 0 { m.selRow-- }
            }
        case "down", "j":
            if !m.focusPreview {
                if m.cursor < len(m.tables)-1 { m.cursor++; m.refreshPreview() }
            } else {
                if m.selRow+1 < len(m.preview) { m.selRow++ }
            }
        case "y":
            if m.focusPreview && m.selRow >= 0 && m.selRow < len(m.preview) && m.selCol >= 0 && m.selCol < len(m.previewColumns) {
                val := ""
                if len(m.preview) > 0 {
                    row := m.preview[m.selRow]
                    if m.selCol < len(row) {
                        val = row[m.selCol]
                    }
                }
                if err := copyToClipboard(val); err != nil {
                    m.status = fmt.Sprintf("copy error: %v", err)
                } else {
                    m.status = "copied"
                }
            }
        case "i":
            if m.focusPreview {
                if err := m.duplicateCurrentRow(); err != nil {
                    m.status = fmt.Sprintf("insert error: %v", err)
                } else {
                    m.status = "inserted duplicate row"
                    m.refreshPreview()
                }
            }
        case "r":
            // reload tables
            if m.db != nil {
                t, err := listTables(m.db)
                if err != nil {
                    m.status = fmt.Sprintf("reload error: %v", err)
                } else {
                    sort.Strings(t)
                    m.allTables = t
                    m.applyFilter()
                }
            }
        }
    case tea.WindowSizeMsg:
        m.width = msg.Width
        m.height = msg.Height
    }
    return m, nil
}

func max(a, b int) int { if a > b { return a }; return b }

func (m model) View() string {
    if m.db == nil {
        return fmt.Sprintf("DB not open. %s\n", m.status)
    }

    // Layout: left column for tables, right column for preview.
    leftWidth := 30
    if m.width > 0 && m.width < 80 {
        leftWidth = m.width / 3
    }
    rightWidth := 80
    if m.width > 0 {
        rightWidth = m.width - leftWidth - 3
        if rightWidth < 20 {
            rightWidth = 20
        }
    }

    // Render tables list
    var left strings.Builder
    left.WriteString("Tables (j/k or ↓/↑, → to preview, / search, r reload, q quit)\n")
    if m.searchActive || m.searchQuery != "" {
        left.WriteString("/" + m.searchQuery + "\n")
    }
    for i, t := range m.tables {
        cursor := "  "
        if i == m.cursor && !m.focusPreview {
            cursor = "> "
        }
        // truncate table name to leftWidth-2
        name := truncateCell(t, max(1, leftWidth-2))
        left.WriteString(fmt.Sprintf("%s%s\n", cursor, name))
    }

    // Render preview table
    var right strings.Builder
    if len(m.tables) == 0 {
        right.WriteString("No tables found.\n")
    } else {
        title := fmt.Sprintf("Preview: %s (up to 10 rows)", m.tables[m.cursor])
        if m.focusPreview { title += " [FOCUS]" }
        right.WriteString(title + "\n")
        if len(m.previewColumns) > 0 {
            // compute column widths based on available rightWidth
            colWidths := computeColumnWidths(m.previewColumns, m.preview, rightWidth)
            // header
            for i, c := range m.previewColumns {
                header := c
                if m.focusPreview && i == m.selCol {
                    header = "*" + header
                }
                cell := padRight(truncateCell(header, colWidths[i]), colWidths[i])
                right.WriteString(cell)
                if i < len(m.previewColumns)-1 {
                    right.WriteString(" ")
                }
            }
            right.WriteString("\n")
            // separator
            for i := range m.previewColumns {
                right.WriteString(strings.Repeat("-", colWidths[i]))
                if i < len(m.previewColumns)-1 {
                    right.WriteString(" ")
                }
            }
            right.WriteString("\n")
            // rows
            for ri, row := range m.preview {
                // row cursor in preview focus
                if m.focusPreview && ri == m.selRow {
                    right.WriteString("> ")
                } else {
                    right.WriteString("  ")
                }
                for i, cell := range row {
                    cell = truncateCell(cell, colWidths[i])
                    right.WriteString(padRight(cell, colWidths[i]))
                    if i < len(row)-1 {
                        right.WriteString(" ")
                    }
                }
                right.WriteString("\n")
            }
        } else {
            right.WriteString("(no columns)\n")
        }
    }

    // Combine columns line by line
    leftLines := strings.Split(strings.TrimRight(left.String(), "\n"), "\n")
    rightLines := strings.Split(strings.TrimRight(right.String(), "\n"), "\n")
    maxLines := len(leftLines)
    if len(rightLines) > maxLines {
        maxLines = len(rightLines)
    }
    var out strings.Builder
    for i := 0; i < maxLines; i++ {
        var l, r string
        if i < len(leftLines) {
            l = padRight(truncateCell(leftLines[i], leftWidth), leftWidth)
        } else {
            l = strings.Repeat(" ", leftWidth)
        }
        if i < len(rightLines) {
            r = rightLines[i]
        } else {
            r = ""
        }
        out.WriteString(l)
        out.WriteString(" | ")
        out.WriteString(r)
        out.WriteString("\n")
    }
    if m.status != "" {
        out.WriteString("\n" + m.status + "\n")
    }
    return out.String()
}

func padRight(s string, width int) string {
    if width <= 0 {
        return ""
    }
    if len(s) >= width {
        return s
    }
    return s + strings.Repeat(" ", width-len(s))
}

func computeColumnWidths(cols []string, rows [][]string, maxWidth int) []int {
    n := len(cols)
    if n == 0 {
        return nil
    }
    // Start with min width per column based on header
    widths := make([]int, n)
    for i, c := range cols {
        if len(c) < 3 {
            widths[i] = 3
        } else if len(c) > 20 {
            widths[i] = 20
        } else {
            widths[i] = len(c)
        }
    }
    // Consider data lengths for a more informed default, capped
    for _, row := range rows {
        for i, cell := range row {
            l := len(cell)
            if l > 40 {
                l = 40
            }
            if l > widths[i] {
                widths[i] = l
            }
        }
    }
    // Add minimal spaces between columns
    total := sum(widths) + (n-1)*1
    if total <= maxWidth {
        return widths
    }
    // Need to shrink proportionally, but keep a minimum of 3 chars per column
    toReduce := total - maxWidth
    for toReduce > 0 {
        // find column with largest width > 3 and reduce
        idx := -1
        maxW := 0
        for i, w := range widths {
            if w > maxW && w > 3 {
                maxW = w
                idx = i
            }
        }
        if idx == -1 {
            break
        }
        widths[idx]--
        toReduce--
    }
    return widths
}

func sum(v []int) int { s := 0; for _, x := range v { s += x }; return s }

func main() {
    if len(os.Getenv("DEBUG")) > 0 {
        f, err := tea.LogToFile("debug.log", "debug")
        if err == nil {
            defer f.Close()
        }
    }
    p := tea.NewProgram(initialModel())
    if _, err := p.Run(); err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
}


// copyToClipboard copies given text to the system clipboard across platforms.
func copyToClipboard(text string) error {
    switch runtime.GOOS {
    case "darwin":
        cmd := exec.Command("pbcopy")
        cmd.Stdin = strings.NewReader(text)
        return cmd.Run()
    case "windows":
        // Use clip.exe
        cmd := exec.Command("cmd", "/c", "clip")
        cmd.Stdin = strings.NewReader(text)
        return cmd.Run()
    default:
        // Linux and others: try wl-copy, then xclip
        if _, err := exec.LookPath("wl-copy"); err == nil {
            cmd := exec.Command("wl-copy")
            cmd.Stdin = strings.NewReader(text)
            return cmd.Run()
        }
        if _, err := exec.LookPath("xclip"); err == nil {
            cmd := exec.Command("xclip", "-selection", "clipboard")
            cmd.Stdin = strings.NewReader(text)
            return cmd.Run()
        }
        // Last resort: xsel
        if _, err := exec.LookPath("xsel"); err == nil {
            cmd := exec.Command("xsel", "--clipboard", "--input")
            cmd.Stdin = strings.NewReader(text)
            return cmd.Run()
        }
        return fmt.Errorf("no clipboard utility found (install wl-copy, xclip, or xsel)")
    }
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
        out = append(out, colInfo{Name: name, Type: ctype, PKOrder: pk})
    }
    return out, rows.Err()
}

func hasExplicitPK(cols []colInfo) bool {
    for _, c := range cols {
        if c.PKOrder > 0 {
            return true
        }
    }
    return false
}

func findColIndex(cols []string, name string) int {
    for i, c := range cols {
        if strings.EqualFold(c, name) {
            return i
        }
    }
    return -1
}

func (m *model) duplicateCurrentRow() error {
    if m.db == nil || m.cursor < 0 || m.cursor >= len(m.tables) {
        return fmt.Errorf("no table selected")
    }
    if m.selRow < 0 || m.selRow >= len(m.preview) {
        return fmt.Errorf("no row selected")
    }
    table := m.tables[m.cursor]
    // Determine PK
    var pkCols []colInfo
    for _, c := range m.tableCols {
        if c.PKOrder > 0 {
            pkCols = append(pkCols, c)
        }
    }
    if len(pkCols) > 1 {
        return fmt.Errorf("composite primary keys not supported for duplicate insert")
    }
    // Build column list
    colNames := make([]string, 0, len(m.previewColumns))
    for _, c := range m.previewColumns { colNames = append(colNames, c) }

    // Quick helpers
    colType := make(map[string]string, len(m.tableCols))
    for _, c := range m.tableCols { colType[strings.ToLower(c.Name)] = strings.ToUpper(strings.TrimSpace(c.Type)) }
    getVal := func(col string) string {
        idx := findColIndex(m.previewColumns, col)
        if idx >= 0 && idx < len(m.preview[m.selRow]) { return m.preview[m.selRow][idx] }
        return ""
    }

    // Unique indexes
    uidx, err := getUniqueIndexes(m.db, table)
    if err != nil {
        return err
    }

    // Track changed columns (pk change counts as change)
    changed := make(map[string]struct{})
    overrides := make(map[string]any)

    // Determine PK handling
    usingRowid := false
    var pkName string
    if len(pkCols) == 0 {
        // No explicit PK → rowid path
        usingRowid = true
    } else {
        pk := pkCols[0]
        pkName = pk.Name
        pkTypeUpper := strings.ToUpper(strings.TrimSpace(pk.Type))
        if pkTypeUpper == "INTEGER" {
            // Omit PK column so SQLite assigns new rowid
            insertCols := without(colNames, pkName)
            // Compute overrides for unique constraints
            changed[strings.ToLower(pkName)] = struct{}{}
            if err := m.computeUniqueOverrides(table, insertCols, colType, uidx, changed, overrides); err != nil {
                return err
            }
            // Build select exprs
            selectExprs, params := buildSelectExprs(insertCols, overrides)
            colsCSV := quoteIdentList(insertCols)
            where := fmt.Sprintf("%s = ?", quoteIdent(pkName))
            q := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s WHERE %s", quoteIdent(table), colsCSV, strings.Join(selectExprs, ", "), quoteIdent(table), where)
            params = append(params, getVal(pkName))
            _, err := m.db.Exec(q, params...)
            return err
        }
        // Non-integer PK: compute a new value and override
        var newPK any
        if isTextType(pkTypeUpper) {
            newPK = uuid.NewString()
        } else if isNumericType(pkTypeUpper) {
            var nextVal sql.NullInt64
            q := fmt.Sprintf("SELECT COALESCE(MAX(%s)+1,1) FROM %s", quoteIdent(pkName), quoteIdent(table))
            if err := m.db.QueryRow(q).Scan(&nextVal); err != nil { return err }
            if !nextVal.Valid { nextVal.Int64 = 1 }
            newPK = nextVal.Int64
        } else {
            newPK = uuid.NewString()
        }
        overrides[strings.ToLower(pkName)] = newPK
        changed[strings.ToLower(pkName)] = struct{}{}
    }

    // Now compute overrides for unique constraints for remaining scenarios
    targetCols := colNames
    whereClause := ""
    var whereParam any
    if usingRowid {
        if m.previewRowIDs == nil || m.selRow >= len(m.previewRowIDs) {
            return fmt.Errorf("rowid unavailable for this table")
        }
        whereClause = "rowid = ?"
        whereParam = m.previewRowIDs[m.selRow]
    } else {
        whereClause = fmt.Sprintf("%s = ?", quoteIdent(pkName))
        whereParam = getVal(pkName)
    }

    if err := m.computeUniqueOverrides(table, targetCols, colType, uidx, changed, overrides); err != nil {
        return err
    }

    // Build select exprs and params
    selectExprs, params := buildSelectExprs(targetCols, overrides)
    colsCSV := quoteIdentList(targetCols)
    q := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s WHERE %s", quoteIdent(table), colsCSV, strings.Join(selectExprs, ", "), quoteIdent(table), whereClause)
    params = append(params, whereParam)
    _, err = m.db.Exec(q, params...)
    return err
}

func quoteIdentList(names []string) string {
    out := make([]string, len(names))
    for i, n := range names { out[i] = quoteIdent(n) }
    return strings.Join(out, ", ")
}

func without(names []string, name string) []string {
    out := make([]string, 0, len(names))
    for _, n := range names { if !strings.EqualFold(n, name) { out = append(out, n) } }
    return out
}

func isTextType(typeUpper string) bool {
    t := typeUpper
    return strings.Contains(t, "CHAR") || strings.Contains(t, "TEXT") || strings.Contains(t, "CLOB")
}

func isNumericType(typeUpper string) bool {
    t := typeUpper
    return strings.Contains(t, "INT") || strings.Contains(t, "REAL") || strings.Contains(t, "NUM") || strings.Contains(t, "DEC") || strings.Contains(t, "DOUBLE") || strings.Contains(t, "FLOAT")
}

func (m *model) computeUniqueOverrides(table string, insertCols []string, colType map[string]string, uidx []uniqueIndex, changed map[string]struct{}, overrides map[string]any) error {
    // Build quick set for present columns
    present := make(map[string]struct{}, len(insertCols))
    for _, c := range insertCols { present[strings.ToLower(c)] = struct{}{} }
    // For each unique index, ensure at least one participating column will change
    for _, ix := range uidx {
        // filter columns to those present in insertCols
        cols := make([]string, 0, len(ix.Columns))
        for _, c := range ix.Columns {
            lc := strings.ToLower(c)
            if _, ok := present[lc]; ok {
                cols = append(cols, c)
            }
        }
        if len(cols) == 0 { continue }
        already := false
        for _, c := range cols {
            if _, ok := changed[strings.ToLower(c)]; ok {
                already = true; break
            }
        }
        if already { continue }
        // Choose a column to modify
        choose := ""
        for _, c := range cols {
            if isTextType(colType[strings.ToLower(c)]) { choose = c; break }
        }
        if choose == "" {
            for _, c := range cols {
                if isNumericType(colType[strings.ToLower(c)]) { choose = c; break }
            }
        }
        if choose == "" { choose = cols[0] }
        // Compute new value
        lc := strings.ToLower(choose)
        if isTextType(colType[lc]) {
            base := ""
            // try to get base from current preview row
            idx := findColIndex(m.previewColumns, choose)
            if idx >= 0 && idx < len(m.preview[m.selRow]) { base = m.preview[m.selRow][idx] }
            overrides[lc] = base + "-" + uuid.NewString()[:8]
        } else if isNumericType(colType[lc]) {
            var nextVal sql.NullInt64
            q := fmt.Sprintf("SELECT COALESCE(MAX(%s)+1,1) FROM %s", quoteIdent(choose), quoteIdent(table))
            if err := m.db.QueryRow(q).Scan(&nextVal); err != nil { return err }
            if !nextVal.Valid { nextVal.Int64 = 1 }
            overrides[lc] = nextVal.Int64
        } else {
            overrides[lc] = uuid.NewString()
        }
        changed[lc] = struct{}{}
    }
    return nil
}

func buildSelectExprs(insertCols []string, overrides map[string]any) ([]string, []any) {
    exprs := make([]string, 0, len(insertCols))
    params := make([]any, 0, len(insertCols))
    for _, c := range insertCols {
        if v, ok := overrides[strings.ToLower(c)]; ok {
            exprs = append(exprs, "?")
            params = append(params, v)
        } else {
            exprs = append(exprs, quoteIdent(c))
        }
    }
    return exprs, params
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

