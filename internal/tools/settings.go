package tools

import (
	"context"
	"database/sql"
	"fmt"
)

// Settings is a tiny key-value store backed by the bot_settings table.
// We use it for a handful of bot-wide flags that don't justify their
// own table (owner chat ID, monthly budget once Phase 5 lands, etc.).
type Settings struct {
	db *sql.DB
}

func NewSettings(db *sql.DB) *Settings {
	return &Settings{db: db}
}

// Get returns the stored value or "" if the key is unset.
func (s *Settings) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM bot_settings WHERE key = ?`, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting setting %q: %w", key, err)
	}
	return value, nil
}

// Set writes a value, replacing any existing entry.
func (s *Settings) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bot_settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE
		SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	return nil
}

// Well-known keys — collected here so we don't sprinkle string
// literals across the codebase.
const (
	KeyOwnerChatID = "owner_chat_id"
)
