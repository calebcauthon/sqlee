package main

import (
    "fmt"
    "sort"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
)

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        // If currently editing a cell, handle input differently
        if m.editingActive {
            switch msg.Type {
            case tea.KeyRunes:
                if len(msg.Runes) > 0 {
                    m.editBuffer += string(msg.Runes)
                }
                return m, nil
            case tea.KeyBackspace:
                r := []rune(m.editBuffer)
                if len(r) > 0 {
                    m.editBuffer = string(r[:len(r)-1])
                }
                return m, nil
            case tea.KeyEnter:
                // commit edit
                if err := m.commitCellEdit(); err != nil {
                    m.status = fmt.Sprintf("update error: %v", err)
                } else {
                    m.status = "updated"
                }
                m.editingActive = false
                m.editBuffer = ""
                m.refreshPreview()
                return m, nil
            case tea.KeyEsc:
                m.editingActive = false
                m.editBuffer = ""
                m.status = "cancelled edit"
                return m, nil
            default:
                return m, nil
            }
        }
        // Confirmation modal for table/view deletion
        if m.confirmDeleteActive {
            switch msg.String() {
            case "y", "Y":
                if err := m.deleteCurrentTable(); err != nil {
                    m.status = fmt.Sprintf("drop %s error: %v", m.confirmDeleteType, err)
                } else {
                    m.status = fmt.Sprintf("dropped %s %s", m.confirmDeleteType, m.confirmDeleteTarget)
                    // reload tables
                    if m.db != nil {
                        if t, err := listTables(m.db); err == nil {
                            sort.Strings(t)
                            m.allTables = t
                            // keep filter
                            m.applyFilter()
                        }
                    }
                }
                m.confirmDeleteActive = false
                m.confirmDeleteTarget = ""
                return m, nil
            case "n", "N", "esc":
                m.confirmDeleteActive = false
                m.confirmDeleteTarget = ""
                m.status = "cancelled"
                return m, nil
            default:
                return m, nil
            }
        }
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
        case "c":
            // begin editing the current cell when focus is on preview
            if m.focusPreview && m.selRow >= 0 && m.selRow < len(m.preview) && m.selCol >= 0 && m.selCol < len(m.previewColumns) {
                m.editingActive = true
                // seed buffer with current cell text
                cur := m.preview[m.selRow]
                if m.selCol < len(cur) { m.editBuffer = cur[m.selCol] } else { m.editBuffer = "" }
                m.status = fmt.Sprintf("editing %s", m.previewColumns[m.selCol])
            }
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
        case "x":
            if m.focusPreview {
                prev := m.selRow
                if err := m.deleteCurrentRow(); err != nil {
                    m.status = fmt.Sprintf("delete error: %v", err)
                } else {
                    m.status = "deleted row"
                    if prev > 0 && prev == len(m.preview)-1 { m.selRow = prev - 1 }
                    m.refreshPreview()
                }
            } else if m.cursor >= 0 && m.cursor < len(m.tables) {
                // from the left pane: request confirmation to drop table or view
                name := m.tables[m.cursor]
                // determine if table or view
                t, err := getObjectType(m.db, name)
                if err != nil { m.status = fmt.Sprintf("lookup type error: %v", err); return m, nil }
                m.confirmDeleteActive = true
                m.confirmDeleteTarget = name
                m.confirmDeleteType = t
                m.status = fmt.Sprintf("drop %s %s? (y/n)", t, name)
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
                if len(m.preview) == 0 {
                    if err := m.insertEmptyRow(); err != nil {
                        m.status = fmt.Sprintf("insert error: %v", err)
                    } else {
                        m.status = "inserted new row"
                        m.refreshPreview()
                    }
                } else {
                    if err := m.duplicateCurrentRow(); err != nil {
                        m.status = fmt.Sprintf("insert error: %v", err)
                    } else {
                        m.status = "inserted duplicate row"
                        m.refreshPreview()
                    }
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
    left.WriteString(styleHeader.Render("Tables (j/k or ↓/↑, → to preview, / search, r reload, q quit)") + "\n")
    if m.searchActive || m.searchQuery != "" {
        left.WriteString(styleSearch.Render("/" + m.searchQuery) + "\n")
    }
    for i, t := range m.tables {
        cursor := "  "
        if i == m.cursor && !m.focusPreview {
            cursor = styleCursor.Render("> ")
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
        if m.focusPreview { title += " " + styleFocusTag.Render("FOCUS") }
        if m.editingActive { title += " " + stylePrompt.Render("EDITING") }
        right.WriteString(styleHeader.Render(title) + "\n")
        if len(m.previewColumns) > 0 {
            // compute column widths based on available rightWidth minus the 2-char row gutter
            cwAvail := rightWidth - 2
            if cwAvail < 1 { cwAvail = 1 }
            colWidths := computeColumnWidths(m.previewColumns, m.preview, cwAvail)
            // header (align with row gutter)
            right.WriteString("  ")
            for i, c := range m.previewColumns {
                headerText := c
                // add selection marker before truncation
                isSelectedHeader := m.focusPreview && i == m.selCol
                if isSelectedHeader {
                    headerText = "*" + headerText
                }
                headerText = truncateCell(headerText, colWidths[i])
                if isSelectedHeader {
                    headerText = styleColSelect.Render(headerText)
                }
                cell := padRightANSI(headerText, colWidths[i])
                right.WriteString(cell)
                if i < len(m.previewColumns)-1 {
                    right.WriteString(" ")
                }
            }
            right.WriteString("\n")
            // separator (align with row gutter)
            right.WriteString("  ")
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
                if m.focusPreview && ri == m.selRow { right.WriteString(styleCursor.Render("> ")) } else { right.WriteString("  ") }
                for i, cell := range row {
                    // If editing this cell, render buffer
                    if m.editingActive && m.focusPreview && ri == m.selRow && i == m.selCol {
                        cell = m.editBuffer
                    }
                    cell = truncateCell(cell, colWidths[i])
                    right.WriteString(padRightANSI(cell, colWidths[i]))
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
            l = padRightANSI(truncateANSI(leftLines[i], leftWidth), leftWidth)
        } else {
            l = strings.Repeat(" ", leftWidth)
        }
        if i < len(rightLines) {
            r = rightLines[i]
            // Ensure a consistent two-char gutter on the right pane for all lines
            if !hasRightPaneGutter(r) {
                r = "  " + r
            }
        } else {
            r = ""
        }
        out.WriteString(l)
        out.WriteString(" | ")
        out.WriteString(r)
        out.WriteString("\n")
    }
    if m.status != "" {
        // Highlight confirmation prompts vs info/errors
        rendered := m.status
        ls := strings.ToLower(m.status)
        if strings.HasPrefix(ls, "drop table") || strings.HasPrefix(ls, "drop view") || strings.Contains(ls, "(y/n)") {
            rendered = stylePrompt.Render(m.status)
        } else if strings.Contains(ls, "error") {
            rendered = styleError.Render(m.status)
        } else {
            rendered = styleInfo.Render(m.status)
        }
        out.WriteString("\n" + rendered + "\n")
    }
    return out.String()
}
