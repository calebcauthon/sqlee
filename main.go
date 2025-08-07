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
    p := tea.NewProgram(initialModel())
    if _, err := p.Run(); err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
}
