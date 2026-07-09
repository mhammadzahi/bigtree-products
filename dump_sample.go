//go:build ignore

// dump_sample.go — fetch a sample of products from WooCommerce and write the
// RAW JSON to sample_products.json, plus print a summary of every custom-field
// (ACF) key and attribute seen. Use this to inspect the real data shape before
// tailoring the schema.
//
//	go run dump_sample.go              # 30 products (default)
//	go run dump_sample.go 50           # 50 products
//	go run dump_sample.go product 1234 # a single product by WooCommerce ID
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

	// Determine the mode from the args.
	//   (none)          -> list, 30 products
	//   <number>        -> list, that many products
	//   product <id>    -> single product by WooCommerce ID
	single := false
	productID := ""
	n := 30
	args := os.Args[1:]
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "product", "single", "single_product", "id":
			if len(args) < 2 {
				log.Fatal("usage: go run dump_sample.go product <id>")
			}
			if _, err := strconv.Atoi(args[1]); err != nil {
				log.Fatalf("invalid product id %q — must be numeric", args[1])
			}
			single, productID = true, args[1]
		default:
			if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
				n = v
			}
		}
	}

	// Build the request URL + output filename per mode.
	var url, outfile string
	if single {
		url = fmt.Sprintf("%s/wp-json/wc/v3/products/%s", store, productID)
		outfile = fmt.Sprintf("single_product_%s.json", productID)
	} else {
		if n > 100 {
			n = 100 // one page max for a sample
		}
		url = fmt.Sprintf("%s/wp-json/wc/v3/products?per_page=%d&page=1&status=publish", store, n)
		outfile = "sample_products.json"
	}

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

	// Decode loosely so we can both save raw and summarise. The single-product
	// endpoint returns one object; wrap it in a slice for uniform handling.
	var products []map[string]any
	var raw any
	if single {
		var one map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&one); err != nil {
			log.Fatalf("decode: %v", err)
		}
		products, raw = []map[string]any{one}, one
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&products); err != nil {
			log.Fatalf("decode: %v", err)
		}
		raw = products
	}

	// Save pretty-printed raw JSON.
	out, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(outfile, out, 0o644); err != nil {
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

	fmt.Printf("\nSaved %d product(s) -> %s\n", len(products), outfile)
	fmt.Println("\n=== Top-level product fields ===")
	printSorted(topKeys)
	fmt.Println("\n=== Attributes (name (slug)) ===")
	printSorted(attrs)
	fmt.Println("\n=== Custom-field / ACF meta_data keys ===")
	printSorted(metaKeys)
	fmt.Printf("\nInspect %s (or the lists above) to see the full shape.\n", outfile)
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
