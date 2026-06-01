# Product Requirements Document: GroceryGenie

## Visual Overview

### How the system works (daily life)

```
┌─────────────────────────────────────────────────────────────────┐
│                        YOUR DAILY LIFE                          │
│                                                                 │
│  MORNING                    EVENING                    ANYTIME  │
│  ┌──────────────┐          ┌──────────────┐     ┌────────────┐  │
│  │ Cook makes   │          │ You shop at  │     │ "Threw away│  │
│  │ breakfast    │          │ FirstClub    │     │  2 bananas"│  │
│  │              │          │              │     │            │  │
│  │ You text bot:│          │ You snap a   │     │ Bot logs   │  │
│  │ "Made poha,  │          │ receipt photo│     │ waste,     │  │
│  │  used 200g   │          │ & send to bot│     │ learns     │  │
│  │  poha, oil,  │          │              │     │ shelf life │  │
│  │  curry leaves"│         │              │     │            │  │
│  └──────┬───────┘          └──────┬───────┘     └─────┬──────┘  │
│         │                        │                    │         │
│  Poha: deducted          Receipt scanned         Waste logged   │
│  Oil: usage logged       Items + prices          separately     │
│  (no qty — estimated)    saved to database                      │
│         │                        │                    │         │
│         ▼                        ▼                    ▼         │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    TELEGRAM BOT                          │   │
│  │                  (you text it like a friend)              │   │
│  └──────────────────────────┬───────────────────────────────┘   │
└─────────────────────────────┼───────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                   GROCERYGENIE BRAIN                             │
│                   (Google ADK + Gemini AI — FREE)                │
│                                                                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │ RECEIPT  │  │ USAGE    │  │ STOCK    │  │ BUDGET   │        │
│  │ AGENT    │  │ AGENT    │  │ AGENT    │  │ AGENT    │        │
│  │          │  │          │  │          │  │          │        │
│  │ Scans    │  │ Parses   │  │ Reports  │  │ Tracks   │        │
│  │ photos   │  │ meals    │  │ stock    │  │ spend    │        │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘        │
│       └──────────────┴─────────────┴─────────────┘              │
│                         │                                       │
│                         ▼                                       │
│              ┌─────────────────────┐                            │
│              │  SQLite DATABASE    │  ← Everything saved here   │
│              │  (permanent memory) │    Forever. New agents      │
│              │                     │    can query ALL history    │
│              └─────────────────────┘    from Day 1.             │
└─────────────────────────────────────────────────────────────────┘
```

### The weekly cycle

```
 MON        TUE        WED        THU        FRI        SAT           SUN
  │          │          │          │          │          │              │
  ▼          ▼          ▼          ▼          ▼          ▼              ▼
┌─────┐  ┌─────┐  ┌─────┐  ┌─────┐  ┌─────┐  ┌──────────┐  ┌──────────────┐
│Log  │  │Log  │  │Log  │  │Log  │  │Log  │  │ SATURDAY │  │   SUNDAY     │
│meals│  │meals│  │meals│  │meals│  │meals│  │ REMINDER │  │   ORDER DAY  │
│     │  │     │  │     │  │     │  │     │  │          │  │              │
│     │  │     │  │     │  │     │  │     │  │ REORDER: │  │ Order from   │
│     │  │     │  │     │  │     │  │     │  │ Carrots  │  │ list, snap   │
│     │  │     │  │     │  │     │  │     │  │  (1 left)│  │ receipt,     │
│     │  │     │  │     │  │     │  │     │  │ Oil (due │  │ send to bot  │
│     │  │     │  │     │  │     │  │     │  │  in 2d)  │  │              │
│     │  │     │  │     │  │     │  │     │  │          │  │ Cycle        │
│     │  │     │  │     │  │     │  │     │  │ BUDGET:  │  │ restarts     │
│     │  │     │  │     │  │     │  │     │  │ Rs 3,200 │  │              │
│     │  │     │  │     │  │     │  │     │  │ of 8,000 │  │              │
└─────┘  └─────┘  └─────┘  └─────┘  └─────┘  └──────────┘  └──────────────┘
```

### Countable vs Estimated items

