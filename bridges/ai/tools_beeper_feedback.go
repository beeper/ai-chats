package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const rageshakeURL = "https://rageshake.beeper.com/api/submit"

func executeBeeperSendFeedback(ctx context.Context, args map[string]any) (string, error) {
	text, _ := args["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("missing or invalid 'text' argument")
	}

	feedbackType := "problem"
	if t, ok := args["type"].(string); ok && strings.TrimSpace(t) != "" {
		feedbackType = strings.TrimSpace(t)
	}

	// Prepend AI Chats tag
	text = "aichats: " + text

	// Best-effort: include a stable login id (not PII) when available.
	loginID := ""
	btc := GetBridgeToolContext(ctx)
	if btc != nil && btc.Client != nil && btc.Client.UserLogin != nil {
		loginID = string(btc.Client.UserLogin.ID)
	}

	// Build multipart form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fields := map[string]string{
		"text":       text,
		"type":       feedbackType,
		"app":        "beeper-a8c-desktop",
		"os":         runtime.GOOS,
		"user_agent": "aichats/1.0",
	}
	if loginID != "" {
		fields["login_id"] = loginID
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return "", fmt.Errorf("failed to write form field %s: %w", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rageshakeURL, &buf)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to submit feedback: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("rageshake returned status %d: %s", resp.StatusCode, string(body))
	}

	result := map[string]any{
		"status":  "submitted",
		"type":    feedbackType,
		"message": "Feedback submitted successfully.",
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to encode result: %w", err)
	}
	return string(raw), nil
}
