# GroceryGenie 🛒

A **conversational grocery inventory manager** for the Indian household. Send your bot a photo of a receipt — it scans the items, asks the right follow-up questions, and remembers what's in your pantry. Saturday morning, it tells you exactly what to reorder and where it's cheapest.

> Built as a portfolio project to learn AI PM concepts end-to-end: multimodal AI, structured extraction, agentic systems, prompt engineering, and production-grade reliability patterns.

---

## Why this project exists

Ten-minute delivery apps (Zepto, Blinkit, Swiggy Instamart) have made it too easy for households to order grocery items the moment they run out — usually mid-cooking. The result: cooks and maids stop giving advance notice, the household manager makes 4-5 panic orders a week, monthly grocery spend is invisible, and duplicate purchases pile up.

**GroceryGenie consolidates the chaos into one planned weekly order.** Photo-driven logging, Saturday reorder list, automatic platform memory, mid-week alerts only when something is truly urgent.

---

## What it can do today (Phase 1)

- **📷 Scan receipt photos and PDF invoices** with Gemini 2.5 Flash (multimodal)
- **🧠 Classify every item** as countable (carrots, eggs) or estimated (oil, rice, atta) — because nobody measures oil while cooking
- **🤔 Ask about ambiguous items** one at a time, with a Skip button — never hallucinate quantities
- **📸 Merge multiple photos** into a single order (long receipts often span 2-3 photos)
- **💳 Confirm payment method** via inline buttons (UPI / Card / Cash)
- **💾 Persist confirmed orders** in a single SQLite transaction
- **🌙 8 PM IST daily nudge** — sends a Telegram reminder if you haven't logged anything today
- **🔁 Automatic retry with exponential backoff** when Gemini's free tier hits capacity (503/UNAVAILABLE)

[Detailed phase plan ➜](docs/PRD.md#4-feature-requirements)

---

## Tech stack

| Component | Choice | Why | Cost |
|-----------|--------|-----|------|
| Language | **Go 1.22+** | Single-binary deploys, strong concurrency, production-grade tooling | Free |
| AI | **Gemini 2.5 Flash** (free tier) | Multimodal (text + vision + PDF), generous quota | Free |
| Bot interface | **Telegram Bot API** via `telebot.v3` | No business verification, swappable to WhatsApp later | Free |
| Database | **SQLite** via `modernc.org/sqlite` | Zero setup, single file, sufficient for single-user MVP | Free |
| Scheduler | **`robfig/cron/v3`** | In-process cron, no external services | Free |
| Hosting (MVP) | Local laptop | Will move to Cloud Run for Phase 2+ | Free |

**Total running cost: ₹0/month.**

---

## Architecture

```
User (Telegram) ──► telebot ──► Bot handlers ──► Receipt Agent ──► Gemini Vision API
                                     │
                                     ├──► SessionStore (in-memory, 10-min TTL)
                                     │
                                     ├──► Inventory tools ──► SQLite (10 tables)
                                     │
                                     └──► Activity + Settings stores
                                                  ▲
                                                  │
                              Scheduler (cron) ───┘
                              ─ 8 PM nudge
                              ─ (Saturday report — Phase 3)
```

[Full architecture ➜](docs/TECHNICAL_DESIGN.md)

---

## Repository layout

```
.
├── cmd/bot/main.go              ← entry point — wires all dependencies
├── internal/
│   ├── agents/receipt.go         ← Gemini Vision call + retry logic
│   ├── database/                 ← SQLite connection + schema.sql (10 tables)
│   ├── prompts/                  ← System prompts (Markdown) embedded at build
│   ├── scheduler/                ← Cron jobs (8 PM nudge)
│   ├── telegram/                 ← Bot handlers + inline buttons + sessions
│   └── tools/                    ← Inventory, Activity, Settings (DB-backed)
├── docs/
│   ├── PRD.md                    ← Product requirements (6 phases)
│   ├── TECHNICAL_DESIGN.md       ← Architecture + schema + algorithms
│   └── EVAL_FRAMEWORK.md         ← AI quality evals (5 dimensions)
├── scripts/run-bot.sh            ← Kills stale bots and starts a fresh one
├── CLAUDE.md                     ← Single source of truth for new AI sessions
└── go.mod / go.sum
```

---

## Running it locally

**Prerequisites:**
- Go 1.22+
- A Telegram bot token (via `@BotFather` on Telegram)
- A free Gemini API key from [aistudio.google.com/apikey](https://aistudio.google.com/apikey)

**Setup:**
```bash
git clone https://github.com/kaustubhi-shukla/grocery-genie
cd grocery-genie

# Create .env with your keys
cat > .env <<EOF
TELEGRAM_BOT_KEY=your_token_from_botfather
GEMINI_API_KEY=your_key_from_aistudio
EOF

# Run
./scripts/run-bot.sh
```

Then DM your bot on Telegram. Send `/start` to register, then send any grocery receipt photo or PDF.

---

## Roadmap

| Phase | Focus | Status |
|-------|-------|--------|
| 1 | Foundation + Receipt Scanning | ✅ Complete |
| 2 | Usage Tracking + NLP (meal logging) | Next |
| 3 | Smart Reminders + Subscription Detection | Planned |
| 4 | Waste Tracking + Platform Quality Scoring | Planned |
| 5 | Budget + Spend Intelligence | Planned |
| 6 | Guest Mode + Cook Access (Kannada support) | Planned |

[Full roadmap ➜](docs/PRD.md#phase-plan)

---

## What this project taught me (AI PM angle)

- **Multimodal AI** is genuinely useful for input-friction problems — photo > typing every time
- **Prompt engineering is product design** — the receipt-parser system prompt is treated as a versioned artifact, edited and reviewed like any other product spec
- **Precision > recall for parsers** — silent errors (hallucinated items) are worse than visible gaps (missed items)
- **Stateful conversations in stateless infrastructure** — Telegram delivers one message at a time, but the confirmation flow needs memory across many turns
- **Production AI is reliability engineering** — retry with backoff, transient-vs-permanent error classification, timeouts, idempotency. The model is the easy part.

[Eval framework with metrics + interview talking points ➜](docs/EVAL_FRAMEWORK.md)

---

_Built by [Kaustubhi Shukla](https://github.com/kaustubhi-shukla) — PM learning to build, one receipt scan at a time._
