// Package telegram is the thin wrapper around the Telegram Bot API.
//
// All Telegram-specific code lives here. The rest of the app talks
// to Bot via its public methods (Start, Stop). Handler logic uses
// SessionStore to remember in-progress orders across messages, and
// calls into the agents/tools packages for AI and persistence.
package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/kaustubhi-shukla/grocery-genie/internal/agents"
	"github.com/kaustubhi-shukla/grocery-genie/internal/tools"
)

// Bot is the GroceryGenie Telegram bot. It owns its dependencies
// (Telegram client, AI agent, DB tools, session store) so handlers
// can reach them via method receivers without globals.
type Bot struct {
	bot          *tele.Bot
	receiptAgent *agents.ReceiptAgent
	inventory    *tools.Inventory
	activity     *tools.Activity
	settings     *tools.Settings
	sessions     *SessionStore

	// Inline button definitions. Telebot keys handlers off these
	// objects, so we keep them as fields to register handlers in
	// one place and reference them when rendering keyboards.
	btnAddMore   tele.Btn
	btnDone      tele.Btn
	btnCancel    tele.Btn
	btnSkipAmbig tele.Btn
	btnPayUPI    tele.Btn
	btnPayCard   tele.Btn
	btnPayCash   tele.Btn
}

// New creates and configures the bot. It registers all message
// handlers and inline-button callbacks but does NOT start polling.
// Call Start() to begin processing messages.
func New(
	token string,
	receiptAgent *agents.ReceiptAgent,
	inventory *tools.Inventory,
	activity *tools.Activity,
	settings *tools.Settings,
	sessions *SessionStore,
) (*Bot, error) {
	teleConfig := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	telebot, err := tele.NewBot(teleConfig)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	b := &Bot{
		bot:          telebot,
		receiptAgent: receiptAgent,
		inventory:    inventory,
		activity:     activity,
		settings:     settings,
		sessions:     sessions,

		// Inline button "Unique" IDs must be unique within the bot.
		// They're used to identify which button was tapped.
		btnAddMore:   tele.Btn{Unique: "order_add_more", Text: "📸 Add more"},
		btnDone:      tele.Btn{Unique: "order_done", Text: "✅ Done"},
		btnCancel:    tele.Btn{Unique: "order_cancel", Text: "❌ Cancel"},
		btnSkipAmbig: tele.Btn{Unique: "ambig_skip", Text: "⏭ Skip this item"},
		btnPayUPI:    tele.Btn{Unique: "pay_upi", Text: "💸 UPI"},
		btnPayCard:   tele.Btn{Unique: "pay_card", Text: "💳 Card"},
		btnPayCash:   tele.Btn{Unique: "pay_cash", Text: "💵 Cash"},
	}
	b.registerHandlers()
	return b, nil
}

// registerHandlers wires every message type and button to its handler.
func (b *Bot) registerHandlers() {
	b.bot.Use(b.logIncoming)

	// Commands
	b.bot.Handle("/start", b.handleStart)
	b.bot.Handle("/help", b.handleHelp)
	b.bot.Handle("/stock", b.handleStockComingSoon)
	b.bot.Handle("/cancel", b.handleCancelCommand)
	b.bot.Handle("/lastorder", b.handleLastOrder)
	b.bot.Handle("/recent", b.handleRecent)
	b.bot.Handle("/find", b.handleFind)
	b.bot.Handle("/freebie", b.handleMarkFreebie)
	b.bot.Handle("/setqty", b.handleSetQuantity)
	b.bot.Handle("/setprice", b.handleSetPrice)
	b.bot.Handle("/delpurchase", b.handleDeletePurchase)

	// Content types
	b.bot.Handle(tele.OnText, b.handleText)
	b.bot.Handle(tele.OnPhoto, b.handlePhoto)
	b.bot.Handle(tele.OnDocument, b.handleDocument)

	// Inline buttons (must register the pointer, not a copy)
	b.bot.Handle(&b.btnAddMore, b.handleAddMore)
	b.bot.Handle(&b.btnDone, b.handleDoneOrder)
	b.bot.Handle(&b.btnCancel, b.handleCancelOrder)
	b.bot.Handle(&b.btnSkipAmbig, b.handleSkipAmbiguous)
	b.bot.Handle(&b.btnPayUPI, b.makePaymentHandler("UPI"))
	b.bot.Handle(&b.btnPayCard, b.makePaymentHandler("Card"))
	b.bot.Handle(&b.btnPayCash, b.makePaymentHandler("Cash"))
}

// logIncoming logs every incoming message — useful for debugging.
func (b *Bot) logIncoming(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		sender := c.Sender()
		msg := c.Message()
		switch {
		case c.Callback() != nil:
			log.Printf("🔘 from @%s (%s): button=%q",
				sender.Username, sender.FirstName, c.Callback().Unique)
		case msg != nil && msg.Photo != nil:
			log.Printf("📷 from @%s (%s): [photo]", sender.Username, sender.FirstName)
		case msg != nil && msg.Document != nil:
			log.Printf("📎 from @%s (%s): [doc %s, %s]",
				sender.Username, sender.FirstName, msg.Document.FileName, msg.Document.MIME)
		case msg != nil && msg.Text != "":
			log.Printf("💬 from @%s (%s): %q", sender.Username, sender.FirstName, msg.Text)
		default:
			log.Printf("📥 from @%s (%s): [other]", sender.Username, sender.FirstName)
		}
		return next(c)
	}
}

