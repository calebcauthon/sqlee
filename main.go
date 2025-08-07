package main

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    _ "modernc.org/sqlite"
)

type model struct {
    db              *sql.DB
    tables          []string
    cursor          int
    preview         [][]string
    previewColumns  []string
    status          string
    width           int
    height          int
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
    m.tables = tables
    if len(m.tables) > 0 {
        m.refreshPreview()
    }
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
    if m.db == nil || len(m.tables) == 0 || m.cursor < 0 || m.cursor >= len(m.tables) {
        return
    }
    tbl := m.tables[m.cursor]
    // Basic preview: limit 10 rows
    q := fmt.Sprintf("SELECT * FROM %s LIMIT 10", quoteIdent(tbl))
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
    m.previewColumns = cols

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
        rec := make([]string, len(cols))
        for i := range raw {
            rec[i] = formatValue(raw[i])
        }
        m.preview = append(m.preview, rec)
    }
    if err := rows.Err(); err != nil {
        m.status = fmt.Sprintf("rows error: %v", err)
    }
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
        switch msg.String() {
        case "ctrl+c", "q":
            if m.db != nil {
                _ = m.db.Close()
            }
            return m, tea.Quit
        case "up", "k":
            if m.cursor > 0 {
                m.cursor--
                m.refreshPreview()
            }
        case "down", "j":
            if m.cursor < len(m.tables)-1 {
                m.cursor++
                m.refreshPreview()
            }
        case "r":
            // reload tables
            if m.db != nil {
                t, err := listTables(m.db)
                if err != nil {
                    m.status = fmt.Sprintf("reload error: %v", err)
                } else {
                    sort.Strings(t)
                    m.tables = t
                    if m.cursor >= len(m.tables) {
                        m.cursor = max(0, len(m.tables)-1)
                    }
                    m.refreshPreview()
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
    left.WriteString("Tables (j/k or ↓/↑ to move, r to reload, q to quit)\n")
    for i, t := range m.tables {
        cursor := "  "
        if i == m.cursor {
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
        right.WriteString(fmt.Sprintf("Preview: %s (up to 10 rows)\n", m.tables[m.cursor]))
        if len(m.previewColumns) > 0 {
            // compute column widths based on available rightWidth
            colWidths := computeColumnWidths(m.previewColumns, m.preview, rightWidth)
            // header
            for i, c := range m.previewColumns {
                cell := padRight(truncateCell(c, colWidths[i]), colWidths[i])
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
            for _, row := range m.preview {
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


