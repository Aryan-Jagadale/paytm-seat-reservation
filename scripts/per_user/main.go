package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
)

type tokenRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type result struct {
	statusCode int
	reason     string
	err        error
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/per_user <BASE_URL>")
		fmt.Println("example: go run ./scripts/per_user http://127.0.0.1:8081")
		os.Exit(1)
	}

	baseURL := trimTrailingSlash(os.Args[1])

	const (
		userID      = "per-user-test"
		concurrency = 10
		perUserLimit = 4
	)

	seats := []string{
		"P1",
		"P2",
		"P3",
		"P4",
		"P5",
		"P6",
		"P7",
		"P8",
		"P9",
		"P10",
	}

	client := newHTTPClient()

	fmt.Println("========================================")
	fmt.Println("       PER-USER LIMIT TEST")
	fmt.Println("========================================")
	fmt.Printf("Base URL    : %s\n", baseURL)
	fmt.Printf("User        : %s\n", userID)
	fmt.Printf("Limit       : %d\n", perUserLimit)
	fmt.Printf("Requests    : %d\n", concurrency)
	fmt.Println()

	// --------------------------------------------------
	// 1. Generate admin token
	// --------------------------------------------------

	fmt.Println("[1/4] Generating admin token...")

	adminToken, err := generateToken(
		client,
		baseURL,
		"per-user-admin",
		"admin",
	)
	if err != nil {
		fatal("admin token failed", err)
	}

	// --------------------------------------------------
	// 2. Create a fresh show
	// --------------------------------------------------

	fmt.Println("[2/4] Creating fresh show...")

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
		seats,
		perUserLimit,
	)
	if err != nil {
		fatal("create show failed", err)
	}

	fmt.Printf("      Show ID: %s\n", showID)

	// --------------------------------------------------
	// 3. Generate user token
	// --------------------------------------------------

	fmt.Println("[3/4] Generating user token...")

	userToken, err := generateToken(
		client,
		baseURL,
		userID,
		"user",
	)
	if err != nil {
		fatal("user token failed", err)
	}

	// --------------------------------------------------
	// 4. Fire concurrent reservations
	// --------------------------------------------------

	fmt.Println("[4/4] Firing concurrent reservations...")

	results := make(chan result, concurrency)

	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func(requestID int) {
			defer wg.Done()

			status, reason, err := reserve(
				client,
				baseURL,
				showID,
				userToken,
				seats[requestID],
				fmt.Sprintf("per-user-%d", requestID),
			)

			results <- result{
				statusCode: status,
				reason:     reason,
				err:        err,
			}
		}(i)
	}

	wg.Wait()
	close(results)

	// --------------------------------------------------
	// Collect results
	// --------------------------------------------------

	confirmed := 0
	perUserLimitConflicts := 0
	otherConflicts := 0
	serverErrors := 0
	networkErrors := 0

	for r := range results {
		if r.err != nil {
			networkErrors++
			fmt.Println("NETWORK ERROR:", r.err)
			continue
		}

		switch r.statusCode {
		case http.StatusCreated:
			confirmed++

		case http.StatusConflict:
			if r.reason == "per_user_limit" {
				perUserLimitConflicts++
			} else {
				otherConflicts++

				fmt.Printf(
					"409 -> %s\n",
					r.reason,
				)
			}

		case http.StatusInternalServerError:
			serverErrors++

			fmt.Printf(
				"500 -> %s\n",
				r.reason,
			)

		default:
			serverErrors++

			fmt.Printf(
				"HTTP %d -> %s\n",
				r.statusCode,
				r.reason,
			)
		}
	}

	// --------------------------------------------------
	// Summary
	// --------------------------------------------------

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("              SUMMARY")
	fmt.Println("========================================")

	fmt.Printf("201 Created        : %d\n", confirmed)
	fmt.Printf("409 per_user_limit : %d\n", perUserLimitConflicts)
	fmt.Printf("Other conflicts    : %d\n", otherConflicts)
	fmt.Printf("5xx                : %d\n", serverErrors)
	fmt.Printf("Network errors     : %d\n", networkErrors)

	fmt.Println()

	// --------------------------------------------------
	// Assertions
	// --------------------------------------------------

	if confirmed > perUserLimit {
		fmt.Println("FAIL: per-user limit was exceeded")
		os.Exit(1)
	}

	if confirmed != perUserLimit {
		fmt.Printf(
			"FAIL: expected exactly %d successful reservations, got %d\n",
			perUserLimit,
			confirmed,
		)
		os.Exit(1)
	}

	expectedConflicts := concurrency - perUserLimit

	if perUserLimitConflicts != expectedConflicts {
		fmt.Printf(
			"FAIL: expected %d per-user-limit conflicts, got %d\n",
			expectedConflicts,
			perUserLimitConflicts,
		)
		os.Exit(1)
	}

	if otherConflicts != 0 {
		fmt.Println("FAIL: unexpected conflict reason detected")
		os.Exit(1)
	}

	if serverErrors != 0 {
		fmt.Println("FAIL: 5xx responses detected")
		os.Exit(1)
	}

	if networkErrors != 0 {
		fmt.Println("FAIL: network errors detected")
		os.Exit(1)
	}

	fmt.Println("PASS: per-user limit held under concurrency")

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("       PER-USER TEST PASSED")
	fmt.Println("========================================")
}

func newHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: nil,

		DialContext: (&net.Dialer{}).DialContext,

		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 20,
		MaxConnsPerHost:     20,

		DisableKeepAlives: false,
	}

	return &http.Client{
		Transport: transport,
	}
}

func generateToken(
	client *http.Client,
	baseURL string,
	userID string,
	role string,
) (string, error) {

	payload, err := json.Marshal(tokenRequest{
		UserID: userID,
		Role:   role,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/dev/token",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"token request failed: HTTP %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response tokenResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf(
			"decode token response: %w",
			err,
		)
	}

	if response.Token == "" {
		return "", fmt.Errorf(
			"token endpoint returned empty token",
		)
	}

	return response.Token, nil
}

func createShow(
	client *http.Client,
	baseURL string,
	adminToken string,
	seats []string,
	perUserLimit int,
) (string, error) {

	payload, err := json.Marshal(map[string]any{
		"name":           "Per User Concurrency Test",
		"seats":          seats,
		"price_paise":    10000,
		"per_user_limit": perUserLimit,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/shows",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	req.Header.Set(
		"Authorization",
		"Bearer "+adminToken,
	)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf(
			"create show failed: HTTP %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var response struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf(
			"decode show response: %w",
			err,
		)
	}

	if response.ID == "" {
		return "", fmt.Errorf(
			"create show returned empty ID",
		)
	}

	return response.ID, nil
}

func reserve(
	client *http.Client,
	baseURL string,
	showID string,
	token string,
	seat string,
	idempotencyKey string,
) (int, string, error) {

	payload, err := json.Marshal(reserveRequest{
		Seats:          []string{seat},
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return 0, "", err
	}

	url := fmt.Sprintf(
		"%s/shows/%s/reserve",
		baseURL,
		showID,
	)

	req, err := http.NewRequest(
		http.MethodPost,
		url,
		bytes.NewReader(payload),
	)
	if err != nil {
		return 0, "", err
	}

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	req.Header.Set(
		"Authorization",
		"Bearer "+token,
	)

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}

	var response struct {
		Error string `json:"error"`
	}

	_ = json.Unmarshal(body, &response)

	return resp.StatusCode, response.Error, nil
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}

	return value
}

func fatal(message string, err error) {
	fmt.Printf(
		"ERROR: %s: %v\n",
		message,
		err,
	)

	os.Exit(1)
}