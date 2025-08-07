package main

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
