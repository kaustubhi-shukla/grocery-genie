package tools

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Activity tracks daily logging activity for the 8 PM nudge.
// "Logged" = the user did SOMETHING today: a purchase, usage event,
// outside meal, etc. We don't care which — just that the user
// interacted in a way that produced data.
type Activity struct {
	db  *sql.DB
	loc *time.Location // timezone for "today" calculations (Asia/Kolkata)
}

// NewActivity wraps a database handle with a fixed timezone. The
// timezone determines what counts as "today" — at 11 PM IST it's
// still "today" in India even though the laptop's UTC clock says
// it's tomorrow.
func NewActivity(db *sql.DB, loc *time.Location) *Activity {
	return &Activity{db: db, loc: loc}
}

// MarkLogged records that the user logged something today.
// Idempotent — calling it twice in one day has the same effect
// as calling it once. The UPSERT (ON CONFLICT) syntax is SQLite's
// way of saying "insert if missing, update if present."
func (a *Activity) MarkLogged(ctx context.Context) error {
	today := a.todayString()
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO daily_log_tracker (log_date, has_logged, nudge_sent, updated_at)
		VALUES (?, 1, 0, CURRENT_TIMESTAMP)
		ON CONFLICT(log_date) DO UPDATE
		SET has_logged = 1, updated_at = CURRENT_TIMESTAMP
	`, today)
	if err != nil {
		return fmt.Errorf("marking activity: %w", err)
	}
	return nil
}

// HasLoggedToday returns true if MarkLogged was called today.
// Used by the 8 PM cron job to decide whether to send a nudge.
func (a *Activity) HasLoggedToday(ctx context.Context) (bool, error) {
	today := a.todayString()
	var hasLogged int
	err := a.db.QueryRowContext(ctx,
		`SELECT has_logged FROM daily_log_tracker WHERE log_date = ?`,
		today,
	).Scan(&hasLogged)
	if err == sql.ErrNoRows {
		return false, nil // no row = no activity yet
	}
	if err != nil {
		return false, fmt.Errorf("querying activity: %w", err)
	}
	return hasLogged == 1, nil
}

// MarkNudgeSent records that the 8 PM nudge already fired today,
// so we don't accidentally double-send if the cron retriggers.
func (a *Activity) MarkNudgeSent(ctx context.Context) error {
	today := a.todayString()
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO daily_log_tracker (log_date, has_logged, nudge_sent, updated_at)
		VALUES (?, 0, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(log_date) DO UPDATE
		SET nudge_sent = 1, updated_at = CURRENT_TIMESTAMP
	`, today)
	return err
}

// NudgeAlreadySent returns true if the 8 PM nudge already fired today.
func (a *Activity) NudgeAlreadySent(ctx context.Context) (bool, error) {
	today := a.todayString()
	var sent int
	err := a.db.QueryRowContext(ctx,
		`SELECT nudge_sent FROM daily_log_tracker WHERE log_date = ?`,
		today,
	).Scan(&sent)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return sent == 1, nil
}

// todayString returns today's date in the configured timezone as
// "YYYY-MM-DD" — matching the schema's DATE format.
func (a *Activity) todayString() string {
	return time.Now().In(a.loc).Format("2006-01-02")
}