```
COUNTABLE items                     ESTIMATED (bulk/liquid) items
─────────────────                   ──────────────────────────────
Carrots, tomatoes, eggs,            Oil, ghee, rice, flour (atta),
onions, paneer (by weight),         salt, sugar, spices, butter,
bread (packets)                     dal (when used in cooking)

User says: "used 2 carrots"        User says: "used oil and rice
                                    for biryani" (no quantity)

  ┌─────────────┐                    ┌──────────────────┐
  │ STOCK-LEVEL │                    │ ORDER-HISTORY    │
  │ TRACKING    │                    │ TRACKING         │
  │             │                    │                  │
  │ 4 → 2 → 0  │                    │ Bought 25 days   │
  │ "2 left"    │                    │ ago, cycle = 25d │
  └──────┬──────┘                    └────────┬─────────┘
         │                                   │
         ▼                                   ▼
  "Carrots running low               "You buy oil every
   — 1 left, you use                  ~25 days. Due in
   ~3/week"                           5 days."
```

### Phase roadmap

```
  PHASE 1 (Week 1-2)          PHASE 2 (Week 3-4)         PHASE 3 (Week 5-6)
  ┌─────────────────┐         ┌─────────────────┐        ┌──────────────────┐
  │ FOUNDATION +    │         │ USAGE TRACKING   │        │ SMART REMINDERS  │
  │ RECEIPT SCAN    │         │ + NLP            │        │ + SUBSCRIPTIONS  │
  │                 │         │                  │        │                  │
  │ • Telegram bot  │         │ • "Made dal,     │        │ • Saturday       │
  │ • Send receipt  │────────▶│   used oil, dal" │───────▶│   weekly report  │
  │   photo → items │         │   → parsed       │        │ • "You buy rice  │
  │   extracted     │         │ • Countable vs   │        │   every 21 days" │
  │ • Platform      │         │   estimated item │        │ • Interval-based │
  │   confirmed     │         │   handling       │        │   alerts for     │
  │ • Basic stock   │         │ • Recipe memory  │        │   estimated items│
  │                 │         │ • Depletion      │        │                  │
  │ LOGBOOK         │         │   alerts         │        │ PROACTIVE        │
  └─────────────────┘         └──────────────────┘        └──────────────────┘

  PHASE 4 (Week 7-8)          PHASE 5 (Week 9-10)        PHASE 6 (Week 11-12)
  ┌─────────────────┐         ┌─────────────────┐        ┌──────────────────┐
  │ WASTE TRACKING  │         │ BUDGET +         │        │ ADVANCED         │
  │                 │         │ SPEND            │        │ FEATURES         │
  │ • "Threw away   │         │                  │        │                  │
  │   3 tomatoes"   │         │ • Monthly budget │        │ • Guest mode     │
  │ • Waste reports │────────▶│ • Spend by UPI/  │───────▶│   (exclude from  │
  │ • Shelf life    │         │   Card/Cash      │        │   forecasting)   │
  │   learning      │         │ • Spend by       │        │ • Cook/maid      │
  │ • "Buy 8 not    │         │   platform       │        │   access         │
  │   12 tomatoes"  │         │ • Price memory   │        │ • Meal planning  │
  │                 │         │                  │        │ • Seasonal       │
  │ REDUCES WASTE   │         │ MONEY VISIBILITY │        │ EDGE CASES       │
  └─────────────────┘         └──────────────────┘        └──────────────────┘

  Each phase is self-contained — stop at any phase and still have a useful product.
  New agents added in later phases can query ALL data from Day 1 (stored in database).
```

## 1. Problem Statement

Urban Indian households with domestic help (cook/maid) face a broken grocery replenishment cycle. The rise of 10-minute delivery apps (Zepto, Blinkit, Swiggy Instamart) has eliminated the incentive for cooks and maids to give advance notice when ingredients run low. Instead, they report shortages at the last moment, triggering daily panic orders.

This leads to:
- **Overspending**: Multiple small orders with delivery fees vs. one planned weekly order
- **No visibility**: The household manager has no idea what's in stock at any given time
- **No budgeting**: Impossible to track monthly grocery spend when orders happen ad-hoc across 5+ apps
- **Food waste**: Duplicate purchases because nobody tracks what's already available

## 2. Target User

