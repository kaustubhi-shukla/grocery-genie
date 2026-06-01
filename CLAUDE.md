# GroceryGenie — Project Brief

> This document is the single source of truth for this project. Use it to start new sessions without hallucinations. Everything decided is here; everything not mentioned is undecided.

## Implementation status

| Phase | Description | Status |
|-------|-------------|--------|
| 1 | Foundation + Receipt Scanning + Confirmation + 8 PM Nudge | **✅ Complete** (tagged `v0.1.0`) |
| 2 | Usage Tracking + NLP meal logging | Not started |
| 3 | Smart Reminders + Subscription Detection | Not started |
| 4 | Waste Tracking + Platform Quality | Not started |
| 5 | Budget + Spend Intelligence | Not started |
| 6 | Guest Mode + Cook Access (Kannada) | Not started |

**What Phase 1 actually delivered (matches PRD F1.1–F1.7):**
- Telegram bot (`@kaustubhi_grocery_bot`)
- Receipt scanning via Gemini 2.5 Flash (images + PDFs)
- Multi-photo orders merged into one session (10-min TTL)
- Ambiguous-item resolution one at a time (with Skip)
- Inline buttons: Add more / Done / Cancel / payment method
- DB persistence: items auto-created, purchases logged, inventory upserted (countable adds, estimated replaces)
- 8 PM IST daily nudge via robfig/cron (idempotent — once per day max)
- In-place message editing for clean UX (Scanning… → Saved!)
- Exponential-backoff retry on Gemini 503/UNAVAILABLE
- Logs tee'd to `bot.log` for debugging

**Known gaps (deferred to later phases):**
- No NLP text parsing yet (Phase 2)
- No /stock query yet (Phase 2)
- No Saturday report yet (Phase 3)
- No waste, budget, guest, cook features yet (Phases 4–6)
- No ADK agent framework yet — we use Gemini SDK directly. ADK will be introduced in Phase 2 when we have multiple agents to coordinate.

## What is this?

GroceryGenie is a **Telegram bot** that acts as a conversational grocery inventory manager for an urban Indian household (Bengaluru). The user (a household manager with a cook/maid) texts the bot about what they bought, cooked, and wasted. The bot tracks inventory, predicts when items will run out, and sends a weekly shopping list every Saturday.

## The problem it solves

10-minute delivery apps (Zepto, Blinkit, Swiggy Instamart) have eliminated the incentive for cooks/maids to give advance notice when ingredients run low. They report shortages at the last moment, causing daily panic orders across multiple apps. This leads to overspending, no budget visibility, food waste, and duplicate purchases.

**Goal:** Consolidate grocery ordering to once a week (Sundays). Eliminate panic orders. Track where money goes.

## Tech stack (finalized)

| Component | Choice | Why | Cost |
|-----------|--------|-----|------|
| Language | **Go 1.22+** | Production-grade, single binary, great concurrency. Chosen for resume impact and learning. | Free |
| Agent framework | **Google ADK Go v1.3** | Multi-agent architecture with tool use. Agents route user messages and call Go functions. | Free |
| AI model | **Gemini 2.5 Flash (free tier)** | Multimodal (text + vision for receipt scanning). Free tier covers all usage for single household. | Free |
| Bot interface | **Telegram Bot API** (via telebot/v3) | Free, no business verification needed, easy API. Swappable to WhatsApp later by changing only the messaging layer. | Free |
| Database | **SQLite** (via go-sqlite3) | Zero setup, single file, sufficient for single-user MVP. Upgradeable to PostgreSQL. | Free |
| Scheduler | **robfig/cron** | Go cron library for Saturday reminders, runs in-process. | Free |
| Hosting | **User's laptop** (MVP). Later: Google Cloud Run. | ADK is optimized for Cloud Run. Free tier available. | Free |

**Total running cost: Rs 0/month.**

Google ADK Go repo: https://github.com/google/adk-go
Gemini API pricing: https://ai.google.dev/gemini-api/docs/pricing

## Data persistence: database is memory, not the AI

The AI (Gemini) is stateless — it processes each message independently and forgets. But every parsed result is saved to SQLite permanently. New agents added in later phases can query ALL historical data from Day 1. The chat is temporary; the database is forever.

```
  AI (Gemini) = temporary brain       Database (SQLite) = permanent memory
  Parses "used 2 carrots" → JSON      Stores: item=carrot, qty=2, date, recipe
  Then forgets the conversation        Keeps it forever. Queryable by any agent.
```

This means: if you add a Budget Agent in Month 3, it can analyze ALL purchase data from Month 1 onward. No data is lost.

