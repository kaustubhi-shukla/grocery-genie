// Read-only analytics queries that run off whatever data is already
// in the DB. None of these call out to Gemini — they're pure SQL —
// so they keep working when the daily AI quota is exhausted, and
// they cost nothing to invoke.
//
// Why this file exists separately from inventory.go: the operations
// here read across many rows and produce dashboards, whereas
// inventory.go writes single transactions. Keeping them apart makes
// it obvious which functions mutate state and which only summarise.

package tools

import (
	"context"
	"database/sql"
	"fmt"
)

// StockEntry is one row in the /stock report — what we have on hand
// for a single item plus the depletion class the bot will use to
// decide whether to nudge for a reorder.
type StockEntry struct {
	ItemName     string
	Category     string
	Quantity     float64
	Unit         string
	TrackingType string
	Status       StockStatus // computed from quantity / tracking_type
}

// StockStatus is the three-bucket classification we promised in the
// PRD: REORDER NOW for items at or near zero, LOW for items running
// down, OK for everything else. For estimated items where stock is
// "current bottle" rather than an exact level, the status falls back
// to OK until Phase 2 wires in the purchase-interval-based logic.
type StockStatus string

const (
	StatusReorderNow StockStatus = "REORDER"
	StatusLow        StockStatus = "LOW"
	StatusOK         StockStatus = "OK"
)

// CurrentStock returns the inventory for all items that have a row in
// the inventory table, sorted by category and item name. Items with
// is_freebie purchases (cutlery, glass jars) still appear because the
// household physically has them, but they're flagged so future Phase
// 3/4 logic can opt out.
func (inv *Inventory) CurrentStock(ctx context.Context) ([]StockEntry, error) {
	rows, err := inv.db.QueryContext(ctx, `
		SELECT
			i.name,
			i.category,
			inv.quantity,
			i.unit,
			i.tracking_type
		FROM inventory inv
		JOIN items i ON i.id = inv.item_id
		ORDER BY i.category, i.name
	`)
	if err != nil {
		return nil, fmt.Errorf("loading stock: %w", err)
	}
	defer rows.Close()

	var results []StockEntry
	for rows.Next() {
		var e StockEntry
		if err := rows.Scan(&e.ItemName, &e.Category, &e.Quantity, &e.Unit, &e.TrackingType); err != nil {
			return nil, err
		}
		e.Status = classifyStock(e)
		results = append(results, e)
	}
	return results, rows.Err()
}

// classifyStock applies the Phase 1.5 cutoffs. Phase 2 will replace
// this with a richer per-item-per-week depletion calculation once we
// have usage data; the simple thresholds below are good enough to
// be useful today off purchase-only data.
func classifyStock(e StockEntry) StockStatus {
	// Estimated items (oil, ghee, rice, atta) keep "current bottle"
	// semantics — we never decrement them on usage so we can't say
	// they're low from stock alone. Status will be set by interval
	// math in Phase 2.
	if e.TrackingType == "estimated" {
		return StatusOK
	}
	switch {
	case e.Quantity <= 0:
		return StatusReorderNow
	case e.Quantity <= 1:
		return StatusLow
	default:
		return StatusOK
	}
}

// SpendBreakdown is the /spend dashboard: totals plus three views
// (by platform, by payment method, by category). Freebies are
// excluded from every total — they aren't money the household spent.
type SpendBreakdown struct {
	TotalSpend       float64
	TotalPurchases   int
	ByPlatform       []SpendGroup // sorted desc by spend
	ByPaymentMethod  []SpendGroup
	ByCategory       []SpendGroup
	FreebieCount     int
	FreebieListPrice float64 // sum of "would have cost" if the items hadn't been free
}

// SpendGroup is one row in a breakdown — a category/platform/payment
// label with its row count and spend.
type SpendGroup struct {
	Label      string
	ItemCount  int
	SpendRupee float64
}

// SpendSummary computes the /spend breakdown across the full DB.
// All money figures EXCLUDE freebies (is_freebie = 1) so we never
// pretend a complimentary jar was a real Rs 499 of grocery spend.
// The freebie count + list-price total is surfaced separately for
// curiosity ("how much value did FirstClub give away to me?").
func (inv *Inventory) SpendSummary(ctx context.Context) (*SpendBreakdown, error) {
	b := &SpendBreakdown{}

	// Totals (paid only).
	err := inv.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(price), 0),
			COUNT(*)
		FROM purchases
		WHERE is_freebie = 0
	`).Scan(&b.TotalSpend, &b.TotalPurchases)
	if err != nil {
		return nil, fmt.Errorf("computing total spend: %w", err)
	}

	// Freebie tally (separate, for fun).
	err = inv.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(price), 0)
		FROM purchases
		WHERE is_freebie = 1
	`).Scan(&b.FreebieCount, &b.FreebieListPrice)
	if err != nil {
		return nil, fmt.Errorf("computing freebies: %w", err)
	}

	// Three breakdowns, all paid-only.
	b.ByPlatform, err = inv.spendGroupedBy(ctx,
		`COALESCE(p.platform, '(unknown)')`, 10)
	if err != nil {
		return nil, fmt.Errorf("by platform: %w", err)
	}

	b.ByPaymentMethod, err = inv.spendGroupedBy(ctx,
		`COALESCE(p.payment_method, '(unknown)')`, 10)
	if err != nil {
		return nil, fmt.Errorf("by payment: %w", err)
	}

	b.ByCategory, err = inv.spendGroupedBy(ctx,
		`COALESCE(i.category, '(uncategorised)')`, 15)
	if err != nil {
		return nil, fmt.Errorf("by category: %w", err)
	}

	return b, nil
}