// ----------------------------------------------------------------------
// Static commands
// ----------------------------------------------------------------------

func (b *Bot) handleStart(c tele.Context) error {
	// Remember the chat ID so the 8 PM nudge knows where to post.
	// Telegram's c.Chat() returns the chat (DM or group) the message
	// arrived from. For a private bot, that's the user's DM with us.
	if c.Chat() != nil && b.settings != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		chatIDStr := fmt.Sprintf("%d", c.Chat().ID)
		if err := b.settings.Set(ctx, tools.KeyOwnerChatID, chatIDStr); err != nil {
			log.Printf("saving owner chat id: %v", err)
		}
	}

	return c.Send(`Hi! I'm GroceryGenie 🛒

Send me a photo or PDF of a grocery receipt and I'll scan it.

Try /help for everything I can do.`)
}

func (b *Bot) handleHelp(c tele.Context) error {
	return c.Send(`*What I can do (Phase 1):*

✅ Scan receipt photos and PDF invoices
✅ Ask about items I couldn't read clearly
✅ Confirm payment method with a tap
✅ Save your order to inventory

*Commands:*
/lastorder — show the most recent saved order (verify payment, items)
/recent — show the last 5 saved orders
/find <name> — search purchases by item name (returns IDs)
/freebie <id> — mark a purchase as a freebie
/setqty <id> <num> — fix a purchase's quantity
/setprice <id> <amount> — fix a purchase's price
/delpurchase <id> — delete a purchase entirely
/cancel — abort the in-progress order
/help — this message

*Tips:*
• For screenshots, send as a *file/document* (not photo) for better quality
• Multi-photo orders: send each photo, then tap *Done* when finished
• Tap *Cancel* at any point to abort

*Coming next:*
• Track meals you cook (Phase 2)
• Saturday weekly shopping list (Phase 3)
• Waste tracking + platform quality scores (Phase 4)
• Monthly budget dashboard (Phase 5)`, tele.ModeMarkdown)
}

func (b *Bot) handleStockComingSoon(c tele.Context) error {
	return c.Send("Stock queries arrive in Phase 2. Hang tight!")
}

// handleLastOrder shows the most recently saved order so the user
// can verify what made it into the database (items, payment, total).
// Useful when the user is bulk-uploading history and wants to spot-
// check that nothing slipped through without a payment method.
func (b *Bot) handleLastOrder(c tele.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	last, err := b.inventory.LastOrder(ctx)
	if err != nil {
		log.Printf("/lastorder query: %v", err)
		return c.Send("Couldn't query the last order right now — try again in a moment.")
	}
	if last == nil {
		return c.Send("No saved orders yet. Send a receipt photo to begin!")
	}
	return c.Send(formatOrderSummaryFromDB(last), tele.ModeMarkdown)
}