## Architecture: multi-agent via ADK

The bot uses Google ADK's multi-agent architecture. A Root Agent receives every message and routes to specialized sub-agents. Each sub-agent has access to Go tool functions that read/write the database.

```
User message → Telegram Bot → Root Agent (ADK)
                                 ├── Receipt Agent (scans photos via Gemini Vision)
                                 ├── Usage Agent (parses "made dal, used 1 cup dal")
                                 ├── Stock Agent (answers /stock, generates reports)
                                 ├── Budget Agent (tracks spend, payment methods)
                                 └── Reminder Agent (Saturday summaries, subscription alerts)
                                        │
                                        ▼
                                   Go Tool Functions
                                   (add_purchase, deduct_stock, log_waste, etc.)
                                        │
                                        ▼
                                   SQLite Database
```

## Key user flows

### Flow 1: Receipt scanning (Phase 1 priority)
```
User sends receipt photo via Telegram
  → Receipt Agent sends image to Gemini Vision
  → Gemini extracts: items, quantities, prices, store name
  → Bot replies: "Found: 4 carrots (Rs 40), 5kg rice (Rs 450) from FirstClub. Correct?"
  → User confirms and provides payment method: "yes, UPI"
  → Bot logs purchase, updates inventory, records spend
```
**Critical rule:** Bot ALWAYS confirms platform with user before logging. Never assume.

### Flow 2: Usage logging (Phase 2)
```
User texts: "Made biryani, used 2 onions, 3 tomatoes, rice, oil and spices"
  → Usage Agent parses via Gemini → identifies countable vs estimated items
  → Countable (onions, tomatoes): deducts precise quantities from stock
  → Estimated (rice, oil, spices): logs usage event WITHOUT stock deduction
  → Logs recipe + all ingredients (learns over time)
  → Replies: "Onions: 3 left | Tomatoes: 0 (REORDER NOW)
              Rice, oil, spices: usage logged (tracked by order cycle)"
```
**Critical rules:**
- Never force users to estimate quantities for bulk/liquid items. Accept "used oil" without a quantity.
- BUT if user volunteers a relative quantity ("half the bottle of oil used"), honour it — recalculate remaining stock and trigger mid-week alert if urgent.
- If usage is guest/party-related ("made sweets for guests"), tag it and exclude from baseline forecasting.
- Bot always asks for missing details (payment method, recipe name, platform) — never silently accepts incomplete data. User can say "skip" for optional fields.

### Flow 3: Saturday reminder (Phase 3)
```
Every Saturday morning, bot sends:
  REORDER NOW:
    Carrots — 1 left (you use ~3/week)                [countable, stock-based]
    Cooking oil — last bought 23 days ago              [estimated, interval-based]
      (you reorder every ~25 days)
  
  RUNNING LOW:
    Tomatoes — 3 left (~4 days)                        [countable]
    Rice — bought 18 days ago (cycle: ~21 days)        [estimated]
  
  BUY FROM:
  - FirstClub: carrots, tomatoes, oil, rice
  - Zepto: paneer
  
  BUDGET: Rs 3,200 of Rs 8,000 spent this month
```

### Flow 4: Waste logging (Phase 4)
```
User texts: "Threw away 3 tomatoes, went bad"
  → Logged as waste (excluded from usage-based predictions)
  → Bot learns shelf life: "tomatoes last ~4 days in your household"
  → Future: suggests buying fewer: "Buy 8 not 12 — you wasted 4 last week"
```

## Countable vs Estimated items (critical design concept)

Not all grocery items can be tracked with precise quantities. The system handles two types:

| | Countable | Estimated |
|--|-----------|-----------|
| **Examples** | Carrots, eggs, tomatoes, paneer | Oil, ghee, rice, flour, salt, spices |
| **User says** | "Used 2 carrots" (quantity given) | "Used oil and rice for biryani" (no quantity) |
| **Stock tracking** | Precise: 4 → 2 → 0 | Not tracked precisely — inventory set on purchase, not decremented on use |
| **Restock signal** | Depletion math: current_stock / daily_usage | Purchase interval: "You buy oil every ~25 days, due in 5 days" |
| **Why** | User naturally counts these | Nobody measures oil while cooking — forcing it kills engagement |

**Guest/party handling for estimated items:** Party purchases are tagged and excluded from purchase-interval calculations. Without this, a Diwali bulk oil buy would make the bot think you buy oil twice as often.

## Database schema (10 tables)

