package main

import (
    "regexp"
    "strings"
    "unicode/utf8"

    "github.com/charmbracelet/lipgloss"
)

var (
    styleHeader    = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
    stylePrompt    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
    styleSearch    = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
    styleCursor    = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
    styleFocusTag  = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57")).Bold(true)
    styleError     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
    styleInfo      = lipgloss.NewStyle().Foreground(lipgloss.Color("178"))
    styleColSelect = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
    styleAI        = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
)

// ansiRegexp matches ANSI SGR escape sequences for styling (e.g., "\x1b[31m").
var ansiRegexp = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth returns the printable width of s excluding ANSI escape sequences.
func visibleWidth(s string) int {
    if s == "" { return 0 }
    // Strip ANSI, then count runes
    plain := ansiRegexp.ReplaceAllString(s, "")
    return len([]rune(plain))
}

// truncateANSI truncates s to max printable columns, preserving ANSI sequences.
func truncateANSI(s string, max int) string {
    if max <= 0 || s == "" { return "" }
    // Fast path: no ANSI sequences
    if !strings.Contains(s, "\x1b[") {
        // Truncate by runes to avoid breaking multibyte chars
        r := []rune(s)
        if len(r) <= max { return s }
        return string(r[:max])
    }
    // Walk bytes, preserve ANSI codes, count printable runes
    var out []byte
    count := 0
    i := 0
    b := []byte(s)
    for i < len(b) && count < max {
        if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[' { // ESC[
            // copy until 'm' inclusive (best-effort for SGR)
            j := i + 2
            for j < len(b) {
                if (b[j] >= 'A' && b[j] <= 'Z') || (b[j] >= 'a' && b[j] <= 'z') { // final byte
                    j++
                    break
                }
                j++
            }
            out = append(out, b[i:j]...)
            i = j
            continue
        }
        // decode next rune
        r, size := utf8.DecodeRune(b[i:])
        if r == utf8.RuneError && size == 1 {
            // invalid byte; treat as single-width
            out = append(out, b[i])
            i++
            count++
            continue
        }
        if count+1 > max { break }
        out = append(out, b[i:i+size]...)
        i += size
        count++
    }
    return string(out)
}

// padRightANSI pads s with spaces to reach width printable columns, or truncates if longer.
func padRightANSI(s string, width int) string {
    if width <= 0 { return "" }
    vw := visibleWidth(s)
    if vw >= width {
        return truncateANSI(s, width)
    }
    return s + strings.Repeat(" ", width-vw)
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string { return ansiRegexp.ReplaceAllString(s, "") }

// hasRightPaneGutter reports whether s starts (visibly) with the two-char right pane gutter
// such as "> " (styled) or "  ".
func hasRightPaneGutter(s string) bool {
    plain := stripANSI(s)
    return strings.HasPrefix(plain, "> ") || strings.HasPrefix(plain, "  ")
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
