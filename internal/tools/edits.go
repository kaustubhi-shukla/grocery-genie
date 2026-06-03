package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrPurchaseNotFound is returned when an edit command references a
// purchase ID that doesn't exist (typo, deleted record, etc.).
var ErrPurchaseNotFound = errors.New("purchase not found")

// PurchaseMatch is a row returned by FindPurchases — enough info for
// the user to identify the row they want to edit.
type PurchaseMatch struct {
	PurchaseID    int64
	ItemName      string
	Quantity      float64
	Unit          string
	Price         float64 // 0 if NULL in DB
	HasPrice      bool    // distinguishes 0 (explicit) from NULL (unknown)
	Platform      string
	PaymentMethod string
	IsFreebie     bool
}

// FindPurchases returns up to limit purchases whose item name contains
// the query (case-insensitive substring). Newest first. Used by the
// /find command so the user can discover purchase IDs to edit.
func (inv *Inventory) FindPurchases(ctx context.Context, query string, limit int) ([]PurchaseMatch, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	pattern := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := inv.db.QueryContext(ctx, `
		SELECT p.id, i.name, p.quantity, i.unit,
		       COALESCE(p.price, 0) AS price,
		       p.price IS NOT NULL AS has_price,
		       COALESCE(p.platform,'(unknown)'),
		       COALESCE(p.payment_method,'(unknown)'),
		       p.is_freebie
		FROM purchases p
		JOIN items i ON i.id = p.item_id
		WHERE LOWER(i.name) LIKE ?
		ORDER BY p.id DESC
		LIMIT ?
	`, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("searching purchases: %w", err)
	}
	defer rows.Close()

	var results []PurchaseMatch
	for rows.Next() {
		var m PurchaseMatch
		var hasPrice int
		var isFreebie int
		if err := rows.Scan(&m.PurchaseID, &m.ItemName, &m.Quantity, &m.Unit,
			&m.Price, &hasPrice, &m.Platform, &m.PaymentMethod, &isFreebie); err != nil {
			return nil, err
		}
		m.HasPrice = hasPrice == 1
		m.IsFreebie = isFreebie == 1
		results = append(results, m)
	}
	return results, rows.Err()
}