**Primary persona: Urban household manager (Bengaluru, India)**
- Manages a household with a cook and/or maid
- Wants to consolidate grocery ordering to once a week (Sundays)
- Buys fruits/veggies from FirstClub; other items from various delivery apps
- Comfortable with Telegram/WhatsApp — not looking for another app to install
- Wants to understand spending patterns and reduce waste

**Secondary persona (future): Cook/Maid**
- Logs ingredient usage after each meal
- Uses Telegram/WhatsApp daily
- Needs a dead-simple interface — no forms, no menus, just chat

## 3. Solution Overview

A Telegram bot ("GroceryGenie") that acts as a conversational grocery inventory manager. Users interact via natural language messages and receipt photos. The bot tracks what's in stock, predicts when items will run out, and generates a weekly shopping list every Saturday.

### Core Interaction Model

```
Input methods:
  1. Receipt photo  → Bot extracts items, quantities, prices, platform via Vision AI
  2. Text message   → "Used 2 carrots and 500g dal for sambar"
  3. Text message   → "Used oil, rice, atta for biryani" (no quantity — estimated items)
  3. Commands        → /stock, /budget, /waste, /plan

Output:
  1. Real-time       → Stock updates after each input
  2. Scheduled       → Saturday reorder reminders
  3. Proactive       → "Carrots running low" alerts
  4. On-demand       → Budget reports, stock checks
```

### Two types of items: Countable vs Estimated

A critical design distinction. Not all grocery items can be tracked with precise quantities:

```
COUNTABLE items                     ESTIMATED (bulk/liquid) items
─────────────────                   ──────────────────────────────
Carrots, tomatoes, onions,          Oil, ghee, rice, flour (atta),
eggs, paneer (by weight),           salt, sugar, spices, butter,
bread (packets)                     milk, dal (when used in cooking)

User says: "used 2 carrots"        User says: "used oil and rice 
                                    for biryani" (no quantity)

Tracking: STOCK-LEVEL BASED        Tracking: ORDER-HISTORY BASED
  Stock goes 4 → 2 → 0               No stock level maintained
  "2 carrots left"                    "You bought 5L oil 20 days ago"

Restock nudge: "Carrots running     Restock nudge: "You buy oil every
low — 1 left, you use ~3/week"     25 days. Due in 5 days."

Prediction: depletion math          Prediction: purchase interval math
  (current_stock / daily_usage)       (avg days between orders)

                                    EXCEPTION: user provides relative
                                    quantity like "half the bottle used"
                                    → bot recalculates remaining stock
                                    and may trigger an IMMEDIATE alert
                                    instead of waiting for Saturday
```

**Why this matters:** If we force users to estimate "used 50ml oil," they'll stop logging — nobody measures oil while cooking. Accept the imprecision, use a different prediction model, and the product stays frictionless. BUT if the user volunteers a relative quantity ("half the bottle," "almost finished"), honour it — that's a strong signal.

### Relative quantities for estimated items

Sometimes users provide rough indicators for bulk items without exact measurements:

```
User says:               Bot interprets:                Alert:
─────────────            ───────────────                ──────
"half the oil bottle     50% of last purchase           If >50% of interval has
 used for sweets"        quantity consumed               passed → REORDER NOW
                                                        If early in cycle →
                                                         add to Saturday list

"rice almost over"       ~90% consumed                  REORDER NOW (immediate)

"used some ghee"         Unknown amount (log event,     No alert change
                         don't adjust stock)             (stay on interval)
```

### Alert timing: Saturday reports + mid-week urgency

```
SATURDAY (scheduled):  Full weekly report with all categories
                       — the default planning moment

MID-WEEK (triggered):  ONLY fires when something becomes urgent
                       between Saturdays. Triggers:
                       • Countable item hits 0 or near-0
                       • User reports large estimated-item usage
                         ("half the oil used", "rice almost over")
                       • Depletion prediction says item will run
                         out before next Saturday

Bot: "Heads up! Oil is running low — you used half the bottle
     yesterday and your usual cycle says you'd reorder in 8 days.
     Want to add it to an immediate order or wait for Sunday?"
```

### Proactive follow-ups: bot asks for missing details

The bot should never silently accept incomplete information. If the user skips key details, the bot asks:

