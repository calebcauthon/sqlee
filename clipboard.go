package main

import (
    "fmt"
    "os/exec"
    "runtime"
    "strings"
)

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