// handleRecent shows the last 5 saved orders.
func (b *Bot) handleRecent(c tele.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	orders, err := b.inventory.RecentOrders(ctx, 5)
	if err != nil {
		log.Printf("/recent query: %v", err)
		return c.Send("Couldn't query recent orders right now — try again in a moment.")
	}
	if len(orders) == 0 {
		return c.Send("No saved orders yet. Send a receipt photo to begin!")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Last %d order(s):*\n\n", len(orders)))
	for i, o := range orders {
		sb.WriteString(fmt.Sprintf("*%d.* %s · _%s_\n", i+1, o.Platform, o.PaymentMethod))
		sb.WriteString(fmt.Sprintf("   %s · %d items · Rs %.2f\n\n",
			o.PurchasedAt.Format("Jan 2, 15:04"), o.ItemCount, o.TotalRupees))
	}
	return c.Send(sb.String(), tele.ModeMarkdown)
}

// handleFind searches purchases by item name and lists purchase IDs
// the user can pass to the edit commands.
//
// Usage: /find banana
func (b *Bot) handleFind(c tele.Context) error {
	query := strings.TrimSpace(c.Message().Payload)
	if query == "" {
		return c.Send("Usage: `/find <item name>` — e.g., `/find banana`", tele.ModeMarkdown)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	matches, err := b.inventory.FindPurchases(ctx, query, 10)
	if err != nil {
		log.Printf("/find: %v", err)
		return c.Send("Couldn't search right now — try again in a moment.")
	}
	if len(matches) == 0 {
		return c.Send(fmt.Sprintf("No purchases matched %q. Try a shorter or different keyword.", query))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Found %d purchase(s) for %q:*\n\n", len(matches), query))
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("`#%d` · %s · %g %s",
			m.PurchaseID, m.ItemName, m.Quantity, m.Unit))
		if m.IsFreebie {
			sb.WriteString(" · _freebie_")
		} else if m.HasPrice {
			sb.WriteString(fmt.Sprintf(" · Rs %.0f", m.Price))
		}
		sb.WriteString(fmt.Sprintf(" · _%s/%s_\n", m.Platform, m.PaymentMethod))
	}
	sb.WriteString("\nEdit commands:\n")
	sb.WriteString("`/freebie <id>` · `/setqty <id> <number>` · `/setprice <id> <amount>` · `/delpurchase <id>`")
	return c.Send(sb.String(), tele.ModeMarkdown)
}

// handleMarkFreebie marks a purchase as a freebie.
//
// Usage: /freebie 119
func (b *Bot) handleMarkFreebie(c tele.Context) error {
	id, ok := parsePurchaseID(c.Message().Payload)
	if !ok {
		return c.Send("Usage: `/freebie <purchase_id>` — e.g., `/freebie 119`\n_(Find IDs with `/find <item name>`.)_", tele.ModeMarkdown)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	after, err := b.inventory.MarkAsFreebie(ctx, id)
	if err != nil {
		return b.sendEditError(c, err, id)
	}
	return c.Send(fmt.Sprintf(
		"✅ Marked as freebie:\n`#%d` · %s · %g %s · _%s_\n\n_Won't count for replenishment forecasting._",
		after.PurchaseID, after.ItemName, after.Quantity, after.Unit, after.Platform,
	), tele.ModeMarkdown)
}

// handleSetQuantity updates a purchase's quantity and adjusts inventory.
//
// Usage: /setqty 161 4
func (b *Bot) handleSetQuantity(c tele.Context) error {
	id, qty, ok := parseIDAndNumber(c.Message().Payload)
	if !ok {
		return c.Send("Usage: `/setqty <purchase_id> <number>` — e.g., `/setqty 161 4`", tele.ModeMarkdown)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	after, err := b.inventory.SetPurchaseQuantity(ctx, id, qty)
	if err != nil {
		return b.sendEditError(c, err, id)
	}
	return c.Send(fmt.Sprintf(
		"✅ Updated quantity:\n`#%d` · %s · *%g %s*\n_Inventory adjusted._",
		after.PurchaseID, after.ItemName, after.Quantity, after.Unit,
	), tele.ModeMarkdown)
}

// handleSetPrice updates a purchase's price.
//
// Usage: /setprice 119 35
func (b *Bot) handleSetPrice(c tele.Context) error {
	id, price, ok := parseIDAndNumber(c.Message().Payload)
	if !ok {
		return c.Send("Usage: `/setprice <purchase_id> <amount>` — e.g., `/setprice 119 35`", tele.ModeMarkdown)
	}
	if price < 0 {
		return c.Send("Price can't be negative.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	after, err := b.inventory.SetPurchasePrice(ctx, id, price)
	if err != nil {
		return b.sendEditError(c, err, id)
	}
	suffix := ""
	if after.IsFreebie {
		suffix = "\n_Auto-marked as freebie since price was set to 0._"
	}
	return c.Send(fmt.Sprintf(
		"✅ Updated price:\n`#%d` · %s · *Rs %.2f*%s",
		after.PurchaseID, after.ItemName, after.Price, suffix,
	), tele.ModeMarkdown)
}

// handleDeletePurchase removes a purchase entirely.
//
// Usage: /delpurchase 38
func (b *Bot) handleDeletePurchase(c tele.Context) error {
	id, ok := parsePurchaseID(c.Message().Payload)
	if !ok {
		return c.Send("Usage: `/delpurchase <purchase_id>` — e.g., `/delpurchase 38`", tele.ModeMarkdown)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	deleted, err := b.inventory.DeletePurchase(ctx, id)
	if err != nil {
		return b.sendEditError(c, err, id)
	}
	return c.Send(fmt.Sprintf(
		"🗑 Deleted purchase:\n`#%d` · %s · %g %s · _%s_\n_Inventory adjusted (countable items only)._",
		deleted.PurchaseID, deleted.ItemName, deleted.Quantity, deleted.Unit, deleted.Platform,
	), tele.ModeMarkdown)
}

// sendEditError centralises the not-found / generic-error reply for
// the edit commands.
func (b *Bot) sendEditError(c tele.Context, err error, id int64) error {
	if errors.Is(err, tools.ErrPurchaseNotFound) {
		return c.Send(fmt.Sprintf("No purchase with id `%d`. Use `/find <item>` to look up IDs.", id), tele.ModeMarkdown)
	}
	log.Printf("edit failed for purchase %d: %v", id, err)
	return c.Send(fmt.Sprintf("Couldn't update purchase %d — %v", id, err))
}

// parsePurchaseID parses a payload like "119" or " 119 " into an int64.
func parsePurchaseID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// parseIDAndNumber parses "119 4" or "119 35.5" into (id, value).
func parseIDAndNumber(s string) (int64, float64, bool) {
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, false
	}
	v, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, 0, false
	}
	return id, v, true
}

// formatOrderSummaryFromDB renders a saved order for /lastorder.
func formatOrderSummaryFromDB(o *tools.OrderSummary) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*🧾 Last saved order — %s*\n\n", o.Platform))
	sb.WriteString(fmt.Sprintf("📅 *Saved:* %s\n", o.PurchasedAt.Format("Jan 2 2006, 15:04 IST")))
	sb.WriteString(fmt.Sprintf("💳 *Payment:* %s\n", o.PaymentMethod))
	sb.WriteString(fmt.Sprintf("📦 *Items:* %d\n", o.ItemCount))
	sb.WriteString(fmt.Sprintf("💰 *Total:* Rs %.2f\n", o.TotalRupees))
	if len(o.SampleItems) > 0 {
		sb.WriteString("\n*Sample items:*\n")
		for _, name := range o.SampleItems {
			sb.WriteString(fmt.Sprintf("  • %s\n", name))
		}
		if o.ItemCount > len(o.SampleItems) {
			sb.WriteString(fmt.Sprintf("  _…and %d more_\n", o.ItemCount-len(o.SampleItems)))
		}
	}
	return sb.String()
}

// handleCancelCommand — text-based escape hatch in case the buttons
// are inaccessible (older Telegram clients, etc.).
func (b *Bot) handleCancelCommand(c tele.Context) error {
	if b.sessions.Get(c.Sender().ID) == nil {
		return c.Send("You don't have a pending order to cancel.")
	}
	b.sessions.Delete(c.Sender().ID)
	return c.Send("❌ Order cancelled. Nothing was saved.")
}

// ----------------------------------------------------------------------
// Text handler — also used for ambiguous-item replies
// ----------------------------------------------------------------------

func (b *Bot) handleText(c tele.Context) error {
	order := b.sessions.Get(c.Sender().ID)
	if order != nil && order.Stage == stageResolvingAmbiguous && order.CurrentAmbiguous != "" {
		return b.applyAmbiguousAnswer(c, order, c.Text())
	}

	// No pending order — echo (intent parsing lands in Phase 2).
	return c.Send(fmt.Sprintf(
		"You said: %q\n\n(I echo for now. Send a receipt photo/PDF to try the real flow!)",
		c.Text(),
	))
}

// ----------------------------------------------------------------------
// Photo + document handlers — both route to processReceiptInput
// ----------------------------------------------------------------------

func (b *Bot) handlePhoto(c tele.Context) error {
	photo := c.Message().Photo
	if photo == nil {
		return c.Send("I expected a photo but didn't get one. Try again?")
	}
	return b.processReceiptInput(c, photo.File, "image/jpeg")
}

func (b *Bot) handleDocument(c tele.Context) error {
	doc := c.Message().Document
	if doc == nil {
		return c.Send("I expected a file but didn't get one. Try again?")
	}
	mimeType := doc.MIME
	if mimeType == "" {
		mimeType = mimeFromName(doc.FileName)
	}
	if !strings.HasPrefix(mimeType, "image/") && mimeType != "application/pdf" {
		return c.Send("I can read receipt images (JPG/PNG) and PDF invoices. " +
			"This file looks like " + mimeType + " — could you send a photo or PDF instead?")
	}
	return b.processReceiptInput(c, doc.File, mimeType)
}

func (b *Bot) processReceiptInput(c tele.Context, file tele.File, mimeType string) error {
	if err := c.Notify(tele.Typing); err != nil {
		log.Printf("notify typing: %v", err)
	}

	statusMsg, err := b.bot.Send(c.Recipient(), "Scanning your receipt… 🔍")
	if err != nil {
		return fmt.Errorf("sending status message: %w", err)
	}

	fileBytes, err := b.downloadFile(file)
	if err != nil {
		log.Printf("downloading file: %v", err)
		b.editOrSend(statusMsg, c, "Sorry, I couldn't download that file. Could you try again?")
		return nil
	}
	log.Printf("downloaded %d bytes (%s)", len(fileBytes), mimeType)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	receipt, err := b.receiptAgent.Scan(ctx, fileBytes, mimeType)
	if err != nil {
		log.Printf("✗ scan failed: %v", err)

		// Three distinct failure modes, three distinct user messages.

		if errors.Is(err, agents.ErrQuotaExhausted) {
			b.editOrSend(statusMsg, c,
				"🛑 *Daily AI quota hit.*\n\n"+
					"Google's free tier limits how many receipts I can scan per day. "+
					"You've used today's allowance.\n\n"+
					"Options:\n"+
					"• Wait until the daily quota resets (~12:30 PM IST tomorrow)\n"+
					"• Enable billing in Google AI Studio for higher limits\n"+
					"• Ask me to switch to a higher-quota model if accuracy permits\n\n"+
					"_Your already-saved data is safe — nothing here uses the quota._")
			return nil
		}

		if errors.Is(err, agents.ErrTransient) {
			b.editOrSend(statusMsg, c,
				"⚠️ The AI service is busy right now (Google's free tier is in high demand).\n"+
					"I already retried a few times. Could you send the receipt again in a minute?")
			return nil
		}

		b.editOrSend(statusMsg, c,
			"Hmm, I couldn't read this receipt. A few things to try:\n"+
				"• Send as a *document/file* (not compressed photo) for screenshots\n"+
				"• Make sure the full receipt is visible\n"+
				"• PDF invoices work too — drag-drop the file into chat")
		return nil
	}
	log.Printf("✓ scanned receipt: store=%q items=%d total=%.2f confidence=%.2f ambiguous=%d",
		receipt.StoreName, len(receipt.Items), receipt.Total, receipt.Confidence, len(receipt.AmbiguousItems))

	// MergeOrQueue is atomic — it acquires the session mutex once
	// for "is this a bulk upload?" AND "apply the change", removing
	// the race that let two concurrent uploads bypass the queue
	// check and end up merged.
	userID := c.Sender().ID
	queued, snapshot := b.sessions.MergeOrQueue(userID, receipt, bulkUploadThreshold)

	if queued {
		// Tell the user this one is parked; the current order still
		// owns the chat UI (its Done button is what advances things).
		notice := fmt.Sprintf(
			"📥 *Got another receipt — queued (#%d in line)*\n\n"+
				"Found %d items, Rs %.2f from %s.\n\n"+
				"_I'll show this one after you finish saving the current order._",
			len(snapshot.QueuedScans),
			len(receipt.Items), receipt.Total,
			displayStore(receipt.StoreName),
		)
		log.Printf("📥 queued receipt (#%d for user) — concurrent upload detected", len(snapshot.QueuedScans))
		b.editOrSend(statusMsg, c, notice)
		return nil
	}

	// Merge path — replace the "Scanning…" message with the scan
	// summary, then drive ambiguous Q&A or order summary.
	b.editOrSend(statusMsg, c, formatScanSummary(receipt))
	return b.advanceFlow(c)
}

// bulkUploadThreshold is the gap below which a second scan is
// considered a "concurrent upload" rather than a "multi-page receipt".
// Picked to be longer than Telegram delivery + scan latency but
// shorter than a human's review time.
const bulkUploadThreshold = 15 * time.Second

// displayStore returns a non-empty store label for messages — falls
// back to "an unknown store" so messages read naturally.
func displayStore(s string) string {
	if s == "" {
		return "an unknown store"
	}
	return s
}

// advanceFlow decides what to send next based on session state.
// Called after every photo and after every ambiguous-item resolution.
func (b *Bot) advanceFlow(c tele.Context) error {
	order := b.sessions.Get(c.Sender().ID)
	if order == nil {
		return nil
	}
	if len(order.Ambiguous) > 0 {
		return b.askNextAmbiguous(c, order)
	}
	return b.showOrderSummary(c, order)
}

// askNextAmbiguous pops the next ambiguous item and asks about it.
func (b *Bot) askNextAmbiguous(c tele.Context, order *PendingOrder) error {
	next := order.Ambiguous[0]
	b.sessions.Update(order.UserID, func(o *PendingOrder) {
		o.Ambiguous = o.Ambiguous[1:]
		o.CurrentAmbiguous = next
		o.Stage = stageResolvingAmbiguous
	})

	markup := &tele.ReplyMarkup{}
	markup.Inline(markup.Row(b.btnSkipAmbig), markup.Row(b.btnCancel))

	return c.Send(fmt.Sprintf(
		"⚠️ I couldn't read *%s* clearly. What was it? Reply with the correct quantity/name, or tap Skip.",
		next,
	), markup, tele.ModeMarkdown)
}

// applyAmbiguousAnswer accepts the user's clarification for the
// currently-asked ambiguous item. We try to parse a quantity + unit
// out of common patterns ("4", "4 pieces", "2 dozen", "250g"); if
// that succeeds AND the ambiguous item name matches an existing
// scanned item, we UPDATE that item's quantity rather than creating
// a new row. Otherwise we just log the clarification and skip —
// never invent a new item from arbitrary free text. (A previous
// version did, which polluted the DB with sentence-shaped item
// names like "what can't you read in egg it's 2 dozen of eggs".)
//
// Phase 2 will replace this hand-rolled parser with a Gemini-backed
// clarification agent that can handle full natural language.
func (b *Bot) applyAmbiguousAnswer(c tele.Context, order *PendingOrder, answer string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return c.Send("Couldn't parse that — try again, or tap Skip.")
	}

	ambigName := order.CurrentAmbiguous
	qty, unit, parsed := parseQuantityReply(answer)

	if parsed {
		// Try to update the matching item already on the order. We
		// match by case-insensitive substring on either side so that
		// "robusta banana" matches the user saying "bananas" and
		// vice versa.
		updated := false
		b.sessions.Update(order.UserID, func(o *PendingOrder) {
			for i := range o.Items {
				if itemMatches(o.Items[i].Name, ambigName) {
					o.Items[i].Quantity = qty
					if unit != "" {
						o.Items[i].Unit = unit
					}
					updated = true
					break
				}
			}
			o.CurrentAmbiguous = ""
			o.Stage = stageCollectingPhotos
		})

		if updated {
			msg := fmt.Sprintf("✓ Got it: %s set to *%g %s*.", ambigName, qty, unit)
			if err := c.Send(msg, tele.ModeMarkdown); err != nil {
				return err
			}
			return b.advanceFlow(c)
		}
	}

	// Couldn't confidently parse the reply into a structured update —
	// log it for future eval data and acknowledge without creating
	// junk items. The user can /cancel and re-upload if needed.
	log.Printf("ambiguous-reply unparsed: item=%q reply=%q", ambigName, answer)

	b.sessions.Update(order.UserID, func(o *PendingOrder) {
		o.CurrentAmbiguous = ""
		o.Stage = stageCollectingPhotos
	})

	hint := fmt.Sprintf(
		"Noted, but I couldn't structure your reply yet. For now I'll skip *%s*.\n"+
			"_(Tip: try short replies like \"4\", \"4 pieces\", \"2 dozen\", or \"250g\". Full natural-language clarification arrives in Phase 2.)_",
		ambigName,
	)
	if err := c.Send(hint, tele.ModeMarkdown); err != nil {
		return err
	}
	return b.advanceFlow(c)
}

// parseQuantityReply pulls a quantity + optional unit out of short
// replies people actually type. Returns parsed=false on anything
// it can't confidently structure (sentences, free text).
//
// Accepted forms (case-insensitive, whitespace flexible):
//
//	"4"             -> 4, "pieces"
//	"4 pieces"      -> 4, "pieces"
//	"4 pcs"         -> 4, "pieces"
//	"2 dozen"       -> 24, "pieces"
//	"250g" / "250 gms" / "250 grams" -> 250, "g"
//	"1.5 kg"        -> 1.5, "kg"
//	"500 ml"        -> 500, "ml"
//	"1 litre"       -> 1, "litres"
//	"half a bottle" -> not parsed (free text, returns false)
func parseQuantityReply(s string) (qty float64, unit string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(s))
	// Strip common filler so "set to 4 pieces" still parses.
	for _, p := range []string{"set to ", "make it ", "it's ", "its ", "actually ", "qty ", "quantity "} {
		lower = strings.TrimPrefix(lower, p)
	}

	// Walk the first token: must start with a number.
	var numStr string
	for _, r := range lower {
		if (r >= '0' && r <= '9') || r == '.' {
			numStr += string(r)
			continue
		}
		break
	}
	if numStr == "" {
		return 0, "", false
	}
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil || n <= 0 {
		return 0, "", false
	}

	// Whatever's after the number tells us the unit.
	rest := strings.TrimSpace(strings.TrimPrefix(lower, numStr))
	switch {
	case rest == "" || rest == "pcs" || rest == "pc" || rest == "piece" || rest == "pieces" || rest == "nos" || rest == "no":
		return n, "pieces", true
	case rest == "dozen" || rest == "dz":
		return n * 12, "pieces", true
	case strings.HasPrefix(rest, "kg") || strings.HasPrefix(rest, "kilo"):
		return n, "kg", true
	case rest == "g" || strings.HasPrefix(rest, "gm") || strings.HasPrefix(rest, "gram") || rest == "grm":
		return n, "g", true
	case strings.HasPrefix(rest, "ml") || strings.HasPrefix(rest, "milli"):
		return n, "ml", true
	case strings.HasPrefix(rest, "l") || strings.HasPrefix(rest, "lit"):
		return n, "litres", true
	case strings.HasPrefix(rest, "pack") || strings.HasPrefix(rest, "packet"):
		return n, "packets", true
	case strings.HasPrefix(rest, "bunch"):
		return n, "bunch", true
	}
	// Number followed by free text we don't recognise — bail out so
	// we don't guess wrong.
	return 0, "", false
}

// itemMatches returns true if the item already on the order seems
// to refer to the same product the ambiguous reply is about. We use
// a loose substring match in either direction so plural/singular and
// modifier words ("robusta banana" vs "banana") still align.
func itemMatches(itemName, ambig string) bool {
	a := strings.ToLower(strings.TrimSpace(itemName))
	b := strings.ToLower(strings.TrimSpace(ambig))
	if a == "" || b == "" {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// handleSkipAmbiguous — user tapped Skip on the current ambiguous item.
func (b *Bot) handleSkipAmbiguous(c tele.Context) error {
	_ = c.Respond() // acknowledge the button tap so the spinner goes away

	order := b.sessions.Get(c.Sender().ID)
	if order == nil {
		return c.Send("This order's session has expired. Send a fresh receipt to start over.")
	}
	b.sessions.Update(order.UserID, func(o *PendingOrder) {
		o.CurrentAmbiguous = ""
		o.Stage = stageCollectingPhotos
	})

	// Remove the inline keyboard from the prior question — visual cue
	// that we've moved on.
	if c.Message() != nil {
		_, _ = b.bot.Edit(c.Message(), c.Message().Text+"\n_(skipped)_", tele.ModeMarkdown)
	}

	return b.advanceFlow(c)
}

// ----------------------------------------------------------------------
// Order summary + action buttons (Add more / Done / Cancel)
// ----------------------------------------------------------------------

func (b *Bot) showOrderSummary(c tele.Context, order *PendingOrder) error {
	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(b.btnAddMore, b.btnDone),
		markup.Row(b.btnCancel),
	)
	return c.Send(formatOrderSummary(order), markup, tele.ModeMarkdown)
}

func (b *Bot) handleAddMore(c tele.Context) error {
	_ = c.Respond()
	if b.sessions.Get(c.Sender().ID) == nil {
		b.neutraliseStaleButtons(c)
		return c.Send("That order is already finished. Send a fresh receipt to start a new one.")
	}
	// User EXPLICITLY wants the next upload merged into this order.
	// Reset the last-scan timestamp so the bulk-upload heuristic
	// doesn't queue it.
	b.sessions.Update(c.Sender().ID, func(o *PendingOrder) {
		o.LastScanFinishedAt = time.Time{}
	})
	return c.Send("📸 Sure — send the next photo/PDF whenever you're ready (I'll merge it into this order).")
}

func (b *Bot) handleCancelOrder(c tele.Context) error {
	_ = c.Respond()
	order := b.sessions.Get(c.Sender().ID)
	if order == nil {
		// Stale Cancel tap on a previously-saved order — silently
		// neutralise the buttons so it doesn't happen again.
		b.neutraliseStaleButtons(c)
		return nil
	}
	// Preserve any queued scans — the user only cancelled THIS order,
	// not the bulk-upload batch they may have started.
	queued := append([]*agents.Receipt(nil), order.QueuedScans...)
	b.sessions.Delete(order.UserID)
	if c.Message() != nil {
		_, _ = b.bot.Edit(c.Message(), "❌ Order cancelled. Nothing saved.")
	}
	b.promoteNextQueuedScan(c, queued)
	return nil
}

func (b *Bot) handleDoneOrder(c tele.Context) error {
	_ = c.Respond()
	order := b.sessions.Get(c.Sender().ID)
	if order == nil {
		// Stale Done tap — the order was already saved/cancelled.
		// Neutralise the buttons so the user can't tap them again.
		b.neutraliseStaleButtons(c)
		return nil
	}
	if len(order.Items) == 0 {
		return c.Send("No items in the order yet — please send a receipt first.")
	}

	b.sessions.Update(order.UserID, func(o *PendingOrder) {
		o.Stage = stageAwaitingPayment
	})

	markup := &tele.ReplyMarkup{}
	markup.Inline(
		markup.Row(b.btnPayUPI, b.btnPayCard, b.btnPayCash),
		markup.Row(b.btnCancel),
	)

	store := order.Store
	if store == "" {
		store = "this order"
	}
	prompt := fmt.Sprintf(
		"💳 *How did you pay* for %s?\n_(Rs %.2f total)_",
		store, order.Total,
	)
	sent, err := b.bot.Send(c.Recipient(), prompt, markup, tele.ModeMarkdown)
	if err == nil && sent != nil {
		b.sessions.Update(order.UserID, func(o *PendingOrder) {
			o.PaymentBtnMsgID = sent.ID
		})
	}
	return err
}

// ----------------------------------------------------------------------
// Payment buttons → save to DB → confirmation message
// ----------------------------------------------------------------------

// neutraliseStaleButtons edits the message containing the tapped
// button to remove its keyboard and label it as a stale message.
// Called when a button tap arrives for a session that no longer
// exists (order was saved/cancelled, or session timed out).
func (b *Bot) neutraliseStaleButtons(c tele.Context) {
	msg := c.Message()
	if msg == nil {
		return
	}
	// Strip the inline keyboard by passing an empty ReplyMarkup, and
	// append a note so it's obvious this message is no longer actionable.
	newText := msg.Text + "\n\n_⏎ This order is no longer active. /lastorder to see what's been saved._"
	if _, err := b.bot.Edit(msg, newText, tele.ModeMarkdown, &tele.ReplyMarkup{}); err != nil {
		log.Printf("neutralise stale buttons: %v", err)
	}
}

// makePaymentHandler returns a handler bound to a specific payment
// method label. Using a factory avoids three near-identical methods.
func (b *Bot) makePaymentHandler(method string) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		order := b.sessions.Get(c.Sender().ID)
		if order == nil {
			// Stale payment tap — order was already saved or cancelled.
			b.neutraliseStaleButtons(c)
			return nil
		}

		// Edit the payment-buttons message to a "Saving…" status,
		// preventing accidental double-taps and showing progress.
		if c.Message() != nil {
			_, _ = b.bot.Edit(c.Message(), fmt.Sprintf("💳 Payment: *%s* — saving order…", method), tele.ModeMarkdown)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		saved, err := b.inventory.SaveConfirmedOrder(
			ctx, order.Items, order.Store, method, "", // receipt_image_ref TODO once we store images
			order.Date, // empty string = "now"; otherwise YYYY-MM-DD from Gemini
		)
		if err != nil {
			log.Printf("save failed: %v", err)
			if c.Message() != nil {
				_, _ = b.bot.Edit(c.Message(), "❌ Something went wrong saving the order. Please try again.")
			}
			return nil
		}

		// Capture the queue BEFORE we delete the session — those
		// receipts arrived while this order was open and need to be
		// handled next.
		queued := append([]*agents.Receipt(nil), order.QueuedScans...)
		b.sessions.Delete(order.UserID)

		// Mark today as "logged" — keeps the 8 PM nudge from firing.
		if b.activity != nil {
			if err := b.activity.MarkLogged(ctx); err != nil {
				log.Printf("marking activity: %v", err)
			}
		}

		// Replace the "Saving…" status with the success summary.
		summary := formatSaveSuccess(order.Store, method, saved)
		if c.Message() != nil {
			_, err = b.bot.Edit(c.Message(), summary, tele.ModeMarkdown)
		} else {
			err = c.Send(summary, tele.ModeMarkdown)
		}
		if err != nil {
			log.Printf("send success summary: %v", err)
		}

		// If more receipts are queued, immediately start the next
		// order so the user can keep moving.
		b.promoteNextQueuedScan(c, queued)
		return nil
	}
}

// promoteNextQueuedScan pops the first receipt off a queue and uses
// it to start a fresh PendingOrder. Any remaining queued receipts
// carry forward into the new session. Called after Done-save and
// after Cancel.
func (b *Bot) promoteNextQueuedScan(c tele.Context, queued []*agents.Receipt) {
	if len(queued) == 0 {
		return
	}
	userID := c.Sender().ID
	next := queued[0]
	remaining := queued[1:]

	newOrder := b.sessions.Create(userID)
	b.sessions.Update(userID, func(o *PendingOrder) {
		o.Items = append(o.Items, next.Items...)
		o.Total = next.Total
		o.Store = next.StoreName
		o.Date = next.Date
		o.Ambiguous = append(o.Ambiguous, next.AmbiguousItems...)
		o.LastScanFinishedAt = time.Now()
		for _, r := range remaining {
			o.QueuedScans = append(o.QueuedScans, r)
		}
	})
	_ = newOrder // sessions.Create already wrote it; this just silences unused-var lint

	announce := fmt.Sprintf("▶️ *Next queued receipt — %s*", displayStore(next.StoreName))
	if err := c.Send(announce, tele.ModeMarkdown); err != nil {
		log.Printf("announce next queued: %v", err)
	}
	if err := b.advanceFlow(c); err != nil {
		log.Printf("advance after queue promotion: %v", err)
	}
}

// ----------------------------------------------------------------------
// File download + small helpers
// ----------------------------------------------------------------------

func (b *Bot) downloadFile(file tele.File) ([]byte, error) {
	rc, err := b.bot.File(&file)
	if err != nil {
		return nil, fmt.Errorf("getting file reader: %w", err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return nil, fmt.Errorf("reading file bytes: %w", err)
	}
	return buf.Bytes(), nil
}

func (b *Bot) editOrSend(statusMsg *tele.Message, c tele.Context, text string) {
	if statusMsg != nil {
		if _, err := b.bot.Edit(statusMsg, text, tele.ModeMarkdown); err == nil {
			return
		} else {
			log.Printf("edit failed, sending new message instead: %v", err)
		}
	}
	if err := c.Send(text, tele.ModeMarkdown); err != nil {
		log.Printf("send fallback failed: %v", err)
	}
}

func mimeFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// ----------------------------------------------------------------------
// Formatting helpers (kept at the bottom — they're pure functions
// that don't depend on Bot state, easy to unit-test later)
// ----------------------------------------------------------------------

func formatScanSummary(r *agents.Receipt) string {
	var sb strings.Builder
	store := r.StoreName
	if store == "" {
		store = "_unknown store_"
	}
	sb.WriteString(fmt.Sprintf("📄 *Scanned* — %s", store))
	if r.Date != "" {
		sb.WriteString(fmt.Sprintf(" (%s)", r.Date))
	}
	sb.WriteString("\n\n")
	if len(r.Items) == 0 {
		sb.WriteString("Didn't find any items. Try a clearer photo.")
		return sb.String()
	}
	sb.WriteString("*Items in this photo:*\n")
	for _, it := range r.Items {
		sb.WriteString(formatItemLine(it))
	}
	if r.Total > 0 {
		sb.WriteString(fmt.Sprintf("\n_Photo total: Rs %.2f_", r.Total))
	}
	return sb.String()
}

func formatOrderSummary(o *PendingOrder) string {
	var sb strings.Builder
	store := o.Store
	if store == "" {
		store = "_unknown store_"
	}
	sb.WriteString(fmt.Sprintf("📦 *Order so far — %s*\n", store))

	// Show the date Gemini extracted (or note that it's missing) so
	// the user can spot wrong dates BEFORE saving — critical when
	// uploading historical receipts.
	if o.Date != "" {
		sb.WriteString(fmt.Sprintf("🗓 Date: *%s*\n", o.Date))
	} else {
		sb.WriteString("🗓 Date: _not detected — will use today_\n")
	}
	sb.WriteString(fmt.Sprintf("%d item(s), Rs %.2f\n\n", len(o.Items), o.Total))
	for _, it := range o.Items {
		sb.WriteString(formatItemLine(it))
	}
	sb.WriteString("\n_Tap below to add more photos, finish, or cancel._")
	sb.WriteString("\n_If the date above is wrong for a historical receipt, cancel and reply with the date in your next message — date editing lands in the next iteration._")
	return sb.String()
}

func formatItemLine(it agents.ReceiptItem) string {
	qty := fmt.Sprintf("%g", it.Quantity)
	if it.Unit != "" {
		qty = fmt.Sprintf("%s %s", qty, it.Unit)
	}
	line := fmt.Sprintf("  • %s %s", qty, it.Name)
	if it.Price > 0 {
		line += fmt.Sprintf(" — Rs %.0f", it.Price)
	}
	if it.TrackingType != "" {
		line += fmt.Sprintf(" _(%s)_", it.TrackingType)
	}
	return line + "\n"
}

func formatSaveSuccess(store, paymentMethod string, saved []tools.SavedPurchase) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ *Saved!* %d items from %s via %s\n\n",
		len(saved), store, paymentMethod))
	sb.WriteString("*Stock updated:*\n")
	for _, s := range saved {
		if s.TrackingType == "estimated" {
			sb.WriteString(fmt.Sprintf("  • %s — tracked by purchase cycle\n", s.ItemName))
		} else {
			sb.WriteString(fmt.Sprintf("  • %s — %g %s in stock\n", s.ItemName, s.NewStock, s.Unit))
		}
	}
	return sb.String()
}

