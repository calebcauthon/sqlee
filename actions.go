package main

import (
    "database/sql"
    "fmt"
    "strings"

    "github.com/google/uuid"
)

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

// insertEmptyRow creates a new row using DEFAULT VALUES for the current table.
// This works when the table has defaults or nullable columns. If NOT NULL
// constraints without defaults exist, SQLite will return an error which we
// surface to the user.
func (m *model) insertEmptyRow() error {
    if m.db == nil || m.cursor < 0 || m.cursor >= len(m.tables) {
        return fmt.Errorf("no table selected")
    }
    table := m.tables[m.cursor]
    // Prefer DEFAULT VALUES when possible; but if table has NOT NULL columns without defaults,
    // fallback to constructing an explicit INSERT with minimal placeholder values.
    // First, try DEFAULT VALUES quickly.
    if _, err := m.db.Exec(fmt.Sprintf("INSERT INTO %s DEFAULT VALUES", quoteIdent(table))); err == nil {
        return nil
    }
    // Build column/value lists honoring NOT NULL and defaults
    insertCols := make([]string, 0, len(m.tableCols))
    params := make([]any, 0, len(m.tableCols))
    values := make([]string, 0, len(m.tableCols))
    for _, c := range m.tableCols {
        // Skip INTEGER PRIMARY KEY so SQLite can auto-assign rowid
        if c.PKOrder > 0 && strings.EqualFold(strings.TrimSpace(c.Type), "INTEGER") {
            continue
        }
        // If column has non-NULL default, omit and let default apply
        if c.Default.Valid {
            // Omitting the column lets DEFAULT be used
            continue
        }
        // If NOT NULL with no default, supply a minimal value by type
        if c.NotNull {
            insertCols = append(insertCols, c.Name)
            tUpper := strings.ToUpper(strings.TrimSpace(c.Type))
            if isTextType(tUpper) {
                params = append(params, "")
            } else if isNumericType(tUpper) {
                params = append(params, 0)
            } else {
                // Blob/unknown -> empty blob as text fallback
                params = append(params, "")
            }
            values = append(values, "?")
            continue
        }
        // Nullable with no default: omit to get NULL
    }
    if len(insertCols) == 0 {
        // Nothing to set explicitly, last resort retry DEFAULT VALUES to surface the original error
        _, err := m.db.Exec(fmt.Sprintf("INSERT INTO %s DEFAULT VALUES", quoteIdent(table)))
        return err
    }
    q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quoteIdent(table), quoteIdentList(insertCols), strings.Join(values, ", "))
    _, err := m.db.Exec(q, params...)
    return err
}

func (m *model) deleteCurrentRow() error {
    if m.db == nil || m.cursor < 0 || m.cursor >= len(m.tables) {
        return fmt.Errorf("no table selected")
    }
    if m.selRow < 0 || m.selRow >= len(m.preview) {
        return fmt.Errorf("no row selected")
    }
    table := m.tables[m.cursor]
    // Prefer explicit PKs
    var pkCols []colInfo
    for _, c := range m.tableCols {
        if c.PKOrder > 0 { pkCols = append(pkCols, c) }
    }
    if len(pkCols) > 0 {
        // Build WHERE using all PK parts (supports composite PK)
        whereParts := make([]string, 0, len(pkCols))
        params := make([]any, 0, len(pkCols))
        for _, pk := range pkCols {
            whereParts = append(whereParts, fmt.Sprintf("%s = ?", quoteIdent(pk.Name)))
            // pull value from current preview row
            idx := findColIndex(m.previewColumns, pk.Name)
            if idx >= 0 && idx < len(m.preview[m.selRow]) {
                params = append(params, m.preview[m.selRow][idx])
            } else {
                params = append(params, nil)
            }
        }
        q := fmt.Sprintf("DELETE FROM %s WHERE %s", quoteIdent(table), strings.Join(whereParts, " AND "))
        _, err := m.db.Exec(q, params...)
        return err
    }
    // Fallback to rowid
    if m.previewRowIDs == nil || m.selRow >= len(m.previewRowIDs) {
        return fmt.Errorf("cannot resolve row identifier (no pk/rowid)")
    }
    rowid := m.previewRowIDs[m.selRow]
    q := fmt.Sprintf("DELETE FROM %s WHERE rowid = ?", quoteIdent(table))
    _, err := m.db.Exec(q, rowid)
    return err
}

// commitCellEdit updates the database with the current editBuffer for the selected cell.
func (m *model) commitCellEdit() error {
    if m.db == nil || m.cursor < 0 || m.cursor >= len(m.tables) {
        return fmt.Errorf("no table selected")
    }
    if m.selRow < 0 || m.selRow >= len(m.preview) {
        return fmt.Errorf("no row selected")
    }
    if m.selCol < 0 || m.selCol >= len(m.previewColumns) {
        return fmt.Errorf("no column selected")
    }
    table := m.tables[m.cursor]
    colName := m.previewColumns[m.selCol]
    // Interpret literal NULL (case-insensitive) as SQL NULL
    var newVal any = m.editBuffer
    if strings.EqualFold(strings.TrimSpace(m.editBuffer), "NULL") {
        newVal = nil
    }
    // Prefer explicit PKs for WHERE condition; support composite PK
    var pkCols []colInfo
    for _, c := range m.tableCols {
        if c.PKOrder > 0 { pkCols = append(pkCols, c) }
    }
    if len(pkCols) > 0 {
        // Build UPDATE ... WHERE pk1=? AND pk2=? ...
        setExpr := fmt.Sprintf("%s = ?", quoteIdent(colName))
        whereParts := make([]string, 0, len(pkCols))
        params := make([]any, 0, len(pkCols)+1)
        params = append(params, newVal)
        for _, pk := range pkCols {
            whereParts = append(whereParts, fmt.Sprintf("%s = ?", quoteIdent(pk.Name)))
            // pull value from current preview row
            idx := findColIndex(m.previewColumns, pk.Name)
            if idx >= 0 && idx < len(m.preview[m.selRow]) {
                params = append(params, m.preview[m.selRow][idx])
            } else {
                params = append(params, nil)
            }
        }
        q := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quoteIdent(table), setExpr, strings.Join(whereParts, " AND "))
        _, err := m.db.Exec(q, params...)
        return err
    }
    // Fallback to rowid
    if m.previewRowIDs == nil || m.selRow >= len(m.previewRowIDs) {
        return fmt.Errorf("cannot resolve row identifier (no pk/rowid)")
    }
    rowid := m.previewRowIDs[m.selRow]
    setExpr := fmt.Sprintf("%s = ?", quoteIdent(colName))
    q := fmt.Sprintf("UPDATE %s SET %s WHERE rowid = ?", quoteIdent(table), setExpr)
    _, err := m.db.Exec(q, newVal, rowid)
    return err
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

func (m *model) deleteCurrentTable() error {
    if m.db == nil || m.cursor < 0 || m.cursor >= len(m.tables) {
        return fmt.Errorf("no table selected")
    }
    name := m.confirmDeleteTarget
    if name == "" { name = m.tables[m.cursor] }
    typ := m.confirmDeleteType
    if typ == "" {
        var err error
        typ, err = getObjectType(m.db, name)
        if err != nil { return err }
    }
    stmt := ""
    if typ == "view" {
        stmt = fmt.Sprintf("DROP VIEW IF EXISTS %s", quoteIdent(name))
    } else {
        stmt = fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteIdent(name))
    }
    _, err := m.db.Exec(stmt)
    return err
}
