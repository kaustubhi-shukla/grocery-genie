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
	"io"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/kaustubhi-shukla/grocery-genie/internal/database"
	"github.com/kaustubhi-shukla/grocery-genie/internal/telegram"
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

	// Open the database. We will start using it in step 7 when we log
	// the first real purchases.
	db, err := database.Open("grocery.db")
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	// Build the bot.
	bot, err := telegram.New(token)
	if err != nil {
		log.Fatalf("creating telegram bot: %v", err)
	}

	log.Println("GroceryGenie starting up — press Ctrl+C to stop")
	// bot.Start() blocks forever. Anything below this line never runs
	// until the bot stops.
	bot.Start()
}
