-- GroceryGenie database schema (SQLite)
--
-- This file is the single source of truth for our database structure.
-- Every CREATE TABLE uses "IF NOT EXISTS" so this file can be run on
-- every bot startup without overwriting existing data.
--
-- Reference: docs/TECHNICAL_DESIGN.md section 2

-- =============================================================
-- TABLE: items
-- The master list of every grocery item the household tracks.
-- The tracking_type field is critical: it determines whether we
-- decrement stock on usage (countable) or only track via purchase
-- intervals (estimated).
-- =============================================================
CREATE TABLE IF NOT EXISTS items (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL UNIQUE,
    category      TEXT NOT NULL,                           -- "vegetable", "grain", "spice", "dairy", "fruit"
    unit          TEXT NOT NULL,                           -- "pieces", "kg", "g", "litres", "ml", "packets"
    tracking_type TEXT NOT NULL DEFAULT 'countable'        -- "countable" or "estimated"
                  CHECK (tracking_type IN ('countable', 'estimated')),
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: inventory
-- Current stock level per item. Only meaningful for COUNTABLE items.
-- For estimated items, quantity is set on purchase but not decremented
-- on usage (since user does not specify usage quantity).
-- =============================================================
CREATE TABLE IF NOT EXISTS inventory (
    item_id    INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    quantity   REAL NOT NULL DEFAULT 0,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: purchases
-- Every purchase event (from receipt scans or manual input).
-- price_per_unit is computed (price / quantity) for cross-platform
-- price comparison (Phase 3).
-- =============================================================
CREATE TABLE IF NOT EXISTS purchases (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id         INTEGER NOT NULL REFERENCES items(id),
    quantity        REAL NOT NULL,
    price           REAL,                                  -- price paid (nullable)
    price_per_unit  REAL,                                  -- computed: price/quantity, for cross-platform comparison
    platform        TEXT,                                  -- "FirstClub", "Zepto", "BigBasket", etc.
    payment_method  TEXT,                                  -- "UPI", "Credit Card", "Cash"
    receipt_image   TEXT,                                  -- Telegram file_id of receipt photo
    is_confirmed    BOOLEAN NOT NULL DEFAULT 0,            -- user confirmed platform/details
    is_guest_event  BOOLEAN NOT NULL DEFAULT 0,            -- TRUE = party/festival purchase, excluded from interval calc
    purchased_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: usage
-- Every usage or waste event (from meal logging).
-- quantity_type distinguishes: "exact" (2 carrots), "relative" (half
-- the bottle), "none" (just "used oil" with no amount).
-- waste_source_purchase links waste back to the purchase, enabling
-- platform quality scoring in Phase 4.
-- =============================================================
CREATE TABLE IF NOT EXISTS usage (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id               INTEGER NOT NULL REFERENCES items(id),
    quantity              REAL,                            -- NULL when quantity_type = 'none'
    quantity_type         TEXT NOT NULL DEFAULT 'exact'    -- 'exact', 'relative', 'none'
                          CHECK (quantity_type IN ('exact', 'relative', 'none')),
    relative_amount       REAL,                            -- for 'relative': 0.5 = half, 0.9 = almost finished
    recipe_name           TEXT,                            -- nullable
    is_waste              BOOLEAN NOT NULL DEFAULT 0,
    waste_reason          TEXT,                            -- "went bad", "expired", nullable
    waste_source_purchase INTEGER REFERENCES purchases(id),-- links waste to the purchase it came from
    is_guest_usage        BOOLEAN NOT NULL DEFAULT 0,      -- TRUE = guest/party usage, excluded from baseline forecasting
    used_at               TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: recipes
-- Recipes the bot has learned from user messages over time.
-- =============================================================
CREATE TABLE IF NOT EXISTS recipes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    times_made INTEGER NOT NULL DEFAULT 1,
    last_made  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: recipe_ingredients
-- Ingredients per recipe with averaged quantities. Composite key
-- (recipe_id, item_id) means each ingredient appears once per recipe.
-- =============================================================
CREATE TABLE IF NOT EXISTS recipe_ingredients (
    recipe_id    INTEGER NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    item_id      INTEGER NOT NULL REFERENCES items(id),
    avg_quantity REAL NOT NULL,
    unit         TEXT NOT NULL,
    PRIMARY KEY (recipe_id, item_id)
);

-- =============================================================
-- TABLE: outside_meals
-- Meals ordered from outside (Zomato, Swiggy, restaurants).
-- Tracked separately from homecooked meals (Phase 2 + Phase 5).
-- =============================================================
CREATE TABLE IF NOT EXISTS outside_meals (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    meal_name  TEXT NOT NULL,
    platform   TEXT,                                       -- "Zomato", "Swiggy", restaurant name
    cost       REAL,
    notes      TEXT,
    meal_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: daily_log_tracker
-- One row per day. Used by the 8 PM nudge: at 8 PM IST, check if
-- has_logged is FALSE for today, and if so send a Telegram reminder.
-- =============================================================
CREATE TABLE IF NOT EXISTS daily_log_tracker (
    log_date   DATE PRIMARY KEY,
    has_logged BOOLEAN NOT NULL DEFAULT 0,
    nudge_sent BOOLEAN NOT NULL DEFAULT 0,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- TABLE: guest_events
-- Guest/party events (dinner parties, festivals). These dates are
-- excluded from baseline forecasting so spikes do not corrupt
-- the regular purchase intervals.
-- =============================================================
CREATE TABLE IF NOT EXISTS guest_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    guest_count INTEGER NOT NULL,
    occasion    TEXT,                                      -- "dinner party", "Diwali", "birthday"
    event_date  DATE NOT NULL,
    notes       TEXT
);

-- =============================================================
-- TABLE: subscriptions
-- Auto-detected purchase patterns. For ESTIMATED items this is the
-- PRIMARY restock signal. For COUNTABLE items it supplements
-- depletion-based alerts. Updated after every confirmed purchase.
-- =============================================================
CREATE TABLE IF NOT EXISTS subscriptions (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id            INTEGER NOT NULL UNIQUE REFERENCES items(id) ON DELETE CASCADE,
    avg_interval_days  REAL NOT NULL,
    avg_quantity       REAL NOT NULL,
    preferred_platform TEXT,
    confidence         REAL NOT NULL DEFAULT 0,            -- 0 to 1
    next_order_date    DATE,
    is_guest_adjusted  BOOLEAN NOT NULL DEFAULT 0,         -- TRUE if guest purchases were excluded from interval calc
    updated_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================
-- INDEXES
-- Indexes speed up common queries. Without an index, the database
-- has to scan every row. With an index, it can jump directly to
-- the right rows. We index columns we will filter or join on often.
-- =============================================================
CREATE INDEX IF NOT EXISTS idx_purchases_item_date ON purchases(item_id, purchased_at);
CREATE INDEX IF NOT EXISTS idx_purchases_platform  ON purchases(platform);
CREATE INDEX IF NOT EXISTS idx_usage_item_date     ON usage(item_id, used_at);
CREATE INDEX IF NOT EXISTS idx_usage_waste         ON usage(is_waste);
CREATE INDEX IF NOT EXISTS idx_outside_meals_date  ON outside_meals(meal_at);
