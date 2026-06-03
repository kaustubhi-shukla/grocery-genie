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

// FreebieCandidate is one purchase the audit thinks MIGHT be a
// freebie based on heuristics, plus the reason. The reason string is
// surfaced to the user so they understand WHY each row was flagged —
// human-in-the-loop review is the design here, not silent auto-mark.
type FreebieCandidate struct {
	PurchaseID int64
	ItemName   string
	Quantity   float64
	Unit       string
	Price      float64
	Platform   string
	Reason     string // explanation shown to the user
}

// FreebieAudit returns purchases that pattern-match the kinds of
// rows a freebie could be hiding inside. It NEVER auto-marks —
// the caller (a Telegram handler) shows them to the user, who
// confirms each with /freebie <id>. The patterns we look for:
//
//  1. Items containing kitchenware/sample/gift keywords (jar, bowl,
//     cutlery, sample, trial, gift, freebie, complimentary, etc.)
//     filtered to exclude food packaging words ("bag" rejected only
//     when it appears in a product brand name to avoid false
//     positives like "popcorn bag" / "coffee bag").
//  2. Items priced at exactly Rs 1 — a common "show as paid" trick.
//  3. Items priced at Rs 0 with is_freebie=0 (which means they
//     genuinely have NO price but weren't yet marked as freebies).
//
// Audit 2 from the manual pass (wild price variance across same item)
// was deliberately dropped — quantity differences dominate the signal
// and produce too many false positives to be useful.
func (inv *Inventory) FreebieAudit(ctx context.Context) ([]FreebieCandidate, error) {
	results := make([]FreebieCandidate, 0)

	// PATTERN 1: kitchenware/sample names. Carefully avoid food terms.
	q1 := `
		SELECT p.id, i.name, p.quantity, i.unit,
		       COALESCE(p.price, 0), COALESCE(p.platform, '(unknown)')
		FROM purchases p JOIN items i ON i.id = p.item_id
		WHERE p.is_freebie = 0 AND (
		    LOWER(i.name) LIKE '%cutlery%'
		 OR LOWER(i.name) LIKE '%spoon%'
		 OR LOWER(i.name) LIKE '%fork%'
		 OR LOWER(i.name) LIKE '%sample%'
		 OR LOWER(i.name) LIKE '%trial%'
		 OR LOWER(i.name) LIKE '%gift%'
		 OR LOWER(i.name) LIKE '%freebie%'
		 OR LOWER(i.name) LIKE '%complimentary%'
		 OR LOWER(i.name) LIKE '%tumbler%'
		 OR LOWER(i.name) LIKE '%coaster%'
		 OR LOWER(i.name) LIKE '%apron%'
		 OR LOWER(i.name) LIKE '%magnet%'
		 OR LOWER(i.name) LIKE '%sticker%'
		 -- jar / bowl / mug / plate need a "not food-package" guard:
		 -- match only when they're standalone or with kitchenware-y
		 -- modifiers (glass, ceramic, steel, breakfast, etc.).
		 OR LOWER(i.name) LIKE '%glass%jar%'
		 OR LOWER(i.name) LIKE '%ceramic%bowl%'
		 OR LOWER(i.name) LIKE '%floral%bowl%'
		 OR LOWER(i.name) LIKE '%steel%lid%'
		 OR LOWER(i.name) LIKE '%coffee%mug%'
		 OR LOWER(i.name) LIKE '%bone china%'
		)
		ORDER BY p.id`

	if err := inv.collectAuditRows(ctx, q1, "looks like kitchenware/sample (name match)", &results); err != nil {
		return nil, err
	}

	// PATTERN 2: priced at exactly Re 1.
	q2 := `
		SELECT p.id, i.name, p.quantity, i.unit,
		       COALESCE(p.price, 0), COALESCE(p.platform, '(unknown)')
		FROM purchases p JOIN items i ON i.id = p.item_id
		WHERE p.is_freebie = 0 AND p.price = 1.0
		ORDER BY p.id`
	if err := inv.collectAuditRows(ctx, q2, `priced exactly Re 1 — common "show-as-paid" trick`, &results); err != nil {
		return nil, err
	}

	// PATTERN 3: priced at Rs 0 with is_freebie still off (data gap).
	q3 := `
		SELECT p.id, i.name, p.quantity, i.unit,
		       COALESCE(p.price, 0), COALESCE(p.platform, '(unknown)')
		FROM purchases p JOIN items i ON i.id = p.item_id
		WHERE p.is_freebie = 0 AND p.price = 0
		ORDER BY p.id`
	if err := inv.collectAuditRows(ctx, q3, "priced Rs 0 but not yet flagged as freebie", &results); err != nil {
		return nil, err
	}

	// PATTERN 4: priced NULL (price was unreadable from receipt).
	q4 := `
		SELECT p.id, i.name, p.quantity, i.unit,
		       0, COALESCE(p.platform, '(unknown)')
		FROM purchases p JOIN items i ON i.id = p.item_id
		WHERE p.is_freebie = 0 AND p.price IS NULL
		ORDER BY p.id`
	if err := inv.collectAuditRows(ctx, q4, "no price recorded — could be a freebie or a scan miss", &results); err != nil {
		return nil, err
	}

	return results, nil
}

func (inv *Inventory) collectAuditRows(ctx context.Context, query, reason string, out *[]FreebieCandidate) error {
	rows, err := inv.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("audit query (%s): %w", reason, err)
	}
	defer rows.Close()
	seen := map[int64]bool{}
	for _, c := range *out {
		seen[c.PurchaseID] = true
	}
	for rows.Next() {
		var c FreebieCandidate
		if err := rows.Scan(&c.PurchaseID, &c.ItemName, &c.Quantity, &c.Unit, &c.Price, &c.Platform); err != nil {
			return err
		}
		if seen[c.PurchaseID] {
			continue // don't double-list a row caught by multiple patterns
		}
		c.Reason = reason
		*out = append(*out, c)
	}
	return rows.Err()
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
