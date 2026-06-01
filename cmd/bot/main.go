// Package main is the entry point for the GroceryGenie Telegram bot.
//
// Current state (Phase 1, step 6): loads .env, opens SQLite, then starts
// the Telegram bot. The bot can echo messages and acknowledge photos.
// Real AI parsing arrives in step 7.
//
// Run with:   go run ./cmd/bot/
// Stop with:  Ctrl+C
package main

import (
	"context"
	"io"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/kaustubhi-shukla/grocery-genie/internal/agents"
	"github.com/kaustubhi-shukla/grocery-genie/internal/database"
	"github.com/kaustubhi-shukla/grocery-genie/internal/telegram"
	"github.com/kaustubhi-shukla/grocery-genie/internal/tools"
)

func main() {
	// Mirror log output to both stdout and bot.log so we can tail it
	// from another terminal. The log package itself does not buffer —
	// each log.Printf call writes immediately to all writers below.
	logFile, err := os.OpenFile("bot.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("opening log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

	// Load environment variables from .env into the process environment.
	if err := godotenv.Load(); err != nil {
		log.Println("note: no .env file found, relying on real environment")
	}

	// Pull required keys from the environment. We fail fast if anything
	// is missing so the operator gets a clear error, not a mysterious
	// crash deep inside the bot.
	token := os.Getenv("TELEGRAM_BOT_KEY")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_KEY is not set (check your .env file)")
	}
	if os.Getenv("GEMINI_API_KEY") == "" {
		log.Fatal("GEMINI_API_KEY is not set (check your .env file)")
	}

	// Open the database. We will start using it in step 8 when we log
	// confirmed purchases into the inventory.
	db, err := database.Open("grocery.db")
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	// Build the Receipt Agent — wraps Gemini Vision. ctx is the root
	// context for the SDK; individual scans wrap their own timeouts.
	ctx := context.Background()
	receiptAgent, err := agents.NewReceiptAgent(ctx, os.Getenv("GEMINI_API_KEY"))
	if err != nil {
		log.Fatalf("creating receipt agent: %v", err)
	}
	defer receiptAgent.Close()

	// Inventory toolset — turns DB writes into discrete operations
	// the bot can call (SaveConfirmedOrder, future stock queries).
	inventory := tools.NewInventory(db)

	// Session store keeps in-progress orders in memory between the
	// initial scan and the final payment confirmation. 10-min TTL —
	// if user goes silent that long, the order is dropped.
	sessions := telegram.NewSessionStore(10 * time.Minute)
	defer sessions.Close()

	// Build the bot, passing everything its handlers need.
	bot, err := telegram.New(token, receiptAgent, inventory, sessions)
	if err != nil {
		log.Fatalf("creating telegram bot: %v", err)
	}

	log.Println("GroceryGenie starting up — press Ctrl+C to stop")
	// bot.Start() blocks forever. Anything below this line never runs
	// until the bot stops.
	bot.Start()
}
