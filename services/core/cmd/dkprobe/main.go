// Command dkprobe is the capability probe harness. It runs every §15.2 probe
// against a DK base URL with a supplied access token and prints the per-
// capability verdicts. With -record it installs a RecordingTransport that writes
// raw request/response snapshots to a directory — the frozen fixtures S35's
// GATED production run diffs live DK behavior against (§10.4).
//
// This harness NEVER exchanges tokens or fires writes on its own; it probes with
// a token the operator provides and against a base URL the operator chooses.
// Pointing it at live DK is a GATED operation (human "go") — by default it is
// pointed at the local mock DK server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mhosseinab/market-ops/services/core/internal/connector"
)

func main() {
	base := flag.String("base", "http://localhost:8090", "DK Seller API base URL (default: local mock)")
	token := flag.String("token", os.Getenv("DK_ACCESS_TOKEN"), "DK access token (or DK_ACCESS_TOKEN env)")
	record := flag.String("record", "", "directory to write raw request/response snapshots into (S35 capture)")
	variant := flag.Int("variant", 1, "sample product variant id for per-variant probes")
	flag.Parse()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	if *record != "" {
		rt, err := connector.NewRecordingTransport(*record, http.DefaultTransport)
		if err != nil {
			log.Fatalf("dkprobe: %v", err)
		}
		httpClient.Transport = rt
		log.Printf("dkprobe: recording snapshots to %s", *record)
	}

	dk, err := connector.NewDKClient(*base, httpClient)
	if err != nil {
		log.Fatalf("dkprobe: %v", err)
	}

	results := dk.Probe(context.Background(), *token, connector.ProbeOptions{SampleVariantID: *variant})
	out, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(out))
}
