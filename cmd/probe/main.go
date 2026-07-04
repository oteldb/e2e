// Command probe is a manual investigation helper (not part of the suite): it ingests or queries a
// single tagged log so a human can interleave a kubectl disk-wipe between the two and observe
// whether oteldb's RF replication makes the data survive. Usage:
//
//	go run ./cmd/probe ingest <grpcAddr> <id>
//	go run ./cmd/probe query  <lokiAddr> <id>
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/oteldb/e2e/internal/otlp"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: probe ingest|query <addr> <id>")
		os.Exit(2)
	}
	mode, addr, id := os.Args[1], os.Args[2], os.Args[3]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch mode {
	case "ingest":
		ing := otlp.NewIngester(addr)
		if err := ing.EmitLog(ctx, "oteldb-e2e-logs", id); err != nil {
			fmt.Fprintln(os.Stderr, "ingest:", err)
			os.Exit(1)
		}
		fmt.Println("ingested", id)
	case "query":
		c := otlp.NewClients("", addr, "")
		ok, err := c.LogsContain(ctx, `{service_name="oteldb-e2e-logs"}`, id)
		if err != nil {
			fmt.Fprintln(os.Stderr, "query:", err)
			os.Exit(1)
		}
		fmt.Printf("found=%v id=%s\n", ok, id)
	default:
		fmt.Fprintln(os.Stderr, "unknown mode", mode)
		os.Exit(2)
	}
}
