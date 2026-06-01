// Package tools contains the Go functions that agents call to read
// and write the database. Each tool is a small, well-named operation
// (add a purchase, deduct stock, log waste). Agents pick the right
// tool based on user intent — but the tools themselves know nothing
// about AI or chat. That separation makes them easy to test.
//
// The "tools" terminology comes from agent frameworks (ADK, OpenAI
// function calling, LangChain). Each tool is what the AI "can do".
package tools

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kaustubhi-shukla/grocery-genie/internal/agents"
)

// Inventory is the toolset for stock-related operations.
// All functions take a context so callers can apply timeouts.
type Inventory struct {
	db *sql.DB
}

// NewInventory wraps a database handle as an Inventory toolset.
func NewInventory(db *sql.DB) *Inventory {
	return &Inventory{db: db}
}

// SavedPurchase summarises what was written to the DB after a
// confirmed order — used to build the success message to the user.
type SavedPurchase struct {
	ItemName     string
	Quantity     float64
	Unit         string
	TrackingType string
	NewStock     float64 // current stock level after this purchase (only for countable items)
}

// SaveConfirmedOrder writes a confirmed receipt to the database in a
// single transaction. For each item:
//
//  1. Look up (or create) the master record in `items`
//  2. Insert a row in `purchases` with platform + payment method
//  3. For countable items, increment `inventory.quantity`
//     For estimated items, set `inventory.quantity` to the purchase
//     quantity (we do not decrement these on usage, so it represents
//     the most recent restock — useful for relative quantity math
//     like "half the bottle used")
//
// Returns one SavedPurchase per item so the bot can render a friendly
// confirmation message ("carrots: 4 in stock, basmati rice: 2kg").
func (inv *Inventory) SaveConfirmedOrder(
	ctx context.Context,
	items []agents.ReceiptItem,
	platform string,
	paymentMethod string,
	receiptImageRef string,
) ([]SavedPurchase, error) {
	tx, err := inv.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback() // safe — Commit() makes this a no-op

	results := make([]SavedPurchase, 0, len(items))
	now := time.Now()

	for _, item := range items {
		// 1) Resolve item_id: find by name (case-insensitive) or create.
		itemID, err := getOrCreateItem(ctx, tx, item)
		if err != nil {
			return nil, fmt.Errorf("resolving item %q: %w", item.Name, err)
		}

		// 2) Insert the purchase row. price_per_unit is computed for
		//    cross-platform comparison later (Phase 3 feature).
		var pricePerUnit *float64
		if item.Price > 0 && item.Quantity > 0 {
			ppu := item.Price / item.Quantity
			pricePerUnit = &ppu
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO purchases (
				item_id, quantity, price, price_per_unit,
				platform, payment_method, receipt_image,
				is_confirmed, purchased_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			itemID, item.Quantity, nullable(item.Price), pricePerUnit,
			platform, paymentMethod, receiptImageRef, now,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting purchase for %q: %w", item.Name, err)
		}

		// 3) Update inventory. The behavior differs by tracking type.
		newStock, err := upsertInventory(ctx, tx, itemID, item)
		if err != nil {
			return nil, fmt.Errorf("updating inventory for %q: %w", item.Name, err)
		}

		results = append(results, SavedPurchase{
			ItemName:     item.Name,
			Quantity:     item.Quantity,
			Unit:         item.Unit,
			TrackingType: item.TrackingType,
			NewStock:     newStock,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}
	return results, nil
}

// getOrCreateItem looks up an item by lowercase name, or inserts it
// if missing. Returns the row's id. Idempotent: calling twice with
// the same item name returns the same id.
func getOrCreateItem(ctx context.Context, tx *sql.Tx, item agents.ReceiptItem) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM items WHERE LOWER(name) = LOWER(?)`,
		item.Name,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	// Insert new item. Default missing fields to safe values.
	category := item.Category
	if category == "" {
		category = "other"
	}
	unit := item.Unit
	if unit == "" {
		unit = "pieces"
	}
	trackingType := item.TrackingType
	if trackingType != "estimated" {
		trackingType = "countable" // schema default
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO items (name, category, unit, tracking_type)
		 VALUES (LOWER(?), ?, ?, ?)`,
		item.Name, category, unit, trackingType,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// upsertInventory updates (or creates) the inventory row for an item
// and returns the resulting on-hand quantity. For countable items we
// add to existing stock; for estimated items we replace it (the
// "current bottle" semantics — see schema.sql for the rationale).
func upsertInventory(ctx context.Context, tx *sql.Tx, itemID int64, item agents.ReceiptItem) (float64, error) {
	var existing float64
	err := tx.QueryRowContext(ctx,
		`SELECT quantity FROM inventory WHERE item_id = ?`,
		itemID,
	).Scan(&existing)

	var newQty float64
	switch {
	case err == sql.ErrNoRows:
		// First time we've seen this item — initialize.
		newQty = item.Quantity
		_, err = tx.ExecContext(ctx,
			`INSERT INTO inventory (item_id, quantity) VALUES (?, ?)`,
			itemID, newQty,
		)
	case err != nil:
		return 0, err
	default:
		// Existing row — apply tracking-type-specific update.
		if item.TrackingType == "estimated" {
			newQty = item.Quantity // replace: this is the new bottle/bag
		} else {
			newQty = existing + item.Quantity // countable: add to stock
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE inventory SET quantity = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE item_id = ?`,
			newQty, itemID,
		)
	}
	if err != nil {
		return 0, err
	}
	return newQty, nil
}

// nullable returns a *float64 that's nil when the value is 0 — used
// for INSERTing NULL into the DB instead of literal zero, which keeps
// "we don't know the price" distinct from "the price is zero rupees."
func nullable(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}
