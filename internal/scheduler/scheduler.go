// Package scheduler owns the bot's recurring background jobs.
//
// Today (Phase 1): just the 8 PM IST daily logging nudge.
// Later phases will add: Saturday morning reorder report, subscription
// "due soon" alerts, monthly budget summary, etc.
//
// We use robfig/cron — a single-process Go cron library. No external
// services. All schedules run in the bot's address space, sharing the
// same DB handle and Telegram client.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kaustubhi-shukla/grocery-genie/internal/tools"
)

// Notifier abstracts "send a message to the owner". The Telegram bot
// implements this — the scheduler doesn't need to know about Telegram
// types, which keeps cron logic testable in isolation.
type Notifier interface {
	NotifyOwner(ctx context.Context, text string) error
}

// Scheduler wires cron schedules to their actions. Build one in main(),
// call Start() to begin firing, and call Stop() on shutdown.
type Scheduler struct {
	cron     *cron.Cron
	activity *tools.Activity
	notifier Notifier
}

// New constructs a Scheduler bound to the given Indian Standard Time
// (or whatever timezone you pass). All cron expressions are interpreted
// in that timezone — so "every day at 20:00" means 8 PM IST when loc
// is Asia/Kolkata, regardless of the laptop's clock.
func New(loc *time.Location, activity *tools.Activity, notifier Notifier) *Scheduler {
	c := cron.New(cron.WithLocation(loc))
	return &Scheduler{
		cron:     c,
		activity: activity,
		notifier: notifier,
	}
}

// Start registers all schedules and begins firing.
// Safe to call from main() — Start() doesn't block.
func (s *Scheduler) Start() error {
	// Cron expression "0 20 * * *" reads as:
	//   minute=0, hour=20, day=*, month=*, day-of-week=*
	// i.e. every day at 20:00 (8:00 PM) in the configured timezone.
	if _, err := s.cron.AddFunc("0 20 * * *", s.runDailyNudge); err != nil {
		return fmt.Errorf("registering daily nudge: %w", err)
	}

	s.cron.Start()
	log.Printf("scheduler started; daily nudge will fire at 20:00 in configured timezone")
	return nil
}

// Stop halts the cron runner. Any in-flight job runs to completion.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop() // returns a context that closes when jobs finish
	<-ctx.Done()
}

// runDailyNudge is the job that fires at 8 PM. It checks whether the
// owner logged anything today, and only nudges if they didn't. Also
// records the nudge so we never double-send.
func (s *Scheduler) runDailyNudge() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	already, err := s.activity.NudgeAlreadySent(ctx)
	if err != nil {
		log.Printf("nudge: checking sent status: %v", err)
		return
	}
	if already {
		log.Println("nudge: already sent today, skipping")
		return
	}

	logged, err := s.activity.HasLoggedToday(ctx)
	if err != nil {
		log.Printf("nudge: checking activity: %v", err)
		return
	}
	if logged {
		log.Println("nudge: user already logged today, not nudging")
		return
	}

	msg := `🌙 *Daily logging nudge*

I noticed you haven't logged anything today — no purchases, no meals.

A quick reminder before you wind down: even if it's just one item, logging keeps the bot useful. Send a receipt photo or just say what you cooked!

_(I send this once a day at 8 PM if you've gone quiet. Reply /nonudge if you'd rather not get these.)_`

	if err := s.notifier.NotifyOwner(ctx, msg); err != nil {
		log.Printf("nudge: send failed: %v", err)
		return
	}
	if err := s.activity.MarkNudgeSent(ctx); err != nil {
		log.Printf("nudge: marking sent: %v", err)
	}
	log.Println("✓ daily nudge sent")
}