// MarkAsFreebie flips a purchase to is_freebie=1, clears price and
// price_per_unit so price math ignores it. Inventory is unchanged —
// the household still has the item, we just don't pretend they paid.
func (inv *Inventory) MarkAsFreebie(ctx context.Context, purchaseID int64) (*PurchaseMatch, error) {
	res, err := inv.db.ExecContext(ctx, `
		UPDATE purchases
		   SET is_freebie = 1, price = NULL, price_per_unit = NULL
		 WHERE id = ?
	`, purchaseID)
	if err != nil {
		return nil, fmt.Errorf("marking freebie: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrPurchaseNotFound
	}
	return inv.fetchOne(ctx, purchaseID)
}

// SetPurchasePrice updates the price (and price_per_unit) for one
// purchase. Use 0 to mean "explicitly free" — also flips is_freebie.
func (inv *Inventory) SetPurchasePrice(ctx context.Context, purchaseID int64, newPrice float64) (*PurchaseMatch, error) {
	tx, err := inv.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var qty float64
	err = tx.QueryRowContext(ctx, `SELECT quantity FROM purchases WHERE id = ?`, purchaseID).Scan(&qty)
	if err == sql.ErrNoRows {
		return nil, ErrPurchaseNotFound
	}
	if err != nil {
		return nil, err
	}

	var pricePerUnit *float64
	if newPrice > 0 && qty > 0 {
		ppu := newPrice / qty
		pricePerUnit = &ppu
	}
	isFreebie := newPrice == 0

	_, err = tx.ExecContext(ctx, `
		UPDATE purchases
		   SET price = ?, price_per_unit = ?, is_freebie = ?
		 WHERE id = ?
	`, nullable(newPrice), pricePerUnit, boolToInt(isFreebie), purchaseID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return inv.fetchOne(ctx, purchaseID)
}

// SetPurchaseQuantity updates a purchase's quantity AND adjusts the
// inventory by the delta. For countable items inventory increments
// by (newQty - oldQty). For estimated items inventory is set to the
// new quantity (matches the "current bottle" semantics elsewhere).
// price_per_unit is recomputed from the new quantity.
func (inv *Inventory) SetPurchaseQuantity(ctx context.Context, purchaseID int64, newQty float64) (*PurchaseMatch, error) {
	if newQty <= 0 {
		return nil, fmt.Errorf("quantity must be positive (got %g)", newQty)
	}

	tx, err := inv.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var oldQty, price float64
	var itemID int64
	var hasPrice int
	err = tx.QueryRowContext(ctx, `
		SELECT item_id, quantity, COALESCE(price,0), price IS NOT NULL
		  FROM purchases WHERE id = ?
	`, purchaseID).Scan(&itemID, &oldQty, &price, &hasPrice)
	if err == sql.ErrNoRows {
		return nil, ErrPurchaseNotFound
	}
	if err != nil {
		return nil, err
	}

	// Determine tracking type to decide inventory adjustment style.
	var trackingType string
	err = tx.QueryRowContext(ctx, `SELECT tracking_type FROM items WHERE id = ?`, itemID).Scan(&trackingType)
	if err != nil {
		return nil, err
	}

	// Recompute price_per_unit.
	var ppu *float64
	if hasPrice == 1 && price > 0 && newQty > 0 {
		v := price / newQty
		ppu = &v
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE purchases SET quantity = ?, price_per_unit = ? WHERE id = ?
	`, newQty, ppu, purchaseID); err != nil {
		return nil, err
	}

	// Adjust inventory.
	if trackingType == "estimated" {
		// Replace semantics — newQty becomes current stock.
		if _, err = tx.ExecContext(ctx, `
			UPDATE inventory SET quantity = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE item_id = ?
		`, newQty, itemID); err != nil {
			return nil, err
		}
	} else {
		// Countable — adjust by delta. If inventory has no row,
		// initialise to newQty (defensive).
		delta := newQty - oldQty
		var existing float64
		row := tx.QueryRowContext(ctx, `SELECT quantity FROM inventory WHERE item_id = ?`, itemID)
		if err := row.Scan(&existing); err == sql.ErrNoRows {
			if _, err = tx.ExecContext(ctx, `INSERT INTO inventory (item_id, quantity) VALUES (?, ?)`, itemID, newQty); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			if _, err = tx.ExecContext(ctx, `
				UPDATE inventory SET quantity = MAX(0, quantity + ?), updated_at = CURRENT_TIMESTAMP
				 WHERE item_id = ?
			`, delta, itemID); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return inv.fetchOne(ctx, purchaseID)
}

// DeletePurchase removes a purchase row and rolls back its inventory
// contribution (countable: subtract qty floored at 0; estimated:
// leaves inventory as-is, since we don't know what came before this
// purchase).
func (inv *Inventory) DeletePurchase(ctx context.Context, purchaseID int64) (*PurchaseMatch, error) {
	tx, err := inv.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Capture row data BEFORE delete so we can return it and adjust
	// inventory correctly.
	before, err := inv.fetchOneTx(ctx, tx, purchaseID)
	if err != nil {
		return nil, err
	}

	// Look up the tracking type.
	var trackingType string
	if err := tx.QueryRowContext(ctx, `
		SELECT i.tracking_type FROM items i JOIN purchases p ON p.item_id = i.id
		 WHERE p.id = ?
	`, purchaseID).Scan(&trackingType); err != nil {
		return nil, err
	}

	// Delete the purchase.
	if _, err := tx.ExecContext(ctx, `DELETE FROM purchases WHERE id = ?`, purchaseID); err != nil {
		return nil, err
	}

	// Undo inventory for countable items only — estimated items can't
	// be safely rolled back without knowing what the previous bottle
	// was. We leave estimated inventory alone (slight overcounting
	// beats wrong data).
	if trackingType == "countable" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE inventory SET quantity = MAX(0, quantity - ?),
			                     updated_at = CURRENT_TIMESTAMP
			 WHERE item_id = (SELECT item_id FROM items WHERE LOWER(name) = LOWER(?))
		`, before.Quantity, before.ItemName); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return before, nil
}

// fetchOne returns the post-edit state of one purchase. Returns
// ErrPurchaseNotFound if the row doesn't exist.
func (inv *Inventory) fetchOne(ctx context.Context, purchaseID int64) (*PurchaseMatch, error) {
	rows, err := inv.db.QueryContext(ctx, `
		SELECT p.id, i.name, p.quantity, i.unit,
		       COALESCE(p.price, 0),
		       p.price IS NOT NULL,
		       COALESCE(p.platform,'(unknown)'),
		       COALESCE(p.payment_method,'(unknown)'),
		       p.is_freebie
		FROM purchases p JOIN items i ON i.id = p.item_id
		WHERE p.id = ?
	`, purchaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrPurchaseNotFound
	}
	var m PurchaseMatch
	var hasPrice, isFreebie int
	if err := rows.Scan(&m.PurchaseID, &m.ItemName, &m.Quantity, &m.Unit,
		&m.Price, &hasPrice, &m.Platform, &m.PaymentMethod, &isFreebie); err != nil {
		return nil, err
	}
	m.HasPrice = hasPrice == 1
	m.IsFreebie = isFreebie == 1
	return &m, nil
}

// fetchOneTx is fetchOne inside an existing transaction.
func (inv *Inventory) fetchOneTx(ctx context.Context, tx *sql.Tx, purchaseID int64) (*PurchaseMatch, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT p.id, i.name, p.quantity, i.unit,
		       COALESCE(p.price, 0),
		       p.price IS NOT NULL,
		       COALESCE(p.platform,'(unknown)'),
		       COALESCE(p.payment_method,'(unknown)'),
		       p.is_freebie
		FROM purchases p JOIN items i ON i.id = p.item_id
		WHERE p.id = ?
	`, purchaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrPurchaseNotFound
	}
	var m PurchaseMatch
	var hasPrice, isFreebie int
	if err := rows.Scan(&m.PurchaseID, &m.ItemName, &m.Quantity, &m.Unit,
		&m.Price, &hasPrice, &m.Platform, &m.PaymentMethod, &isFreebie); err != nil {
		return nil, err
	}
	m.HasPrice = hasPrice == 1
	m.IsFreebie = isFreebie == 1
	return &m, nil
}
