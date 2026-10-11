package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// replay re-sends recorded webhook bodies (one JSON document per line) to a
// webhook URL, e.g. https://api-dev.example.com/webhooks/bitgo/<token>.
// Used to test idempotency and to recover from receiver outages.
func replay(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	file := fs.String("file", "", "JSONL file of recorded webhook bodies")
	url := fs.String("url", "", "webhook URL to post to")
	times := fs.Int("times", 1, "send every body this many times")
	_ = fs.Parse(args)
	if *file == "" || *url == "" {
		return fmt.Errorf("--file and --url are required")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	sent := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for i := 0; i < *times; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, *url, bytes.NewReader([]byte(line)))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				return fmt.Errorf("receiver answered %d", resp.StatusCode)
			}
			sent++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	fmt.Printf("replayed %d deliveries\n", sent)
	return nil
}
