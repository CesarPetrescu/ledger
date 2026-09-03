package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

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
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
		if err != nil || len(password) > 4096 {
			log.Fatal("could not read password")
		}
		plain := strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r")
		if plain == "" {
			log.Fatal("password must not be empty")
		}
		hash, err := oauth.HashPassword(plain)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(hash)
	case "serve":
		db := config.OpenDB(ctx)
		defer db.Close()
		handler := oauth.NewServer(oauth.Config{PublicURL: config.Required("LEDGER_PUBLIC_URL"), PasswordHash: config.Required("LEDGER_PASSWORD_HASH"), InternalProxyCIDR: config.Required("LEDGER_INTERNAL_PROXY_CIDR")}, db)
		if err := config.Serve(":8082", handler); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case "clients":
		db := config.OpenDB(ctx)
		defer db.Close()
		clients, err := db.ListClients(ctx)
		if err != nil {
			log.Fatal(err)
		}
		_ = json.NewEncoder(os.Stdout).Encode(clients)
	case "revoke":
		flags := flag.NewFlagSet("revoke", flag.ExitOnError)
		all := flags.Bool("all", false, "revoke every token")
		clientID := flags.String("client", "", "revoke tokens for one client")
		_ = flags.Parse(os.Args[2:])
		if *all == (*clientID != "") {
			log.Fatal("choose exactly one of --all or --client ID")
		}
		db := config.OpenDB(ctx)
		defer db.Close()
		count, err := db.Revoke(ctx, *clientID, *all)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(count)
	case "gc":
		db := config.OpenDB(ctx)
		defer db.Close()
		count, err := db.GC(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(count)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ledger-auth serve|hash-password|clients|revoke --all|--client ID|gc")
	os.Exit(2)
}
