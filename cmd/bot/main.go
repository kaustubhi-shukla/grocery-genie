// Package main is the entry point for the GroceryGenie Telegram bot.
//
// Current state (Phase 1, step 5): loads .env, opens the SQLite database,
// applies the schema, and prints the list of tables to confirm everything
// is wired up. No bot logic yet — that comes in step 6.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/kaustubhi-shukla/grocery-genie/internal/database"
)

func main() {
	// Load environment variables from .env into the process environment.
	// In production we would use real env vars set by the deployment
	// platform (e.g., Cloud Run). For local dev, .env is convenient.
	if err := godotenv.Load(); err != nil {
		log.Println("note: no .env file found, relying on real environment")
	}

	// Verify required keys are present. We do not USE them yet — but we
	// fail fast if they're missing so the user knows to configure them.
	if os.Getenv("TELEGRAM_BOT_KEY") == "" {
		log.Fatal("TELEGRAM_BOT_KEY is not set (check your .env file)")
	}
	if os.Getenv("GEMINI_API_KEY") == "" {
		log.Fatal("GEMINI_API_KEY is not set (check your .env file)")
	}

	// Open (or create) the database file.
	db, err := database.Open("grocery.db")
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close() // ensures the database is closed when main() returns

	// Query the list of tables that exist in the database, so we can
	// confirm the schema was applied. sqlite_master is SQLite's
	// built-in metadata table that lists every table, index, etc.
	rows, err := db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name;
	`)
	if err != nil {
		log.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()

	fmt.Println("GroceryGenie: database ready ✓")
	fmt.Println("Tables in grocery.db:")

	tableCount := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			log.Fatalf("reading row: %v", err)
		}
		fmt.Printf("  • %s\n", name)
		tableCount++
	}

	fmt.Printf("Total: %d tables\n", tableCount)
}