```
RECEIPT SCAN — missing payment method:
  User: [sends receipt photo]
  Bot:  "Got it! 4 carrots, 2kg rice from FirstClub (Rs 490).
         How did you pay? (UPI / Card / Cash)"

USAGE LOG — missing recipe name:
  User: "used 2 onions, 3 tomatoes, oil"
  Bot:  "Logged! What dish did you make? (helps me learn recipes
         for future suggestions — or say 'skip')"

RECEIPT SCAN — platform unclear:
  User: [sends receipt photo, store name not visible]
  Bot:  "I couldn't identify the store from this receipt.
         Where did you buy this? (FirstClub / Zepto / other)"

USAGE LOG — ambiguous quantity:
  User: "used some paneer"
  Bot:  "How much paneer? (e.g., 200g, half a packet, or say
         'not sure' and I'll just log that you used it)"

RECEIPT SCAN — unclear quantity in image:
  User: [sends receipt photo where "500" could be 500g or 500ml]
  Bot:  "I see '500' next to ghee — is that 500g or 500ml?
         Just want to make sure I log the right unit."

RELATIVE QUANTITY — confirming against last order:
  User: "half the oil bottle used"
  Bot:  "You ordered 1L of oil last time from FirstClub.
         So ~500ml was used — is that right?
         (Asking because you've ordered different sizes before)"
```


## 4. Feature Requirements

### Phase 1: Foundation + Receipt Scanning (Week 1-2)
**Goal: Get data flowing into the system with minimum friction**

| ID | Feature | Description | Priority |
|----|---------|-------------|----------|
| F1.1 | Telegram bot setup | Bot responds to messages, handles text and images | P0 |
| F1.2 | Receipt scanning | User sends receipt photo → Claude Vision extracts: items, quantities, prices, store name | P0 |
| F1.3 | Platform confirmation | After scanning, bot asks: "Looks like this is from FirstClub. Correct?" — user confirms or corrects | P0 |
| F1.4 | Basic inventory | Store items with quantities. Classify items as "countable" (carrots, eggs) or "estimated" (oil, rice, spices) based on whether user provides quantities. Show stock on /stock command | P0 |
| F1.5 | Spend logging | Every scanned receipt logs: total amount, platform, payment method (ask user), date | P0 |
| F1.6 | Proactive follow-ups | Bot asks for missing details: payment method if not provided, platform if unclear from receipt, quantity if ambiguous from receipt image — confirm unclear quantities via chat before assuming any units. Never silently accept incomplete data. User can say "skip" for optional fields. | P0 |
| F1.7 | Daily logging nudge | If user hasn't logged anything by 8:00 PM IST, bot sends a Telegram reminder: "Hey! Looks like you haven't logged any meals today. What did you eat?" Keeps the logging habit alive. | P1 |

**AI PM concept — why receipt scanning first?**
> Receipt scanning reduces "input friction" — the biggest risk to a logging-based product. If users have to manually type every item they bought, they'll stop within a week. A photo takes 2 seconds. This is a product decision informed by behavioral economics: make the desired behavior (logging) as easy as possible.

### Phase 2: Usage Tracking + NLP Parsing (Week 3-4)
**Goal: Track what goes OUT, not just what comes IN**

| ID | Feature | Description | Priority |
|----|---------|-------------|----------|
| F2.1 | Usage logging via NLP | "Made paneer butter masala, used 250g paneer, 2 tomatoes, 1 onion" → parsed and deducted from stock. For estimated items (oil, rice, spices), accept usage WITHOUT quantity — "used oil, rice for biryani" — and log as a usage event without stock deduction. ALSO handle relative quantities: "half the bottle of oil used" → recalculate remaining stock and potentially trigger mid-week alert | P0 |
| F2.2 | Recipe memory | Bot stores recipe-to-ingredient mappings learned from usage messages | P1 |
| F2.3 | Quick recipe recall | "Made paneer butter masala again" → bot uses last time's ingredients, asks to confirm | P1 |
| F2.4 | Depletion alerts (countable items) | When a countable item drops below a threshold (e.g., 2 days of avg usage left), alert the user | P0 |
| F2.5 | Reorder alerts (estimated items) | For estimated items (oil, rice, atta), use ORDER HISTORY to predict restock: "You buy 5L oil every ~25 days, due in 5 days." No stock-level math — purely interval-based. | P0 |
| F2.6 | Stock categories | Classify alerts: REORDER NOW (critical) / RUNNING LOW (soon) / ALL GOOD. Countable items use stock levels; estimated items use purchase interval proximity. | P0 |
| F2.7 | Mid-week urgent alerts | Don't wait for Saturday if something runs out or gets critically low mid-week. Fires immediately when: countable item hits near-zero, user reports large estimated-item usage ("half the oil used"), or depletion prediction says item will run out before next Saturday. | P0 |
| F2.8 | Relative quantity confirmation | When user says "used half the bottle of oil", bot checks last purchase quantity and confirms: "You ordered 1L last time — so you mean ~500ml was used? Just confirming since order sizes can vary." Never assume — always confirm context. | P0 |
| F2.9 | Outside meal logging | User can log meals ordered from outside: "Ordered pizza from Zomato" or "Had biryani from Swiggy." Tracked separately from homecooked meals. Bot logs: meal name, platform (Zomato/Swiggy/restaurant name), cost if mentioned. Enables homecooked vs outside meal ratio tracking. | P1 |