// ----------------------------------------------------------------------
// Lifecycle
// ----------------------------------------------------------------------

func (b *Bot) Start() {
	log.Println("Telegram bot listening for messages...")
	b.bot.Start()
}

func (b *Bot) Stop() {
	b.bot.Stop()
}

// NotifyOwner sends a Markdown message to the owner's chat. Used by
// the scheduler for the 8 PM nudge and (later) Saturday reports.
// Implements scheduler.Notifier.
func (b *Bot) NotifyOwner(ctx context.Context, text string) error {
	if b.settings == nil {
		return fmt.Errorf("settings store not configured")
	}
	chatIDStr, err := b.settings.Get(ctx, tools.KeyOwnerChatID)
	if err != nil {
		return fmt.Errorf("looking up owner chat id: %w", err)
	}
	if chatIDStr == "" {
		return fmt.Errorf("owner chat id not set — owner has not /start'd the bot yet")
	}

	var chatID int64
	if _, err := fmt.Sscanf(chatIDStr, "%d", &chatID); err != nil {
		return fmt.Errorf("parsing chat id %q: %w", chatIDStr, err)
	}

	chat := &tele.Chat{ID: chatID}
	if _, err := b.bot.Send(chat, text, tele.ModeMarkdown); err != nil {
		return fmt.Errorf("sending to owner chat %d: %w", chatID, err)
	}
	return nil
}
