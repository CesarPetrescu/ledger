package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	calendarapi "github.com/cesarpetrescu/ledger/internal/calendar"
	"github.com/cesarpetrescu/ledger/internal/config"
	"github.com/cesarpetrescu/ledger/internal/mcpserver"
	"github.com/cesarpetrescu/ledger/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	db := config.OpenDB(ctx)
	defer db.Close()
	switch os.Args[1] {
	case "serve":
		publicURL := config.Required("LEDGER_PUBLIC_URL")
		calendar, err := calendarapi.NewService(db, config.Required("LEDGER_DATABASE_URL"), nil)
		if err != nil {
			log.Fatal(err)
		}
		server := mcpserver.NewServer(db, config.Required("LEDGER_INDEX_URL"), calendar)
		if err := config.Serve(":8081", mcpserver.HTTPHandler(server, db, publicURL)); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case "seed":
		input := os.Stdin
		if len(os.Args) == 3 {
			file, err := os.Open(os.Args[2])
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()
			input = file
		}
		var projects []store.Project
		if err := json.NewDecoder(input).Decode(&projects); err != nil {
			log.Fatal(err)
		}
		if err := mcpserver.Seed(ctx, db, projects); err != nil {
			log.Fatal(err)
		}
		fmt.Println(len(projects))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ledger-mcp serve|seed [projects.json]")
	os.Exit(2)
}