**AI PM concept — structured data extraction:**
> This is a classic NLP-to-structured-data problem. The Claude API takes unstructured text ("used 2 carrots in curry") and returns structured JSON ({item: "carrot", quantity: 2, unit: "pieces", recipe: "curry"}). In interviews, this is called "entity extraction" — the AI identifies entities (carrot, 2, curry) and their relationships.

### Phase 3: Smart Reminders + Subscription Detection (Week 5-6)
**Goal: The bot starts being proactive**

| ID | Feature | Description | Priority |
|----|---------|-------------|----------|
| F3.1 | Saturday reminder | Weekly message listing what to reorder, grouped by platform (FirstClub vs others) | P0 |
| F3.2 | Subscription detection | Bot identifies repeating purchase patterns. Especially critical for estimated items (oil, rice, atta) where this is the PRIMARY restock signal since stock levels aren't tracked precisely. "You buy 5kg rice every 3 weeks from FirstClub" | P1 |
| F3.3 | Auto-order reminders | "Your rice order is due in 2 days based on your usual cycle" | P1 |
| F3.4 | Platform memory | Bot remembers which items you buy from which platform, suggests accordingly | P1 |
| F3.5 | Cross-platform price comparison | Compare prices of same items across platforms from YOUR receipt history (not scraped). "Carrots: Rs 40/kg at FirstClub vs Rs 55/kg at Zepto — FirstClub is 27% cheaper." Suggests cheapest platform per item in Saturday report. | P1 |

**AI PM concept — subscription detection is anomaly detection in reverse:**
> Instead of finding outliers, we're finding regularity. The algorithm looks at purchase history for each item and fits a frequency (every N days). If the coefficient of variation is low (consistent intervals), it's flagged as a "subscription-like" pattern. This is the same math behind SaaS churn prediction.

### Phase 4: Waste Tracking (Week 7-8)
**Goal: Reduce waste, save money**

| ID | Feature | Description | Priority |
|----|---------|-------------|----------|
| F4.1 | Waste logging | "Threw away 3 tomatoes — went bad" → logged separately from usage | P0 |
| F4.2 | Waste insights | "You wasted Rs 320 of vegetables this month (8% of grocery spend)" | P1 |
| F4.3 | Shelf-life learning | Bot learns: tomatoes last ~4 days in your household, paneer ~3 days | P1 |
| F4.4 | Smart quantity suggestions | "Buy 8 tomatoes instead of 12 — you used 7 and wasted 4 last week" | P2 |
| F4.5 | Platform quality scoring | Cross-reference waste logs with purchase platform. "Eggs from Zepto: 30% waste rate vs FirstClub: 5%. FirstClub eggs are Rs 10 more but you throw away fewer." Calculates upfront cost vs true cost (including waste). | P1 |
| F4.6 | Quality-adjusted platform suggestions | "FirstClub veggies cost Rs 40/kg (Rs 5 more than Zepto) but you waste 60% less. Effective cost: FirstClub Rs 42/kg vs Zepto Rs 68/kg after waste." | P2 |

### Phase 5: Budget & Spend Intelligence (Week 9-10)
**Goal: Full financial visibility on groceries**

