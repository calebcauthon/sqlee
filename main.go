package main

import (
    "fmt"
    "os"

    tea "github.com/charmbracelet/bubbletea"
)

func main() {
    if len(os.Getenv("DEBUG")) > 0 {
        f, err := tea.LogToFile("debug.log", "debug")
        if err == nil {
            defer f.Close()
        }
    }
    // Pre-flight DB path check for a friendlier error before the TUI starts
    if resolveDBPath() == "" {
        fmt.Println("No SQLite .db file found. Usage: 'go run . <db path>' or set DB_PATH. Alternatively, place a .db in the current directory or in 'instance/'.")
        os.Exit(2)
    }
    p := tea.NewProgram(initialModel())
    if _, err := p.Run(); err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
}
