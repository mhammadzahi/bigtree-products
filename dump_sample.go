//go:build ignore

// dump_sample.go — fetch a sample of products from WooCommerce and write the
// RAW JSON to sample_products.json, plus print a summary of every custom-field
// (ACF) key and attribute seen. Use this to inspect the real data shape before
// tailoring the schema.
//
//	go run dump_sample.go          # 30 products (default)
//	go run dump_sample.go 50       # 50 products
//
// Reads WC_STORE_URL / WC_CONSUMER_KEY / WC_CONSUMER_SECRET from .env.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"bigtree-products/internal/config"
)

func main() {
	config.Load() // load .env into the environment

	store := strings.TrimRight(os.Getenv("WC_STORE_URL"), "/")
	key := os.Getenv("WC_CONSUMER_KEY")
	secret := os.Getenv("WC_CONSUMER_SECRET")
	if store == "" || key == "" || secret == "" {
		log.Fatal("missing WC_STORE_URL / WC_CONSUMER_KEY / WC_CONSUMER_SECRET in .env")
	}

	n := 30
	if len(os.Args) > 1 {
		if v, err := strconv.Atoi(os.Args[1]); err == nil && v > 0 {
			n = v
		}
	}
	if n > 100 {
		n = 100 // one page max for a sample
	}

	url := fmt.Sprintf("%s/wp-json/wc/v3/products?per_page=%d&page=1&status=publish", store, n)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.SetBasicAuth(key, secret)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		log.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Fatalf("HTTP %d from WooCommerce", resp.StatusCode)
	}

	// Decode loosely so we can both save raw and summarise.
	var products []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&products); err != nil {
		log.Fatalf("decode: %v", err)
	}

	// Save pretty-printed raw JSON.
	out, _ := json.MarshalIndent(products, "", "  ")
	if err := os.WriteFile("sample_products.json", out, 0o644); err != nil {
		log.Fatalf("write file: %v", err)
	}

	// Summarise field/attribute names to reveal the ACF + taxonomy shape.
	metaKeys := map[string]int{}
	attrs := map[string]int{}
	topKeys := map[string]int{}
	for _, p := range products {
		for k := range p {
			topKeys[k]++
		}
		if md, ok := p["meta_data"].([]any); ok {
			for _, m := range md {
				if mm, ok := m.(map[string]any); ok {
					if key, ok := mm["key"].(string); ok {
						metaKeys[key]++
					}
				}
			}
		}
		if as, ok := p["attributes"].([]any); ok {
			for _, a := range as {
				if am, ok := a.(map[string]any); ok {
					name, _ := am["name"].(string)
					slug, _ := am["slug"].(string)
					attrs[fmt.Sprintf("%s (%s)", name, slug)]++
				}
			}
		}
	}

	fmt.Printf("\nSaved %d products -> sample_products.json\n", len(products))
	fmt.Println("\n=== Top-level product fields ===")
	printSorted(topKeys)
	fmt.Println("\n=== Attributes (name (slug)) ===")
	printSorted(attrs)
	fmt.Println("\n=== Custom-field / ACF meta_data keys ===")
	printSorted(metaKeys)
	fmt.Println("\nShare sample_products.json (or the lists above) to tailor the schema.")
}

func printSorted(m map[string]int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-40s x%d\n", k, m[k])
	}
}