- **items**: master list (name, category, unit, **tracking_type**: "countable" or "estimated")
- **inventory**: current stock levels per item (meaningful for countable; set-but-not-decremented for estimated)
- **purchases**: every buy event (item, qty, price, **price_per_unit** (computed), platform, payment method, receipt image, confirmed flag)
- **usage**: every usage/waste event (item, qty, quantity_type (exact/relative/none), relative_amount (0.5 = half), recipe name, is_waste flag, waste_source_purchase (links waste to purchase for platform quality), is_guest_usage flag)
- **outside_meals**: meals ordered from outside (meal name, platform like Zomato/Swiggy, cost, timestamp)
- **daily_log_tracker**: tracks if user logged anything each day (for 8 PM nudge)
- **recipes**: learned recipes (name, times made)
- **recipe_ingredients**: ingredients per recipe with averaged quantities
- **guest_events**: guest occasions excluded from forecasting
- **subscriptions**: auto-detected purchase patterns (interval, quantity, platform, confidence, next order date, guest-adjusted flag). **Primary restock signal for estimated items.**

Full schema is in docs/TECHNICAL_DESIGN.md section 2.

## Phase plan

| Phase | Weeks | What it adds | Milestone |
|-------|-------|-------------|-----------|
| **1: Foundation + Receipt Scan** | 1-2 | Telegram bot, receipt photo scanning (Gemini Vision), platform confirmation (always confirm), image ambiguity confirmation, basic inventory, spend logging, 8 PM daily logging nudge, proactive follow-ups for missing details | "I can log purchases by sending a photo" |
| **2: Usage Tracking + NLP** | 3-4 | Natural language meal logging, countable vs estimated item handling, relative quantity confirmation ("half the bottle" → confirm against last order), recipe memory, depletion + interval alerts, mid-week urgent alerts, outside meal logging | "The bot knows what I'm cooking and what's running out" |
| **3: Smart Reminders + Subscriptions** | 5-6 | Saturday weekly report, subscription pattern detection, auto-order reminders, platform memory, cross-platform price comparison from receipt history | "The bot tells me what to buy and where it's cheapest" |
| **4: Waste Tracking** | 7-8 | Waste logging, waste-to-platform attribution, platform quality scoring (upfront cost vs effective cost after waste), shelf-life learning, smart quantity suggestions | "The bot helps me waste less food and pick better platforms" |
| **5: Budget + Spend Intelligence** | 9-10 | Monthly budget setting, spend dashboard (by category/platform/payment method), budget alerts, price memory, outside meal spend tracking, homecooked vs outside ratio | "I know exactly where my grocery AND eating-out money goes" |
| **6: Advanced Features** | 11-12 | Guest mode (exclude from forecasting), cook/maid Telegram access with Kannada language support, meal planning, seasonal awareness | "It handles every edge case" |

**Each phase is self-contained — stop at any phase and still have a useful product.**

## Project file structure

```
grocery-app/
├── CLAUDE.md                   ← THIS FILE (project brief for new sessions)
├── docs/
│   ├── PRD.md                  ← Full product requirements document
│   ├── TECHNICAL_DESIGN.md     ← Architecture, schema, algorithms, Go structure
│   └── EVAL_FRAMEWORK.md       ← How to test AI quality (receipt accuracy, NLP, etc.)
├── cmd/
│   └── bot/
│       └── main.go             ← Entry point
├── internal/
│   ├── agents/                 ← ADK agent definitions (root, receipt, usage, stock, budget)
│   ├── tools/                  ← Go functions agents call (inventory, budget, recipes, subscriptions)
│   ├── database/               ← SQLite connection + schema
│   ├── telegram/               ← Telegram bot message handling
│   └── scheduler/              ← Cron jobs for reminders
├── prompts/                    ← System prompts for each agent
├── evals/                      ← Test data + eval runner
├── .env                        ← API keys (NEVER commit)
├── go.mod                      ← Go module definition
└── go.sum                      ← Dependency checksums
```

## Success metrics

| Metric | Target |
|--------|--------|
| Weekly active logging | User logs 5+ days/week |
| Receipt scan accuracy | >90% items correctly extracted |
| Panic orders reduced | <2 emergency orders/month |
| Weekly order adherence | >3 of 4 weeks ordered on Sunday |
| Waste reduction | 20% less in month 2 vs month 1 |

## Eval framework (for AI quality)

Five eval types are defined in docs/EVAL_FRAMEWORK.md:
1. **Receipt scanning accuracy** — precision, recall, quantity/price accuracy on test receipts
2. **NLP text parsing** — intent classification, item extraction, Hinglish handling
3. **Subscription detection** — correctly identifying repeating purchase patterns
4. **Depletion prediction** — accuracy of "will run out in N days" predictions
5. **UX quality (human eval)** — clarity, tone, actionability of bot messages

