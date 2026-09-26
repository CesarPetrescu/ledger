package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/cesarpetrescu/ledger/internal/config"
	"github.com/cesarpetrescu/ledger/internal/retrieval"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	db := config.OpenDB(ctx)
	defer db.Close()
	// reextract only clears database rows, so it must not need inference settings.
	if os.Args[1] == "reextract" {
		count, err := db.ClearModelMeta(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(count)
		return
	}
	infer := retrieval.NewInferClient(config.Required("LEDGER_INFER_URL"), config.Value("LEDGER_EMBED_MODEL", "qwen3-embedding"), config.Value("LEDGER_RERANK_MODEL", "qwen3-reranker"), config.Int("LEDGER_EMBED_DIM", 4096), os.Getenv("LEDGER_INFER_API_KEY"))
	worker := retrieval.NewIndexer(db, infer)
	switch os.Args[1] {
	case "serve":
		workerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			if err := worker.Run(workerCtx); err != nil && workerCtx.Err() == nil {
				log.Printf("index worker stopped: %v", err)
			}
		}()
		// Table settings are read only here, so reindex and reextract never
		// depend on them.
		if chatURL := os.Getenv("LEDGER_CHAT_URL"); chatURL != "" {
			threshold := config.Float("LEDGER_DUPLICATE_SIMILARITY", 0.9)
			if !retrieval.ValidDuplicateThreshold(threshold) {
				log.Fatalf("LEDGER_DUPLICATE_SIMILARITY must be greater than 0 and at most 1, got %v", threshold)
			}
			extractor := retrieval.NewExtractor(db, chatURL, os.Getenv("LEDGER_CHAT_MODEL"), os.Getenv("LEDGER_CHAT_API_KEY"), infer, threshold)
			go func() {
				if err := extractor.Run(workerCtx); err != nil && workerCtx.Err() == nil {
					log.Printf("metadata extractor stopped: %v", err)
				}
			}()
		} else {
			log.Print("LEDGER_CHAT_URL is not set; entry metadata extraction is disabled")
		}
		handler := retrieval.NewHTTPHandler(retrieval.NewSearcher(db, infer), worker)
		if err := config.Serve(":8083", handler); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case "reindex":
		count, err := worker.QueueAll(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(count)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ledger-index serve|reindex|reextract")
	os.Exit(2)
}
