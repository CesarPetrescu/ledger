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
	fmt.Fprintln(os.Stderr, "usage: ledger-index serve|reindex")
	os.Exit(2)
}