// spendGroupedBy returns spend + item count grouped by the given
// expression, sorted desc by spend. Always excludes freebies. Limit
// caps the number of rows (we never want a 50-platform list dumped
// to chat).
func (inv *Inventory) spendGroupedBy(ctx context.Context, groupExpr string, limit int) ([]SpendGroup, error) {
	query := fmt.Sprintf(`
		SELECT %s AS label,
		       COUNT(*) AS items,
		       COALESCE(SUM(p.price), 0) AS spend
		FROM purchases p
		JOIN items i ON i.id = p.item_id
		WHERE p.is_freebie = 0
		GROUP BY label
		ORDER BY spend DESC
		LIMIT ?
	`, groupExpr)

	rows, err := inv.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SpendGroup
	for rows.Next() {
		var g SpendGroup
		if err := rows.Scan(&g.Label, &g.ItemCount, &g.SpendRupee); err != nil {
			return nil, err
		}
		results = append(results, g)
	}
	return results, rows.Err()
}

// PlatformPrice is one platform's experience with one item — what
// you typically pay there per unit, how often you've bought it,
// and what the cheapest/most expensive paid experiences were.
type PlatformPrice struct {
	Platform        string
	PurchaseCount   int
	AvgPricePerUnit float64
	MinPricePerUnit float64
	MaxPricePerUnit float64
	Unit            string
}

// ItemPriceComparison is the /compare response — every platform from
// which the household has bought a given item, ranked cheapest
// avg-price-per-unit first. Freebies are excluded so a complimentary
// jar doesn't make a platform look like it sells for Rs 0/kg.
type ItemPriceComparison struct {
	ItemName  string
	Platforms []PlatformPrice
}

// CompareItem returns the platform-by-platform price experience for
// items whose name matches the given query (case-insensitive
// substring). Matches multiple items if the query is vague — the
// caller decides how to present them. Returns an empty slice if
// nothing matches.
func (inv *Inventory) CompareItem(ctx context.Context, query string) ([]ItemPriceComparison, error) {
	// Resolve query to one or more item ids.
	itemRows, err := inv.db.QueryContext(ctx, `
		SELECT id, name, unit FROM items WHERE LOWER(name) LIKE LOWER(?)
		ORDER BY name LIMIT 5
	`, "%"+query+"%")
	if err != nil {
		return nil, fmt.Errorf("matching items: %w", err)
	}

	type matchedItem struct {
		id   int64
		name string
		unit string
	}
	var matches []matchedItem
	for itemRows.Next() {
		var m matchedItem
		if err := itemRows.Scan(&m.id, &m.name, &m.unit); err != nil {
			itemRows.Close()
			return nil, err
		}
		matches = append(matches, m)
	}
	itemRows.Close()

	results := make([]ItemPriceComparison, 0, len(matches))
	for _, it := range matches {
		platforms, err := inv.platformsForItem(ctx, it.id, it.unit)
		if err != nil {
			return nil, err
		}
		if len(platforms) == 0 {
			continue // skip items where every purchase was a freebie or had no price
		}
		results = append(results, ItemPriceComparison{
			ItemName:  it.name,
			Platforms: platforms,
		})
	}
	return results, nil
}

// platformsForItem is the per-item helper for CompareItem. Returns
// platforms sorted by avg price-per-unit ascending (cheapest first).
func (inv *Inventory) platformsForItem(ctx context.Context, itemID int64, unit string) ([]PlatformPrice, error) {
	rows, err := inv.db.QueryContext(ctx, `
		SELECT
			COALESCE(platform, '(unknown)'),
			COUNT(*),
			AVG(price_per_unit),
			MIN(price_per_unit),
			MAX(price_per_unit)
		FROM purchases
		WHERE item_id = ?
		  AND is_freebie = 0
		  AND price_per_unit IS NOT NULL
		GROUP BY platform
		ORDER BY AVG(price_per_unit) ASC
	`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PlatformPrice
	for rows.Next() {
		var pp PlatformPrice
		// MIN/MAX/AVG can be NULL when GROUP has rows where price_per_unit IS NULL,
		// but our WHERE already excludes those. Even so, scan into nullable for safety.
		var avg, min, max sql.NullFloat64
		if err := rows.Scan(&pp.Platform, &pp.PurchaseCount, &avg, &min, &max); err != nil {
			return nil, err
		}
		if !avg.Valid {
			continue
		}
		pp.AvgPricePerUnit = avg.Float64
		pp.MinPricePerUnit = min.Float64
		pp.MaxPricePerUnit = max.Float64
		pp.Unit = unit
		results = append(results, pp)
	}
	return results, rows.Err()
}
