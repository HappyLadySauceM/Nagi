package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/pkg/config"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/google/uuid"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("nagi-eval", flag.ContinueOnError)
	configFile := flags.String("config", "", "Nagi config file")
	dataset := flags.String("dataset", "", "Langfuse dataset name")
	botID := flags.Int64("bot", 0, "source bot account")
	conversationID := flags.String("conversation", "", "source conversation UUID")
	first := flags.Int64("first", 0, "inclusive lower message ID")
	last := flags.Int64("last", 0, "inclusive upper message ID")
	sourceURL := flags.String("nagi-url", "", "browser origin for administrator-only inbound evidence")
	release := flags.String("baseline-release", "", "code revision used for the observed baseline")
	split := flags.String("split", "tuning", "tuning or holdout; assign the complete conversation together")
	upload := flags.Bool("upload", false, "create missing Langfuse items")
	if err := flags.Parse(args); err != nil {
		return err
	}
	conversation, uuidErr := uuid.Parse(*conversationID)
	if uuidErr != nil || *dataset == "" || *botID <= 0 || *first <= 0 || *last < *first || *last == math.MaxInt64 ||
		(*split != "tuning" && *split != "holdout") {
		return fmt.Errorf("dataset, bot, conversation, first, last and valid split are required")
	}
	if *sourceURL != "" {
		u, err := url.Parse(*sourceURL)
		if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("invalid Nagi origin")
		}
		*sourceURL = strings.TrimRight(*sourceURL, "/")
	}
	cfg, err := config.Load(config.Options{File: *configFile})
	if err != nil {
		return fmt.Errorf("load Nagi configuration")
	}
	dsn := os.Getenv("NAGI_EVAL_READ_DSN")
	if dsn == "" {
		dsn = cfg.Database.DSN
	}
	if dsn == "" {
		return fmt.Errorf("read DSN is required")
	}
	aliasKey := os.Getenv("NAGI_EVAL_HMAC_KEY")
	if aliasKey == "" {
		aliasKey = cfg.Observability.UserIDSecret
	}
	if len(aliasKey) < 32 {
		return fmt.Errorf("stable HMAC key of at least 32 bytes required")
	}
	limited, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	pool, err := db.Connect(limited, dsn, 2)
	if err != nil {
		return fmt.Errorf("connect to evaluation source database")
	}
	defer pool.Close()
	items, err := collectDataset(limited, pool, selection{
		dataset: *dataset, split: *split, bot: *botID, conversation: conversation.String(),
		first: *first, last: *last, secret: []byte(aliasKey), origin: *sourceURL, release: *release,
	})
	if err != nil {
		return err
	}
	var partial int
	for _, item := range items {
		if len(item.Metadata.Missing) != 0 {
			partial++
		}
	}
	fmt.Printf("dataset=%s split=%s messages=%d partial=%d\n", *dataset, *split, len(items), partial)
	if !*upload {
		return nil
	}
	client, err := newLangfuseClient(cfg.Observability.BaseURL, cfg.Observability.PublicKey, cfg.Observability.SecretKey)
	if err != nil {
		return err
	}
	return client.publish(limited, *dataset, items)
}
