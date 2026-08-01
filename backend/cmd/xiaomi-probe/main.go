package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/feranydev/homeloom/backend/internal/persistence/gormstore"
	"github.com/feranydev/homeloom/backend/internal/providers/xiaomi"
)

func main() {
	ctx := context.Background()
	store, err := gormstore.Open(ctx, os.Getenv("HOMELOOM_DATABASE_URL"), os.Getenv("HOMELOOM_MASTER_KEY"))
	if err != nil {
		panic(err)
	}
	defer store.Close()
	items, err := store.ListProviders(ctx)
	if err != nil {
		panic(err)
	}
	for _, item := range items {
		if item.ID != "xiaomi-2231ed" {
			continue
		}
		p, err := xiaomi.NewProviderFromConfig(item)
		if err != nil {
			fmt.Println("NEW:", err)
			os.Exit(2)
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		start := time.Now()
		if err := p.Initialize(cctx); err != nil {
			fmt.Println("INIT_ERROR:", err)
			os.Exit(1)
		}
		fmt.Println("INIT_OK elapsed", time.Since(start))
		_ = p.Close(context.Background())
		return
	}
	panic("not found")
}
