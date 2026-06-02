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

	// Append to any existing pending order, or start a new one.
	userID := c.Sender().ID
	order := b.sessions.Get(userID)
	if order == nil {
		order = b.sessions.Create(userID)
	}
	b.sessions.Update(userID, func(o *PendingOrder) {
		o.Items = append(o.Items, receipt.Items...)
		o.Total += receipt.Total
		if o.Store == "" {
			o.Store = receipt.StoreName
		}
		if o.Date == "" {
			o.Date = receipt.Date
		}
		o.Ambiguous = append(o.Ambiguous, receipt.AmbiguousItems...)
	})

	// First: replace the "Scanning…" message with the scan result.
	b.editOrSend(statusMsg, c, formatScanSummary(receipt))

	// Then: drive the next step (ambiguous Q&A or order summary).
	return b.advanceFlow(c)
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
// currently-asked ambiguous item. For MVP we just store the text in
// the item name; future versions will parse "4 pieces" into qty+unit.
func (b *Bot) applyAmbiguousAnswer(c tele.Context, order *PendingOrder, answer string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return c.Send("Couldn't parse that — try again, or tap Skip.")
	}

	b.sessions.Update(order.UserID, func(o *PendingOrder) {
		// MVP: treat the user's reply as the corrected item name and
		// add it as a new countable item with quantity 1. Full NLP
		// parsing of "4 pieces of carrots" lands in Phase 2 alongside
		// the usage-logging Gemini agent.
		o.Items = append(o.Items, agents.ReceiptItem{
			Name:         strings.ToLower(answer),
			Quantity:     1,
			Unit:         "pieces",
			Category:     "other",
			TrackingType: "countable",
		})
		o.CurrentAmbiguous = ""
		o.Stage = stageCollectingPhotos
	})

	if err := c.Send(fmt.Sprintf("✓ Got it: %q added to the order.", answer)); err != nil {
		return err
	}
	return b.advanceFlow(c)
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
		return c.Send("Your session has expired. Start with a fresh receipt photo.")
	}
	return c.Send("📸 Sure — send the next photo/PDF whenever you're ready.")
}

func (b *Bot) handleCancelOrder(c tele.Context) error {
	_ = c.Respond()
	order := b.sessions.Get(c.Sender().ID)
	if order == nil {
		return c.Send("No pending order to cancel.")
	}
	b.sessions.Delete(order.UserID)
	if c.Message() != nil {
		_, _ = b.bot.Edit(c.Message(), "❌ Order cancelled. Nothing saved.")
	}
	return nil
}

func (b *Bot) handleDoneOrder(c tele.Context) error {
	_ = c.Respond()
	order := b.sessions.Get(c.Sender().ID)
	if order == nil {
		return c.Send("No pending order. Send a receipt photo to begin.")
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

// makePaymentHandler returns a handler bound to a specific payment
// method label. Using a factory avoids three near-identical methods.
func (b *Bot) makePaymentHandler(method string) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		order := b.sessions.Get(c.Sender().ID)
		if order == nil {
			return c.Send("Your session expired. Start with a fresh receipt.")
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
			return err
		}
		return c.Send(summary, tele.ModeMarkdown)
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
