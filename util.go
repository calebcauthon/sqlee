package main

import (
    "fmt"
    "strconv"
    "strings"
)

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

func findColIndex(cols []string, name string) int {
    for i, c := range cols {
        if strings.EqualFold(c, name) {
            return i
        }
    }
    return -1
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

func max(a, b int) int { if a > b { return a }; return b }
