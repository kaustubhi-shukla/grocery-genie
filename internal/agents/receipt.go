// Package agents holds the AI-powered logic for GroceryGenie.
//
// Each agent is responsible for one kind of user input:
//
//   - ReceiptAgent: scans a receipt image, returns structured items.
//
// In later phases we will add UsageAgent (meal logging), StockAgent
// (queries), BudgetAgent (spend), and a router (Google ADK). For now
// receipt scanning is the only AI surface.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/kaustubhi-shukla/grocery-genie/internal/prompts"
)

// ErrTransient is returned when a scan fails because of a transient
// upstream issue (e.g. Gemini 503, network blip). The caller — usually
// the Telegram handler — should show a "service busy, try again"
// message rather than blame the image.
var ErrTransient = errors.New("transient AI service error")

// Receipt is the structured output of scanning a receipt image.
// The JSON tags match the schema in prompts/receipt_scan.txt; Gemini
// returns JSON in that shape, which we decode into this struct.
type Receipt struct {
	StoreName      string        `json:"store_name"`
	Date           string        `json:"date"`
	Items          []ReceiptItem `json:"items"`
	Total          float64       `json:"total"`
	Confidence     float64       `json:"confidence"`
	AmbiguousItems []string      `json:"ambiguous_items,omitempty"`
}

// ReceiptItem is one line item extracted from a receipt.
type ReceiptItem struct {
	Name         string  `json:"name"`
	Quantity     float64 `json:"quantity"`
	Unit         string  `json:"unit"`
	Price        float64 `json:"price"`
	Category     string  `json:"category"`
	TrackingType string  `json:"tracking_type"`
}

// ReceiptAgent wraps the Gemini client and exposes Scan().
// New() is the constructor; the caller is responsible for the
// API key (it should come from the GEMINI_API_KEY env var).
type ReceiptAgent struct {
	client *genai.Client
	model  string
}

// NewReceiptAgent builds an agent ready to scan receipts. The Gemini
// SDK uses contexts everywhere; we accept one so the caller can
// cancel/timeout long calls (e.g. from a Telegram request).
func NewReceiptAgent(ctx context.Context, apiKey string) (*ReceiptAgent, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI, // free tier — public Gemini API, not Vertex
	})
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %w", err)
	}

	return &ReceiptAgent{
		client: client,
		// Gemini 2.5 Flash: multimodal (text+vision), generous free quota,
		// fast enough for chat-bot latency requirements.
		model: "gemini-2.5-flash",
	}, nil
}

// Scan sends a receipt image (or PDF) to Gemini Vision and returns
// the parsed items. mimeType is the file MIME type (e.g. "image/jpeg"
// or "application/pdf").
//
// Implements automatic retry with exponential backoff for transient
// upstream errors (HTTP 503, UNAVAILABLE, deadline exceeded). If
// every retry fails, returns ErrTransient wrapped, so the caller can
// show the user a "service busy" message rather than blame the image.
func (a *ReceiptAgent) Scan(ctx context.Context, imageBytes []byte, mimeType string) (*Receipt, error) {
	// Build a multimodal request: one inline image part + the user's
	// instruction. The system prompt tells Gemini to return JSON.
	parts := []*genai.Part{
		{InlineData: &genai.Blob{
			MIMEType: mimeType,
			Data:     imageBytes,
		}},
		{Text: "Parse this receipt:"},
	}

	contents := []*genai.Content{{Parts: parts, Role: "user"}}
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: prompts.ReceiptScan}},
		},
		ResponseMIMEType: "application/json",
		Temperature:      genai.Ptr[float32](0.1), // low temp = consistent, deterministic output
	}

	// Retry loop with exponential backoff for transient errors.
	// 4 attempts total: immediate, +1s, +2s, +4s.
	// Permanent errors (bad input, auth, schema) bail out immediately.
	const maxAttempts = 4
	backoff := 1 * time.Second
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := a.client.Models.GenerateContent(ctx, a.model, contents, config)

		if err == nil {
			rawText := strings.TrimSpace(resp.Text())
			if rawText == "" {
				// Empty response is unusual — treat as transient and retry.
				lastErr = fmt.Errorf("gemini returned empty response")
			} else {
				var receipt Receipt
				if jsonErr := json.Unmarshal([]byte(rawText), &receipt); jsonErr != nil {
					// JSON parse failure is NOT transient — model gave us
					// something it cannot. Log raw output for prompt tuning.
					return nil, fmt.Errorf("parsing gemini JSON: %w\nraw output:\n%s", jsonErr, rawText)
				}
				return &receipt, nil // success
			}
		} else {
			lastErr = err
			if !isTransient(err) {
				// Permanent error (auth, invalid input, etc.) — stop retrying.
				return nil, fmt.Errorf("gemini generate: %w", err)
			}
		}

		// Transient error — wait and retry (unless this was the last attempt).
		if attempt < maxAttempts {
			log.Printf("gemini transient error (attempt %d/%d), retrying in %v: %v",
				attempt, maxAttempts, backoff, lastErr)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(backoff):
			}
			backoff *= 2 // exponential: 1s, 2s, 4s
		}
	}

	return nil, fmt.Errorf("%w: %v (after %d attempts)", ErrTransient, lastErr, maxAttempts)
}

// isTransient returns true if the error is the kind that might succeed
// if retried (capacity issues, timeouts, network blips) versus a
// permanent failure (bad request, invalid auth, schema rejection).
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	transientMarkers := []string{
		"503",
		"unavailable",
		"deadline exceeded",
		"timeout",
		"rate limit",
		"429", // too many requests
		"500", // internal server error
		"502", // bad gateway
		"504", // gateway timeout
		"high demand",
		"try again",
	}
	for _, marker := range transientMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Close releases any resources held by the underlying Gemini client.
func (a *ReceiptAgent) Close() {
	// genai.Client currently has no Close method, but we keep this
	// hook so callers can defer cleanup symmetrically with the DB.
}