Key principle: **precision > recall** for receipt scanning (hallucinated items are worse than missed items — silent errors corrupt inventory).

## User context

- **Who**: Product manager, no technical background, based in Bengaluru, India
- **Why building this**: (1) Solve a real personal problem, (2) Portfolio project for resume, (3) Learn AI PM concepts hands-on
- **Learning goals**: Understand agents, evals, prompt engineering, agentic AI, multimodal AI, structured output, tool use — all in interview-ready depth
- **Communication preference**: Explain technical concepts in PM-friendly language with interview context. Every feature should come with "why this matters for AI PM interviews."
- **Household setup**: Has a cook and maid. Buys fruits/veggies from FirstClub. Other groceries from various delivery apps (Zepto, Blinkit, Swiggy Instamart, Amazon, Flipkart Minutes).

## Key product decisions (already made)

1. **Telegram first, not WhatsApp** — free API, no business verification, swap later
2. **Receipt scanning is Phase 1 priority** — reduces input friction, most impressive AI feature
3. **Always confirm platform with user** — never assume which store a receipt is from
4. **Natural language input (Option B)** — not structured commands. Gemini parses "used 2 carrots in curry"
5. **Guest events excluded from forecasting** — spikes from dinner parties don't corrupt baseline predictions
6. **Subscription detection via statistics, not ML** — mean/std of purchase intervals. Switch to ML only at scale.
7. **Waste tracked separately from usage** — different data, different insights
8. **Payment method asked on every receipt** — enables spend-by-payment-method reports
9. **Go + ADK chosen over simpler Python** — harder but more resume impact and learning value
10. **Zero cost architecture** — Gemini free tier, no paid components
11. **Countable vs Estimated items** — items like oil, rice, flour are tracked by purchase intervals (not stock levels) since users won't specify usage quantities. Never force quantity input for bulk/liquid items.
12. **Guest/party purchases excluded from interval calculations** — a Diwali bulk buy shouldn't make the bot think you buy oil twice as often
13. **Relative quantities honoured** — "half the bottle of oil" → recalculate remaining, trigger mid-week alert if urgent
14. **Mid-week urgent alerts** — Don't wait for Saturday if something runs out or user reports large usage mid-week
15. **Proactive follow-ups** — Bot asks for missing details (payment method, recipe name, platform). Never silently accept incomplete data.
16. **Cross-platform price comparison from user's own receipts** — no scraping, just compare prices you've already logged across platforms
17. **Platform quality scoring** — cross-reference waste with purchase platform. Upfront cost vs effective cost after waste. "Zepto is Rs 5/kg cheaper but you waste 35% — FirstClub is actually cheaper."
18. **8 PM daily logging nudge** — if user hasn't logged anything today, send a Telegram reminder at 8 PM IST
19. **Outside meal tracking** — log meals from Zomato/Swiggy/restaurants, track homecooked vs outside ratio and outside meal spend
20. **Kannada language support (Phase 6)** — cook/maid can type in Kannada, Gemini translates to English for storage
21. **Relative quantity confirmation against last order** — "half the bottle of oil" → bot confirms: "You ordered 1L last time, so ~500ml used?" because order sizes can vary
22. **Receipt image ambiguity confirmation** — if any quantity/unit is unclear in the image, confirm via chat before assuming

## What NOT to do

- Don't build a web/mobile UI — Telegram IS the interface
- Don't scrape delivery app prices — no public APIs, fragile, against ToS
- Don't auto-add to delivery app carts — API limitations, Phase 1-6 generate lists only
- Don't use ML for predictions with single-user data — simple statistics work better
- Don't skip the confirmation step on receipt scans — trust requires human-in-the-loop
- Don't store bank/card numbers — only track payment METHOD (UPI/Card/Cash), not details
- Don't force quantity input for bulk/liquid items — accept "used oil" without amounts
- Don't hallucinate quantities — if the user didn't specify how much oil they used, don't invent "50ml"
- Don't assume receipt quantities when image is unclear — always confirm via chat
- Don't assume relative quantity context — "half the bottle" must be confirmed against last purchase qty since order sizes vary
- Don't scrape delivery app prices — compare only from user's own receipt history
- Don't count outside meals as homecooked — they are separate data streams with separate tracking

## Documents to read for full context

1. **docs/PRD.md** — Complete product requirements with feature tables, risks, metrics
2. **docs/TECHNICAL_DESIGN.md** — Architecture, database schema, API integration, algorithms
3. **docs/EVAL_FRAMEWORK.md** — How to test and measure AI quality
