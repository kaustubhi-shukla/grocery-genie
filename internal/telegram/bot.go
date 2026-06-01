// Package telegram is the thin wrapper around the Telegram Bot API.
//
// All Telegram-specific code lives here. The rest of the app talks to
// Bot via methods like Start() and (later) handlers we will plug in.
// If we ever swap Telegram for WhatsApp, only this package changes.
//
// Current state (Phase 1, step 6): bot connects to Telegram, responds
// to /start, echoes any text message, and acknowledges photos. The
// actual receipt scanning and meal parsing arrive in steps 7+.
package telegram

import (
	"fmt"
	"log"
	"time"

	tele "gopkg.in/telebot.v3"
)

// Bot is our wrapper around the underlying telebot.Bot. Holding it in
// a struct lets us add fields later (e.g., database handle, AI client)
// without changing the public API.
type Bot struct {
	bot *tele.Bot
}

// New creates and configures a Bot with the given token. It registers
// the message handlers but does not start listening yet. Call Start()
// to begin processing messages.
//
// The token is the long string BotFather gave you when you ran /newbot.
func New(token string) (*Bot, error) {
	// LongPoller: the bot will repeatedly ask Telegram "any new
	// messages?" with a 10-second timeout. If a message arrives,
	// Telegram returns it immediately. If not, the connection holds
	// open for 10s before returning empty — this is called "long
	// polling" and is efficient (no constant rapid-fire requests).
	settings := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	telebot, err := tele.NewBot(settings)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	b := &Bot{bot: telebot}
	b.registerHandlers()
	return b, nil
}

// registerHandlers wires each kind of incoming message to its handler.
// Telebot's "/start" matches the literal /start command. tele.OnText
// matches any text message that is NOT a command. tele.OnPhoto matches
// any photo message.
//
// The bot.Use(...) line adds a "middleware" that runs before every
// handler — we use it to log every incoming message. Middleware is a
// common pattern: a function that wraps every handler with extra
// behavior (logging, auth, rate limiting, etc.).
func (b *Bot) registerHandlers() {
	b.bot.Use(b.logIncoming)
	b.bot.Handle("/start", b.handleStart)
	b.bot.Handle("/stock", b.handleStockComingSoon)
	b.bot.Handle("/help", b.handleHelp)
	b.bot.Handle(tele.OnText, b.handleText)
	b.bot.Handle(tele.OnPhoto, b.handlePhoto)
}

// logIncoming logs every incoming message so we can see activity in
// the terminal. Returning next(c) hands off to the real handler.
func (b *Bot) logIncoming(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		sender := c.Sender()
		msg := c.Message()
		switch {
		case msg.Photo != nil:
			log.Printf("📷 from @%s (%s): [photo]", sender.Username, sender.FirstName)
		case msg.Text != "":
			log.Printf("💬 from @%s (%s): %q", sender.Username, sender.FirstName, msg.Text)
		default:
			log.Printf("📥 from @%s (%s): [other message type]", sender.Username, sender.FirstName)
		}
		return next(c)
	}
}

// handleStart greets the user. This is what Telegram users see the
// very first time they open the bot, when they tap the "Start" button.
func (b *Bot) handleStart(c tele.Context) error {
	welcome := `Hi! I'm GroceryGenie 🛒

I help you manage your household groceries.

Right now I'm in early setup mode. I can already:
• Echo your text messages (just so you know I'm alive)
• Acknowledge photos (receipt scanning lands in the next step)

Try /help to see what's coming.`
	return c.Send(welcome)
}

// handleHelp shows what the bot can (and will) do.
func (b *Bot) handleHelp(c tele.Context) error {
	help := `What I can do (Phase 1 in progress):

✅ Reply when you talk to me
🔜 Scan receipt photos and log purchases
🔜 Ask for payment method, platform if you skip
🔜 Send an 8 PM nudge if you forget to log

Coming in later phases:
• Track meals you cook (Phase 2)
• Saturday weekly shopping list (Phase 3)
• Waste tracking + platform quality scores (Phase 4)
• Monthly budget dashboard (Phase 5)
• Guest mode + Kannada support for your cook (Phase 6)`
	return c.Send(help)
}

// handleStockComingSoon is a stub for /stock until we build inventory
// in step 5+. It exists so the command shows up in Telegram's UI.
func (b *Bot) handleStockComingSoon(c tele.Context) error {
	return c.Send("Stock tracking arrives in Phase 2. Hang tight!")
}

// handleText is invoked for any text message that is not a command.
// For now it just echoes — proving the message round-trip works.
// Step 7+ replaces this with Gemini-powered intent parsing.
func (b *Bot) handleText(c tele.Context) error {
	reply := fmt.Sprintf(
		"You said: %q\n\n(I'm just echoing right now. Meal/usage parsing lands in Phase 2.)",
		c.Text(),
	)
	return c.Send(reply)
}

// handlePhoto is invoked when the user sends a photo. Step 7 will
// route this to Gemini Vision for receipt extraction. For now we just
// confirm the bot received it.
func (b *Bot) handlePhoto(c tele.Context) error {
	return c.Send("Got your photo! 📸\n\nReceipt scanning is the very next thing I'm learning — coming in step 7.")
}

// Start begins polling Telegram for messages. This call blocks
// forever (until Stop is called or the process is killed). Run this
// from main() as the last thing.
func (b *Bot) Start() {
	log.Println("Telegram bot listening for messages...")
	b.bot.Start() // blocks
}

// Stop gracefully shuts down the polling loop. Useful for signal
// handlers (Ctrl+C) — we will hook this up in a later step.
func (b *Bot) Stop() {
	b.bot.Stop()
}
