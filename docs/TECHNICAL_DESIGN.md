# Technical Design Document: GroceryGenie

## 1. Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│                     USER LAYER                           │
│  ┌─────────┐  ┌─────────┐                               │
│  │ Owner   │  │ Cook    │   (Telegram accounts)          │
│  │ (you)   │  │ (future)│                                │
│  └────┬────┘  └────┬────┘                                │
│       │            │                                     │
│       ▼            ▼                                     │
│  ┌──────────────────────┐                                │
│  │   Telegram Bot API   │  ← Messaging layer             │
│  └──────────┬───────────┘    (swappable to WhatsApp)     │
│             │                                            │
├─────────────┼────────────────────────────────────────────┤
│             ▼            APPLICATION LAYER               │
│  ┌──────────────────────────────────────────────┐        │
│  │          Google ADK (Agent Framework)         │        │
│  │                                              │        │
│  │  ┌─────────────────────────────────────────┐ │        │
│  │  │  Root Agent: GroceryGenie               │ │        │
│  │  │  (routes to specialized sub-agents)     │ │        │
│  │  └──┬──────────┬───────────┬───────────┬───┘ │        │
│  │     │          │           │           │      │        │
│  │     ▼          ▼           ▼           ▼      │        │
│  │  ┌───────┐ ┌────────┐ ┌────────┐ ┌────────┐  │        │
│  │  │Receipt│ │Usage   │ │Stock   │ │Budget  │  │        │
│  │  │Agent  │ │Agent   │ │Agent   │ │Agent   │  │        │
│  │  │(scan) │ │(parse) │ │(query) │ │(track) │  │        │
│  │  └──┬────┘ └──┬─────┘ └──┬─────┘ └──┬─────┘  │        │
│  │     │         │          │          │         │        │
│  └─────┼─────────┼──────────┼──────────┼─────────┘        │
│        │         │          │          │                  │
│        ▼         ▼          ▼          ▼                  │
│  ┌──────────────────────┐                                │
│  │   Gemini API         │  ← AI layer (FREE TIER)        │
│  │   (Vision + Text)    │    Vision: receipt scanning    │
│  │                      │    Text: NLP parsing           │
│  └──────────┬───────────┘                                │
│             │                                            │
│             ▼                                            │
│  ┌──────────────────────┐                                │
│  │   Inventory Engine   │  ← Core business logic         │
│  │   (Go functions that │    Called as "tools" by agents │
│  │    agents invoke)    │                                │
│  └──────────┬───────────┘                                │
│             │                                            │
├─────────────┼────────────────────────────────────────────┤
│             ▼            DATA LAYER                      │
│  ┌──────────────────────┐                                │
│  │   SQLite Database    │  ← Upgradeable to PostgreSQL   │
│  │   (single file)      │                                │
│  └──────────────────────┘                                │
│                                                          │
│  ┌──────────────────────┐                                │
│  │   Go cron scheduler  │  ← Saturday reminders          │
│  └──────────────────────┘                                │
└──────────────────────────────────────────────────────────┘
```

### For the AI PM interview: key architectural concepts

**"What is Google ADK and why use it?"**
> ADK (Agent Development Kit) is Google's framework for building AI agents. An **agent** is an AI that can take actions — not just answer questions, but actually DO things (update a database, send a message, calculate a prediction). ADK gives us:
> - **Multi-agent architecture**: Instead of one big blob of code, we have specialized agents (Receipt Agent, Usage Agent, Stock Agent) that each handle one job. The Root Agent decides which sub-agent to call. Think of it like a company org chart — the CEO (Root Agent) delegates to department heads (sub-agents).
> - **Tool use**: Each agent can call "tools" — Go functions that do real work (database queries, calculations). The AI decides WHICH tool to call based on the user's message. This is called **agentic AI** — the AI has agency to take actions.
> - **Model-agnostic design**: ADK works with Gemini but can swap to other models. Our code stays the same.

**"Why is the messaging layer separate from the business logic?"**
> This is called **separation of concerns**. The Telegram bot code only knows how to receive and send messages. ADK handles the AI reasoning. The inventory engine handles grocery logic. Each layer is independent — we can swap Telegram for WhatsApp, or Gemini for Claude, by changing only one layer. In PM terms, this is like designing a product where the "distribution channel" is independent from the "core value."

**"What is an API?"**
> An API (Application Programming Interface) is a structured way for two software systems to talk to each other. When our bot sends a receipt photo to Gemini, it uses Gemini's API — a defined set of rules saying "send me an image in this format, and I'll return extracted text in this format." Think of it as a restaurant menu: you (the bot) can only order what's on the menu (the API), and the kitchen (Gemini) prepares it and sends it back in a predictable way.

**"What is a database schema?"**
> A schema is the structure/blueprint of your database — like a spreadsheet template with predefined column headers. It defines what data you store and how it relates to other data. Getting the schema right early is critical because changing it later means migrating existing data (like remodeling a house while people live in it).

**"What is Go and why pick it over Python?"**
> Go (also called Golang) is a programming language created by Google. It's known for being fast, simple, and great at handling many tasks at once (concurrency). Python is easier to learn, but Go produces a single compiled file you can run anywhere — no "install Python first" hassle. For an AI PM, knowing Go signals that you understand production-grade engineering, not just prototyping. The tradeoff: Go requires more explicit code (you have to declare what type every variable is), which makes it harder to write but easier to maintain long-term.

## 2. Database Schema

```sql
-- The items our household tracks
CREATE TABLE items (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,           -- "carrot", "basmati rice"
    category      TEXT NOT NULL,           -- "vegetable", "grain", "spice", "dairy"
    unit          TEXT NOT NULL,           -- "pieces", "kg", "g", "litres", "packets"
    tracking_type TEXT NOT NULL DEFAULT 'countable',  -- "countable" or "estimated"
    -- countable: precise stock levels tracked (carrots, eggs, paneer)
    -- estimated: no precise stock level; restock based on purchase intervals (oil, rice, atta, spices)
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Current stock levels (one row per item)
-- NOTE: Only meaningful for "countable" items. For "estimated" items,
-- quantity is set on purchase but NOT decremented on usage (since user
-- doesn't provide usage quantities). Restock for estimated items relies
-- on purchase interval patterns in the subscriptions table instead.
CREATE TABLE inventory (
    item_id     INTEGER PRIMARY KEY REFERENCES items(id),
    quantity    REAL NOT NULL DEFAULT 0, -- current quantity in stock
    updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Every purchase (from receipts or manual input)
-- Price per unit is computed (price/quantity) for cross-platform comparison
CREATE TABLE purchases (
    id              INTEGER PRIMARY KEY,
    item_id         INTEGER REFERENCES items(id),
    quantity        REAL NOT NULL,
    price           REAL,                    -- price paid (nullable, not always known)
    price_per_unit  REAL,                    -- computed: price/quantity for cross-platform comparison
    platform        TEXT,                    -- "FirstClub", "Zepto", "BigBasket"
    payment_method  TEXT,                    -- "UPI", "Credit Card", "Cash"
    receipt_image   TEXT,                    -- Telegram file_id of receipt photo
    is_confirmed    BOOLEAN DEFAULT FALSE,   -- user confirmed platform/details
    purchased_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Every usage event (from meal logging)
CREATE TABLE usage (
    id              INTEGER PRIMARY KEY,
    item_id         INTEGER REFERENCES items(id),
    quantity        REAL,                    -- NULL for estimated items (user said "used oil" with no amount)
    quantity_type   TEXT DEFAULT 'exact',    -- "exact" (2 carrots), "relative" (half the bottle), "none" (just "used oil")
    relative_amount REAL,                   -- for "relative" type: 0.5 = half, 0.9 = almost finished. NULL otherwise.
    recipe_name     TEXT,                   -- "paneer butter masala", nullable
    is_waste        BOOLEAN DEFAULT FALSE,  -- TRUE = thrown away, FALSE = used in cooking
    waste_reason    TEXT,                   -- "went bad", "expired", nullable
    waste_source_purchase INTEGER REFERENCES purchases(id), -- links waste to original purchase for platform quality tracking
    is_guest_usage  BOOLEAN DEFAULT FALSE,  -- TRUE = guest/party related, excluded from baseline forecasting
    used_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Recipes learned from user's messages
CREATE TABLE recipes (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,     -- "paneer butter masala"
    times_made  INTEGER DEFAULT 1,
    last_made   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Ingredients per recipe (with average quantities)
CREATE TABLE recipe_ingredients (
    recipe_id   INTEGER REFERENCES recipes(id),
    item_id     INTEGER REFERENCES items(id),
    avg_quantity REAL NOT NULL,           -- averaged over all times this recipe was logged
    unit        TEXT NOT NULL,
    PRIMARY KEY (recipe_id, item_id)
);

-- Outside meals (ordered from Zomato, Swiggy, restaurants)
CREATE TABLE outside_meals (
    id          INTEGER PRIMARY KEY,
    meal_name   TEXT NOT NULL,            -- "pizza", "biryani", "butter chicken"
    platform    TEXT,                     -- "Zomato", "Swiggy", "restaurant name"
    cost        REAL,                     -- amount spent (nullable)
    notes       TEXT,                     -- any additional details
    meal_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Daily activity log (for 8 PM nudge — tracks if user logged anything today)
CREATE TABLE daily_log_tracker (
    log_date    DATE PRIMARY KEY,         -- one row per day
    has_logged  BOOLEAN DEFAULT FALSE,    -- TRUE if any usage/purchase/outside meal logged
    nudge_sent  BOOLEAN DEFAULT FALSE,    -- TRUE if 8 PM nudge was sent
    updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Guest events (excluded from forecasting baseline)
CREATE TABLE guest_events (
    id          INTEGER PRIMARY KEY,
    guest_count INTEGER NOT NULL,
    occasion    TEXT,                     -- "dinner party", "Diwali", "birthday"
    event_date  DATE NOT NULL,
    notes       TEXT
);

-- Subscription patterns (auto-detected)
-- For ESTIMATED items, this is the PRIMARY restock signal (since stock levels aren't precise)
-- For COUNTABLE items, this supplements depletion-based alerts
CREATE TABLE subscriptions (
    id              INTEGER PRIMARY KEY,
    item_id         INTEGER REFERENCES items(id),
    avg_interval_days REAL NOT NULL,      -- average days between purchases
    avg_quantity    REAL NOT NULL,         -- average quantity per purchase
    preferred_platform TEXT,              -- where they usually buy this
    confidence      REAL,                 -- 0-1, how confident we are in this pattern
    next_order_date DATE,                 -- predicted next order date
    is_guest_adjusted BOOLEAN DEFAULT FALSE, -- TRUE if guest/party orders were excluded from interval calc
    updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### For the AI PM interview: schema design decisions

**"Why separate `purchases` and `usage` tables instead of one `transactions` table?"**
> They have different attributes. Purchases have price, platform, payment method. Usage has recipe name, waste flag. Merging them would create a table full of NULL columns — a design smell called **sparse tables**. Keeping them separate makes each table clean and queryable. In PM terms: purchases answer "where is my money going?" while usage answers "where is my food going?" — different questions, different data.

**"Why store `avg_quantity` in recipe_ingredients instead of individual amounts?"**
> Because quantities vary slightly each time ("sometimes I use 2 tomatoes, sometimes 3"). The average is more useful for predictions than any single instance. This is a **data modeling tradeoff**: we lose per-instance precision but gain a more useful aggregate. Same principle behind showing "average order value" in analytics dashboards rather than every individual order.

**"Why have a `tracking_type` field on items?"**
> Because oil, ghee, rice, flour, and spices can't be tracked the same way as carrots or eggs. Nobody measures "50ml oil" while cooking — they just pour. If we force precise quantity input, users will stop logging. So we split items into two categories:
> - **Countable**: User provides quantities on usage ("used 2 carrots"). Stock is tracked precisely. Restock nudge = depletion math.
> - **Estimated**: User just mentions the item ("used oil and rice for biryani"). No stock deduction. Restock nudge = purchase interval from order history ("you buy oil every ~25 days").
>
> This is a **product-informed schema decision** — the data model reflects how humans actually behave in the kitchen, not an idealized version. In interviews, this shows you understand that technical design must serve user behavior, not the other way around.

## 3. Gemini API Integration via ADK

### How ADK changes the design

Without ADK, we'd write: "if message has photo → call Gemini Vision. If text → call Gemini text parser." Lots of if/else code.

With ADK, we define **agents with tools**. Each agent has a description of what it does, and the Root Agent decides which sub-agent to route to. The sub-agent then decides which tools (Go functions) to call. This is called **agentic routing** — the AI makes the routing decision, not our code.

```
User sends: "bought 4 carrots from FirstClub"
  → Root Agent reads message
  → Decides: this is a purchase → routes to Receipt/Purchase Agent
  → Purchase Agent calls tool: add_purchase(item="carrot", qty=4, platform="FirstClub")
  → Tool writes to SQLite, returns confirmation
  → Agent formulates response: "Got it! 4 carrots added from FirstClub."
```

### 3.1 Receipt Scanning (Gemini Vision)

When a user sends a receipt photo, the Receipt Agent uses Gemini's multimodal capability:

```
Input:  Receipt photo (image sent via Telegram)
Output: Structured JSON

How it works in ADK: The Receipt Agent's system prompt tells it to
extract items from receipt images. Gemini processes the image and
returns structured data. The agent then calls tools to log each item.
```

**Expected JSON output from Gemini Vision:**
```json
{
  "store_name": "FirstClub",
  "date": "2026-05-18",
  "items": [
    {
      "name": "carrots",
      "quantity": 4,
      "unit": "pieces",
      "price": 40.0,
      "category": "vegetable"
    },
    {
      "name": "basmati rice",
      "quantity": 5,
      "unit": "kg",
      "price": 450.0,
      "category": "grain"
    }
  ],
  "total": 490.0,
  "confidence": 0.92
}
```

**The confirmation flow (critical for trust):**
```
User:    [sends receipt photo]
Bot:     I scanned your receipt. Here's what I found:

         FirstClub — May 18, 2026
         - 4 carrots (Rs 40)
         - 5kg basmati rice (Rs 450)
         Total: Rs 490

         Is this correct? (reply "yes" or tell me what to fix)
         What payment method did you use? (UPI/Card/Cash)

User:    yes, UPI
Bot:     Logged! Stock updated. Spent Rs 490 at FirstClub via UPI.
```

### 3.2 NLP Text Parsing (Gemini Text)

The Usage Agent handles natural language meal logging, including messages where some items have quantities and others don't:

**Example with mixed countable + estimated items:**
```
Input:  "Made biryani tonight, used 2 onions, 3 tomatoes, rice, oil and whole spices"

ADK flow:
  → Root Agent routes to Usage Agent
  → Usage Agent parses via Gemini, identifies:
      - Countable (with qty): onion × 2, tomato × 3
      - Estimated (no qty): rice, oil, whole spices
  → For countable items: calls deduct_stock("onion", 2, "pieces"), etc.
  → For estimated items: calls log_usage_event("rice"), log_usage_event("oil"), etc.
      (logs that item was used, but does NOT deduct stock)
  → Calls tool: log_recipe("biryani", all ingredients)
  → Returns: "Noted! Onions: 3 left | Tomatoes: 0 (REORDER NOW)
              Rice, oil, spices: usage logged (tracked by order cycle)"
```

**Example with only estimated items:**
```
Input:  "Made dal, used dal, ghee, salt, jeera"

ADK flow:
  → All items are estimated (no quantities given)
  → Logs usage events for each (no stock deduction)
  → Returns: "Noted! Logged dal, ghee, salt, jeera usage for dal."
```

**Example with relative quantity for estimated item (triggers mid-week alert):**
```
Input:  "Half the bottle of oil used making sweets for guests"

ADK flow:
  → Usage Agent parses: item=oil, relative_quantity="half", context="guests"
  → Checks last purchase: 5L oil bought 10 days ago (cycle = 15 days)
  → Calculates: ~2.5L remaining, normally lasts 15 days but only 5 days
    of the cycle remain → running out faster than expected
  → Tags as guest-related usage (excludes from baseline forecasting)
  → Checks: is there time before Saturday?
      - If mid-week and urgent → IMMEDIATE alert:
        "Heads up! You used half your oil for sweets. At this rate you'll
         run out before Sunday. Want to add oil to an immediate order?"
      - If close to Saturday → adds to Saturday report as REORDER NOW
```

**Example with proactive follow-up for missing details:**
```
Input:  "used 2 onions, 3 tomatoes, oil"

ADK flow:
  → Usage Agent parses items and quantities
  → Notices: no recipe name provided
  → Responds: "Logged! Onions: 3 left | Tomatoes: 0 (REORDER NOW)
               Oil: usage logged.
               What dish did you make? (helps me learn recipes —
               or say 'skip')"
```

The key insight: Gemini classifies each item as countable-with-quantity, estimated-without-quantity, or estimated-with-relative-quantity per message. The same item (e.g., rice) could be countable in one message ("used 500g rice"), estimated in another ("used rice for biryani"), or relative in another ("rice is almost over"). The system handles all three gracefully.

### 3.3 Agent Definitions (ADK concept)

In ADK, each agent is defined with:
- **Name**: what to call it
- **Model**: which Gemini model to use
- **Instructions**: system prompt telling it what to do
- **Tools**: Go functions it can call
- **Sub-agents**: other agents it can delegate to

```
Root Agent: "GroceryGenie"
├── Receipt Agent: scans photos, logs purchases, confirms unclear quantities from images
├── Usage Agent: parses meal/usage messages, updates stock, confirms relative quantities
│   └── also handles outside meal logging ("ordered pizza from Zomato")
├── Stock Agent: answers stock queries, generates reports, cross-platform price comparison
├── Budget Agent: tracks spend, payment methods, outside vs homecooked meal ratio
└── Reminder Agent: Saturday summaries, subscription alerts, 8 PM daily nudge, mid-week urgent alerts
```

### Language support

Gemini natively supports Kannada (ಕನ್ನಡ), Hindi, Hinglish, and English. All input is parsed by Gemini regardless of language and stored in English in the database. This means:
- Owner can type in English or Hinglish
- Cook/maid (Phase 6) can type in Kannada: "ಎರಡು ಈರುಳ್ಳಿ ಬಳಸಿದೆ" → stored as "onion, qty: 2"
- No separate translation step needed — Gemini handles it as part of entity extraction

### For the AI PM interview: prompt engineering concepts

**"What is prompt engineering?"**
> It's the practice of crafting the instructions you give to an AI model to get reliable, useful output. Think of it as writing a very precise job description — the better your instructions, the more consistent the output. Key techniques:
> - **System prompts**: Define the AI's role and constraints ("You are a grocery receipt parser...")
> - **Few-shot examples**: Show the AI 2-3 examples of input/output pairs so it learns the pattern
> - **Output schemas**: Tell the AI exactly what JSON structure to return
> - **Confidence scores**: Ask the AI to rate its own certainty (useful for knowing when to ask the user to confirm)

**"What is agentic AI vs traditional AI?"**
> Traditional AI: you ask a question, you get an answer. It's reactive.
> Agentic AI: you describe a goal, the AI figures out WHAT to do, decides WHICH tools to use, and takes actions. It has agency.
> In GroceryGenie: the user says "I made dal fry with 1 cup dal and 2 tomatoes." A traditional AI would say "okay noted." An agentic AI (our ADK setup) actually calls functions to deduct dal and tomatoes from inventory, log the recipe, check if anything is running low, and proactively warn the user. Same input, 5x more useful output.

**"What is multimodal AI?"**
> An AI model that handles multiple input types — text, images, audio, video. Gemini is multimodal: we send it a receipt photo (image) and it returns extracted items (text). When we parse "used 2 carrots," we use text-only. Under the hood, it's the same model handling both — that's the power of multimodal.

## 4. Subscription Detection Algorithm

```
For each item with 3+ purchases:
  1. Filter out guest/party-tagged purchases (don't let a Diwali bulk buy
     make the bot think you order oil twice a week)
  2. Calculate intervals between remaining consecutive purchases
  3. Compute mean interval and standard deviation
  4. If coefficient of variation (std/mean) < 0.3:
     → This item has a regular purchase cycle
  5. Store: avg_interval, avg_quantity, preferred_platform, is_guest_adjusted
  6. Predict next_order_date = last_purchase + avg_interval

Example (estimated item — this is the PRIMARY restock signal):
  Oil purchases: Day 1, Day 26, Day 48 (Diwali party — EXCLUDED), Day 52, Day 77
  After excluding party purchase:
  Intervals: [25, 51, 25] → wait, Day 26 to Day 52 = 26 days, Day 52 to Day 77 = 25 days
  Corrected intervals: [25, 26, 25] days
  Mean: 25.3 days | Std: 0.47 | CV: 0.02 (very consistent!)
  → "You buy oil every ~25 days. Next order due: Day 102"

Example (countable item — this supplements depletion alerts):
  Rice purchases: Day 1, Day 22, Day 43, Day 65
  Intervals: [21, 21, 22] days
  Mean: 21.3 days | Std: 0.47 | CV: 0.02 (very consistent!)
  → "You buy rice every ~21 days. Next order due: Day 86"
```

### For the AI PM interview: this is a "rule-based vs ML" decision

> We're using simple statistics (mean, standard deviation) instead of a machine learning model. Why? Because:
> 1. We have very little data per user (a few months of orders)
> 2. The pattern is simple (regular intervals)
> 3. It's explainable — you can tell the user exactly WHY the bot thinks they need rice
> 4. ML would be overkill and harder to debug
>
> **When would you switch to ML?** When you have thousands of users and want to find patterns across households, like "people who buy paneer also tend to buy cream within 2 days." That's a recommendation system — a different problem.

## 4B. Cross-Platform Price Comparison Algorithm

```
For each item purchased from 2+ platforms:
  1. Compute price_per_unit for each purchase (price / quantity)
  2. Group by platform, take average price_per_unit per platform
  3. Rank platforms cheapest to most expensive
  4. Include in Saturday report: "Carrots: Rs 40/kg (FirstClub) vs Rs 55/kg (Zepto)"

Example:
  Carrots from FirstClub: Rs 40/kg, Rs 38/kg, Rs 42/kg → avg Rs 40/kg
  Carrots from Zepto: Rs 52/kg, Rs 58/kg → avg Rs 55/kg
  → "FirstClub is 27% cheaper for carrots"
```

## 4C. Platform Quality Score (upfront cost vs true cost)

```
For each item purchased from 2+ platforms:
  1. Calculate waste_rate per platform:
     waste_qty / total_purchased_qty (from that platform)
  2. Calculate effective_cost_per_unit:
     avg_price_per_unit / (1 - waste_rate)
     (what you actually pay per unit of USABLE product)
  3. Compare across platforms

Example:
  Tomatoes from FirstClub:
    Avg price: Rs 40/kg | Waste rate: 10% | Effective cost: Rs 44/kg
  Tomatoes from Zepto:
    Avg price: Rs 35/kg | Waste rate: 35% | Effective cost: Rs 54/kg
  
  → "Zepto tomatoes are Rs 5/kg cheaper upfront, but you waste 35% of them.
     Effective cost: FirstClub Rs 44/kg vs Zepto Rs 54/kg.
     FirstClub saves you Rs 10/kg after accounting for waste."
```

### For the AI PM interview: upfront cost vs total cost of ownership

> This is the grocery equivalent of **TCO (Total Cost of Ownership)** — a concept from enterprise sales. A cheaper product that breaks/expires faster costs MORE in the long run. We're applying the same logic: a platform with cheaper prices but higher waste rates is actually more expensive. This insight is powerful because:
> 1. It's data-driven (not opinion: "I feel FirstClub is better")
> 2. It's counter-intuitive (cheaper ≠ better value)
> 3. It drives actionable behavior change (switch platforms for specific items)
> 4. No API scraping needed — all from the user's own data

## 5. Depletion & Restock Prediction Logic

### 5A. Countable items (stock-level based)

```
For each COUNTABLE item in stock:
  1. Calculate daily_usage_rate = total_used_last_30_days / 30
  2. days_remaining = current_stock / daily_usage_rate
  3. Classify:
     - days_remaining <= 2  → REORDER NOW (essential)
     - days_remaining <= 5  → RUNNING LOW (good-to-have)
     - days_remaining > 5   → ALL GOOD
  
Special handling:
  - New items (< 7 days of data): use category averages
  - Guest events: exclude those days from baseline calculation
  - Seasonal items: weight recent usage higher (exponential moving average)
```

### 5B. Estimated items (order-history based)

```
For each ESTIMATED item (oil, rice, atta, spices, ghee, etc.):
  1. Look at purchase history (from receipts)
  2. Calculate avg_interval = mean(days between consecutive purchases)
  3. days_since_last_purchase = today - last_purchase_date
  4. days_until_reorder = avg_interval - days_since_last_purchase
  5. Classify:
     - days_until_reorder <= 2   → REORDER NOW
     - days_until_reorder <= 5   → RUNNING LOW
     - days_until_reorder > 5    → ALL GOOD

Special handling:
  - New items (< 3 purchases): can't predict interval yet, show "no pattern detected"
  - Guest/party purchases: EXCLUDE from interval calculation
    (if you bought extra oil for a Diwali party, that shouldn't make the bot
     think you buy oil twice as often)
  - Usage frequency boost: if usage events logged more often than usual
    this week (e.g., cooking daily instead of 4x/week), shorten the predicted
    interval proportionally
  - Relative quantity signals: "half the bottle used" → recalculate:
      remaining = last_purchase_qty × (1 - fraction_used)
      adjusted_days_left = remaining / (last_purchase_qty / avg_interval)
      If adjusted_days_left < days_until_saturday → IMMEDIATE mid-week alert
```

### 5C. Mid-week urgent alerts

```
Regular alerts fire on Saturday. Mid-week alerts fire ONLY when urgent:

Trigger conditions (any one):
  1. Countable item stock hits 0 or ≤ 1 day of usage left
  2. User reports large estimated-item usage with relative quantity
     ("half the oil used", "rice almost finished")
  3. Depletion prediction says item will run out before next Saturday

Alert format:
  "Heads up! [Item] is running low mid-week.
   [Reason: stock depleted / large usage reported / predicted to run out Thursday]
   Want to add it to an immediate order, or wait for Sunday?"

Rules:
  - Max 1 mid-week alert per day (don't spam)
  - Only for items classified as REORDER NOW, not RUNNING LOW
  - User can reply "wait" to defer to Saturday report
```

### For the AI PM interview: two prediction models in one product

> This is a great interview talking point. Most products pick one prediction model. GroceryGenie uses two, matched to data availability:
> - **Data-rich path** (countable items): precise stock levels → depletion math → exact "N days left"
> - **Data-sparse path** (estimated items): no precise usage data → fall back to purchase frequency → "due in ~N days based on your order history"
>
> This is called **graceful degradation** — when you don't have the ideal data, you fall back to a less precise but still useful signal rather than showing nothing. The alternative (forcing users to measure oil in ml) would improve data quality but destroy user engagement. Product sense means choosing user behavior over data purity.

### Combined Saturday report example

```
REORDER NOW:
  Carrots — 1 left (you use ~3/week)              ← countable, stock-based
  Cooking oil — last bought 23 days ago            ← estimated, interval-based
    (you reorder every ~25 days)

RUNNING LOW:
  Tomatoes — 3 left (~4 days)                      ← countable
  Rice — bought 18 days ago (cycle: ~21 days)      ← estimated
  Atta — bought 12 days ago (cycle: ~15 days)      ← estimated

ALL GOOD:
  Onions — 8 left                                  ← countable
  Ghee — bought 5 days ago (cycle: ~30 days)       ← estimated
```

## 6. File Structure

```
grocery-app/
├── docs/
│   ├── PRD.md                  ← Product requirements
│   ├── TECHNICAL_DESIGN.md     ← This document
│   └── EVAL_FRAMEWORK.md       ← How we test AI quality
├── cmd/
│   └── bot/
│       └── main.go             ← Entry point: starts Telegram bot + ADK agents
├── internal/
│   ├── agents/
│   │   ├── root.go             ← Root Agent: routes messages to sub-agents
│   │   ├── receipt.go          ← Receipt Agent: scans photos, logs purchases
│   │   ├── usage.go            ← Usage Agent: parses meal/usage messages
│   │   ├── stock.go            ← Stock Agent: queries inventory, generates reports
│   │   └── budget.go           ← Budget Agent: tracks spend, payment methods
│   ├── tools/
│   │   ├── inventory.go        ← Tools: add_purchase, deduct_stock, get_stock
│   │   ├── budget.go           ← Tools: log_spend, get_budget_summary
│   │   ├── recipes.go          ← Tools: log_recipe, recall_recipe
│   │   └── subscriptions.go   ← Tools: detect_patterns, get_predictions
│   ├── database/
│   │   ├── db.go               ← SQLite connection + migrations
│   │   └── schema.sql          ← Database schema (tables defined here)
│   ├── telegram/
│   │   └── bot.go              ← Telegram bot: receives messages, sends replies
│   └── scheduler/
│       └── cron.go             ← Saturday reminders, subscription alerts
├── prompts/
│   ├── receipt_agent.txt       ← System prompt for receipt scanning agent
│   ├── usage_agent.txt         ← System prompt for usage parsing agent
│   ├── stock_agent.txt         ← System prompt for stock query agent
│   └── budget_agent.txt        ← System prompt for budget tracking agent
├── evals/
│   ├── receipt_samples/        ← Test receipt images
│   ├── text_samples.json       ← Test text inputs + expected outputs
│   └── run_evals.go            ← Eval runner
├── .env                        ← API keys (never committed to git)
├── go.mod                      ← Go module definition (like package.json)
├── go.sum                      ← Dependency checksums (auto-generated)
└── README.md                   ← Setup instructions
```

### For the AI PM interview: Go project structure conventions

> Go projects follow a standard layout:
> - **cmd/**: Entry points (the "main" function that starts the app)
> - **internal/**: Private code that only this project can use. The `internal` keyword in Go actually PREVENTS other projects from importing your code — a built-in encapsulation feature.
> - **go.mod**: Declares your project name and dependencies (like a shopping list for code libraries)
>
> This matters for PMs because it shows you understand how code is organized in production systems — not just "it works on my laptop" scripts.

## 7. Technology Choices

| Component | Choice | Why | Cost |
|-----------|--------|-----|------|
| Language | Go 1.22+ | Production-grade, compiles to single binary, great concurrency | Free |
| Agent framework | Google ADK Go v1.3 | Multi-agent architecture, tool use, model-agnostic | Free |
| AI model | Gemini 2.5 Flash (free tier) | Multimodal (text + vision), generous free limits | Free |
| Bot framework | telebot/v3 (Go library) | Popular Go Telegram bot library | Free |
| Database | SQLite via go-sqlite3 | Zero setup, single file, sufficient for single-user MVP | Free |
| Scheduler | robfig/cron (Go library) | Standard Go cron library, runs in-process | Free |
| Hosting | Your laptop (MVP) | No deployment needed initially | Free |
| Hosting (later) | Google Cloud Run | ADK is optimized for Cloud Run, has free tier | Free tier |

**Total cost: Rs 0/month**

## 8. Security & Privacy Considerations

- API keys stored in `.env`, never committed to git (use `.gitignore`)
- Receipt images processed via Gemini API — Google's data retention policy applies
- No sensitive financial data stored (no bank account numbers, no card details)
- Bot is private (only your Telegram account interacts with it)
- SQLite database is a local file — your data stays on your machine
- Go binaries are compiled — no source code exposed in deployment
