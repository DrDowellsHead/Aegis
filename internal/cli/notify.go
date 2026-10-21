package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/notify"
	"github.com/DrDowellsHead/Aegis/internal/secrets"
)

// runNotify is a separate supervised process; incident writes never depend
// on SMTP/Telegram/webhook availability. Secrets are environment-only.
func runNotify(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("notify", flag.ContinueOnError)
	flags.SetOutput(errOut)
	email := flags.String("email-to", "", "one notification recipient")
	from := flags.String("smtp-from", "", "SMTP envelope sender")
	smtpAddress := flags.String("smtp-address", "", "implicit TLS host:port (usually 465)")
	chat := flags.String("telegram-chat-id", "", "Telegram user/group ID")
	webhook := flags.String("webhook-url", "", "public HTTPS webhook without embedded secrets")
	poll := flags.Duration("poll", 3*time.Second, "poll interval (1s to 1m)")
	once := flags.Bool("once", false, "drain currently ready jobs then exit")
	deliveryRate := flags.Int("max-deliveries-per-minute", 60, "maximum delivery attempts per minute per worker (1..600)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *poll < time.Second || *poll > time.Minute || *deliveryRate < 1 || *deliveryRate > 600 {
		fmt.Fprintln(errOut, "invalid notify arguments, poll interval or delivery rate (1..600 per minute)")
		return 2
	}
	pacer, err := notify.NewPacer(*deliveryRate)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	smtpPassword, err := secrets.Resolve("AEGIS_SMTP_PASSWORD")
	if err != nil {
		fmt.Fprintln(errOut, "invalid SMTP credential source:", err)
		return 2
	}
	telegramToken, err := secrets.Resolve("AEGIS_TELEGRAM_TOKEN")
	if err != nil {
		fmt.Fprintln(errOut, "invalid Telegram credential source:", err)
		return 2
	}
	webhookSecret, err := secrets.Resolve("AEGIS_WEBHOOK_SECRET")
	if err != nil {
		fmt.Fprintln(errOut, "invalid webhook credential source:", err)
		return 2
	}
	cfg := notify.Config{
		EmailTo: *email, SMTPFrom: *from, SMTPAddress: *smtpAddress,
		SMTPUser: os.Getenv("AEGIS_SMTP_USER"), SMTPPassword: smtpPassword,
		TelegramChatID: *chat, TelegramToken: telegramToken,
		WebhookURL: *webhook, WebhookSecret: webhookSecret,
	}
	sender, routes, err := notify.New(cfg)
	if err != nil {
		fmt.Fprintln(errOut, "notification configuration:", err)
		return 2
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	db, err := openConfiguredDatabase(checkCtx)
	if err != nil {
		cancel()
		fmt.Fprintln(errOut, "notification database unavailable")
		return 1
	}
	defer db.Close()
	if err = db.Ready(checkCtx); err != nil {
		cancel()
		fmt.Fprintln(errOut, "notification schema unavailable (run migrate)")
		return 1
	}
	if err = db.ConfigureNotificationRoutes(checkCtx, routes); err != nil {
		cancel()
		fmt.Fprintln(errOut, "cannot configure notification routes")
		return 1
	}
	cancel()
	channels := make([]string, 0, len(routes))
	for _, r := range routes {
		channels = append(channels, r.Channel)
	}
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	logger.Info("notification worker running", "channels", channels, "poll", poll.String(), "once", *once,
		"max_deliveries_per_minute", *deliveryRate)
	for {
		if ctx.Err() != nil {
			return 0
		}
		// Wait BEFORE taking a lease: slow providers must not cause lease expiry.
		if err := pacer.Wait(ctx); err != nil {
			return 0
		}
		callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		job, err := db.ClaimNotification(callCtx, channels)
		cancel()
		if err != nil {
			logger.Error("notification claim failed", "error_type", "database")
			if *once {
				return 1
			}
		} else if job != nil {
			pacer.NoteAttempt()
			sendCtx, stop := context.WithTimeout(ctx, 12*time.Second)
			outcome := sender.Deliver(sendCtx, *job)
			stop()
			finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
			// A bounded commit still runs on shutdown; if it fails, the
			// lease expires and another worker can retry at least once.
			err = db.FinishNotification(finishCtx, *job, outcome.Code == "", outcome.Permanent, outcome.Code)
			finishCancel()
			if err != nil {
				logger.Error("notification result could not be committed",
					"job_id", job.ID, "channel", job.Channel)
				if *once {
					return 1
				}
			} else {
				logger.Info("notification attempt recorded", "job_id", job.ID,
					"event_id", job.EventID, "channel", job.Channel,
					"attempt", job.Attempt, "outcome", outcome.Code)
			}
			continue // drain ready jobs immediately, one at a time.
		}
		if *once {
			return 0
		}
		tick := time.NewTimer(*poll)
		select {
		case <-ctx.Done():
			if !tick.Stop() {
				select {
				case <-tick.C:
				default:
				}
			}
			return 0
		case <-tick.C:
		}
	}
}
