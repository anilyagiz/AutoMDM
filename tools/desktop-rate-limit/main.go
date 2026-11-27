package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/anilyagiz/automdm/v4/pkg/automdmhttp"
	"github.com/google/uuid"
)

func main() {
	automdmURL := flag.String("automdm_url", "", "URL (with protocol and port of AutoMDM server)")
	automdmDesktopToken := flag.String("automdm_desktop_token", "", "Valid \"AutoMDM Desktop\" token")

	flag.Parse()

	if *automdmURL == "" {
		log.Fatal("missing automdm_url argument")
	}

	c := automdmhttp.NewClient(automdmhttp.WithTLSClientConfig(&tls.Config{
		// Ignoring "G402: TLS InsecureSkipVerify set true", this is only used for automated testing.
		InsecureSkipVerify: true, //nolint:gosec
	}))

	endpoints := [][2]string{
		{"GET", "/api/latest/automdm/device/%s/desktop"},
		{"GET", "/api/latest/automdm/device/%s/transparency"},
		{"POST", "/api/latest/automdm/device/%s/refetch"},
	}

	start := time.Now()
	for i := 1; ; i++ {
		token := uuid.NewString()
		endpoint := endpoints[0]
		if *automdmDesktopToken != "" && (i%500 == 0) {
			log.Print("Attempting good token")
			token = *automdmDesktopToken
		} else {
			endpoint = endpoints[i%len(endpoints)]
		}
		req, err := http.NewRequest(endpoint[0], *automdmURL+fmt.Sprintf(endpoint[1], token), nil)
		if err != nil {
			panic(err)
		}
		req.Header.Add("X-Forwarded-For", "127.0.0.1")
		res, err := c.Do(req)
		if err != nil {
			panic(err)
		}
		_ = res.Body.Close()
		log.Printf("%d: %s %s: %d\n", i, req.Method, req.URL.Path, res.StatusCode)
		if res.StatusCode == http.StatusTooManyRequests {
			log.Printf("Rate limited: %s\n", time.Since(start))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}