| ID | Feature | Description | Priority |
|----|---------|-------------|----------|
| F5.1 | Monthly budget setting | User sets: "My grocery budget is Rs 8,000/month" | P1 |
| F5.2 | Spend dashboard | /budget shows: spent so far, by category, by platform, by payment method | P0 |
| F5.3 | Budget alerts | "You've spent 70% of your grocery budget and it's only the 15th" | P1 |
| F5.4 | Payment method tracking | Track UPI / credit card / cash per order (asked during receipt scan) | P1 |
| F5.5 | Price memory | Bot remembers item prices over time, flags unusual spikes | P2 |
| F5.6 | Outside meal spend | /meals shows: homecooked vs outside meal ratio, outside meal spend by platform (Zomato/Swiggy), frequency trend. "You ordered out 8 times this month (vs 5 last month). Outside meal spend: Rs 4,200." | P1 |

### Phase 6: Guest Mode + Advanced Features (Week 11-12)
**Goal: Handle edge cases, add delight**

| ID | Feature | Description | Priority |
|----|---------|-------------|----------|
| F6.1 | Guest mode | "Having 6 guests Saturday" → quantities inflated, excluded from baseline forecasting. For estimated items, guest/party orders are tagged and excluded from purchase-interval calculations so they don't artificially shorten reorder cycles | P1 |
| F6.2 | Festival/occasion tags | Tag spikes as "Diwali" / "Birthday" → excluded from forecasting for both countable and estimated items | P2 |
| F6.3 | Cook/maid access | Second Telegram user can log usage; owner sees summary. Supports Kannada language input — Gemini translates to English for storage. Cook can type in Kannada ("ಎರಡು ಈರುಳ್ಳಿ ಬಳಸಿದೆ") and bot understands + stores in English. | P1 |
| F6.4 | Meal planning | "Plan meals for the week" → bot suggests based on stock + preferences | P2 |
| F6.5 | Seasonal awareness | "Mangoes in season — you bought 2kg/week last May" | P2 |

## 5. Success Metrics

| Metric | Target | Why it matters |
|--------|--------|---------------|
| **Weekly active logging** | User logs 5+ days/week | Product is only useful if data flows in |
| **Receipt scan accuracy** | >90% items correctly extracted | Core AI feature must be reliable |
| **Panic orders reduced** | <2 emergency orders/month (from daily) | Primary problem solved |
| **Weekly order adherence** | >3 out of 4 weeks ordered on Sunday | Behavior change achieved |
| **Monthly spend visibility** | User can state grocery spend within 10% | Budget awareness achieved |
| **Waste reduction** | 20% less waste in month 2 vs month 1 | Tangible financial impact |
| **Daily logging consistency** | 8 PM nudge converts to log >50% of the time | Nudge is effective, not annoying |
| **Outside meal tracking** | User logs >80% of outside meals | Complete picture of eating habits |
| **Platform optimization** | User shifts >1 item to cheaper/better-quality platform | Price/quality insights are actionable |

## 6. Out of Scope (for now)

- Auto-adding items to delivery app carts (API limitations)
- Scraping prices from delivery apps (no public APIs) — but we DO compare prices from user's own receipt history
- Multi-household / sharing with other families
- Web or mobile app UI (Telegram is the interface)
- Integration with smart fridges or IoT devices

## 7. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| User stops logging after initial excitement | Product becomes useless | Receipt scanning reduces friction; Saturday reminders create habit loop |
| Receipt scan quality varies (blurry photos, different formats) | Bad data in system | Confidence scores on extraction; always confirm with user before logging |
| Claude API costs at scale | Unsustainable | Batch processing; cache common recipes; use smaller models for simple parsing |
| Cook/maid uncomfortable with Telegram bot | Half the data missing | Start with owner-only; add cook later when value is proven |
| India-specific receipt formats not parsed well | Core feature broken | Build eval dataset of real Indian receipts; fine-tune prompts |

## 8. Technical Constraints

- Must work on low-bandwidth connections (Telegram is lightweight)
- Must handle Hindi/English code-mixed input ("2 kilo atta liya aaj")
- Must handle Indian units (kg, dozen, bunch, packet)
- Receipt formats vary wildly across Indian stores (FirstClub, Reliance, local vendors)
- No public APIs available for Indian grocery delivery platforms
- Must gracefully handle usage messages without quantities ("used oil and rice for biryani") — never force users to estimate bulk/liquid amounts
