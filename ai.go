package main

import (
    "context"
    "os"
    "time"

    "github.com/tmc/langchaingo/llms"
    "github.com/tmc/langchaingo/llms/openai"
    tea "github.com/charmbracelet/bubbletea"
)

// runLLM sends the prompt to the configured LLM and returns a command that yields aiResponseMsg.
func runLLM(prompt string) tea.Cmd {
    return func() tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
        defer cancel()

        // Allow model and API key to be configured via env
        // OPENAI_API_KEY is used by langchaingo's openai.New()
        // Optional: LLM_MODEL env sets the model, defaults to gpt-4o-mini
        modelName := os.Getenv("LLM_MODEL")
        if modelName == "" {
            modelName = "gpt-4o-mini"
        }

        llm, err := openai.New()
        if err != nil {
            return aiResponseMsg{"", err}
        }

        // Use a simple single prompt completion
        completion, err := llms.GenerateFromSinglePrompt(ctx, llm, prompt, llms.WithModel(modelName))
        if err != nil {
            return aiResponseMsg{"", err}
        }
        return aiResponseMsg{completion, nil}
    }
}



