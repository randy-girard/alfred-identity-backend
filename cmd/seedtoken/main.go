package main

// seedtoken creates a user + API token when Discord is disabled (local/CI bootstrap).
// Usage: go run ./cmd/seedtoken <discord_id> <display_name>

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/alfred-identity/web/internal/config"
	"github.com/alfred-identity/web/internal/crypto"
	"github.com/alfred-identity/web/internal/db"
	"github.com/alfred-identity/web/internal/store"
)

func main() {
	_ = godotenv.Load()
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: seedtoken <discord_id> <display_name>\n")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(sqlDB); err != nil {
		fatal(err)
	}
	aead, err := crypto.NewAEAD(cfg.DataEncryptionKey)
	if err != nil {
		fatal(err)
	}
	st := &store.Store{DB: sqlDB, AEAD: aead, Key: cfg.DataEncryptionKey}
	u, err := st.UpsertUser(ctx, os.Args[1], os.Args[2], nil)
	if err != nil {
		fatal(err)
	}
	raw, id, err := st.CreateToken(ctx, u.ID)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("user_id=%d token_id=%d\ntoken=%s\n", u.ID, id, raw)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
