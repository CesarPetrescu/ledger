package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/cesarpetrescu/ledger/internal/admin"
	"github.com/cesarpetrescu/ledger/internal/config"
	"github.com/cesarpetrescu/ledger/internal/oauth"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "hash-password":
		fmt.Fprint(os.Stderr, "Password: ")
		hash, err := oauth.HashPasswordFromReader(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(hash)
	case "serve":
		db := config.OpenDB(ctx)
		defer db.Close()
		handler := admin.NewServer(admin.Config{
			PublicURL:         config.Required("LEDGER_PUBLIC_URL"),
			PasswordHash:      config.Required("LEDGER_ADMIN_PASSWORD_HASH"),
			InternalProxyCIDR: config.Required("LEDGER_INTERNAL_PROXY_CIDR"),
			IndexURL:          config.Required("LEDGER_INDEX_URL"),
		}, db)
		if err := config.Serve(":8084", handler); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case "revoke-sessions":
		db := config.OpenDB(ctx)
		defer db.Close()
		count, err := db.RevokeAdminSessions(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(count)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ledger-admin serve|hash-password|revoke-sessions")
	os.Exit(2)
}
