package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultConcurrency = 20000
	defaultWorkers     = 200

	testSeat      = "BURST-1"
	testPricePaise = int64(10000)

	// We use 200 distinct authenticated users.
	// Each user sends multiple requests, but all requests target the same seat.
	defaultUsers = 200
)

type tokenRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

type createShowRequest struct {
	Name         string   `json:"name"`
	Seats        []string `json:"seats"`
	PricePaise   int64    `json:"price_paise"`
	PerUserLimit int      `json:"per_user_limit"`
}

type createShowResponse struct {
	ID string `json:"id"`
}

type showResponse struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	PricePaise   int64        `json:"price_paise"`
	PerUserLimit int          `json:"per_user_limit"`
	Seats        []seatResult `json:"seats"`
	Counts       seatCounts   `json:"counts"`
}

type seatResult struct {
	ID         string `json:"id"`
	ShowID     string `json:"show_id"`
	SeatNumber string `json:"seat_number"`
	Status     string `json:"status"`
}

type seatCounts struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Held      int `json:"held"`
	Confirmed int `json:"confirmed"`
}

type reserveRequest struct {
	Seats          []string `json:"seats"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type burstResult struct {
	statusCode int
	reason     string
	err        error
}

type counters struct {
	confirmed atomic.Int64
	fiveXX    atomic.Int64
	network   atomic.Int64
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/burst <BASE_URL>")
		fmt.Println("example: go run ./scripts/burst http://127.0.0.1:8081")
		os.Exit(1)
	}

	baseURL := os.Args[1]
	baseURL = trimTrailingSlash(baseURL)

	concurrency := getPositiveEnv(
		"CONCURRENCY",
		defaultConcurrency,
	)

	workers := getPositiveEnv(
		"WORKERS",
		defaultWorkers,
	)

	userCount := getPositiveEnv(
		"BURST_USERS",
		defaultUsers,
	)

	if workers > concurrency {
		workers = concurrency
	}

	if userCount > concurrency {
		userCount = concurrency
	}

	fmt.Println("========================================")
	fmt.Println("       SEAT RESERVATION BURST TEST")
	fmt.Println("========================================")
	fmt.Printf("Base URL     : %s\n", baseURL)
	fmt.Printf("Requests     : %d\n", concurrency)
	fmt.Printf("Workers      : %d\n", workers)
	fmt.Printf("Users        : %d\n", userCount)
	fmt.Printf("Target seat  : %s\n", testSeat)
	fmt.Println()

	client := newHTTPClient(workers)

	// ------------------------------------------------------------
	// 1. Generate admin token automatically.
	// ------------------------------------------------------------

	fmt.Println("[1/5] Creating admin authentication token...")

	adminToken, err := generateToken(
		client,
		baseURL,
		"burst-admin",
		"admin",
	)
	if err != nil {
		fatal("failed to generate admin token", err)
	}

	// ------------------------------------------------------------
	// 2. Create a fresh show.
	// ------------------------------------------------------------

	fmt.Println("[2/5] Creating fresh test show...")

	showID, err := createShow(
		client,
		baseURL,
		adminToken,
	)
	if err != nil {
		fatal("failed to create test show", err)
	}

	fmt.Printf("      Show ID: %s\n", showID)

	// ------------------------------------------------------------
	// 3. Generate user tokens.
	// ------------------------------------------------------------

	fmt.Println("[3/5] Generating test-user tokens...")

	tokens, err := generateUserTokens(
		client,
		baseURL,
		userCount,
	)
	if err != nil {
		fatal("failed to generate user tokens", err)
	}

	fmt.Printf("      Generated %d user tokens\n", len(tokens))

	// ------------------------------------------------------------
	// 4. Fire the hot-seat storm.
	// ------------------------------------------------------------

	fmt.Println("[4/5] Starting hot-seat storm...")
	fmt.Printf(
		"      %d requests -> 1 seat\n",
		concurrency,
	)

	results := make(chan burstResult, concurrency)

	jobs := make(chan int)

	var workersWG sync.WaitGroup

	start := time.Now()

	for workerID := 0; workerID < workers; workerID++ {
		workersWG.Add(1)

		go func() {
			defer workersWG.Done()

			for requestID := range jobs {
				userIndex := requestID % len(tokens)

				userID := fmt.Sprintf(
					"burst-user-%d",
					userIndex+1,
				)

				token := tokens[userIndex]

				result := reserveSeat(
					client,
					baseURL,
					showID,
					userID,
					token,
					requestID,
				)

				results <- result
			}
		}()
	}

	go func() {
		for i := 0; i < concurrency; i++ {
			jobs <- i
		}

		close(jobs)

		workersWG.Wait()
		close(results)
	}()

	confirmed := int64(0)
	fiveXX := int64(0)
	networkErrors := int64(0)

	declinedByReason := make(map[string]int64)

	for result := range results {
		if result.err != nil {
			networkErrors++
			continue
		}

		switch {
		case result.statusCode == http.StatusCreated:
			confirmed++

		case result.statusCode >= 500:
			fiveXX++

		case result.statusCode == http.StatusConflict:
			reason := result.reason

			if reason == "" {
				reason = "unknown_conflict"
			}

			declinedByReason[reason]++

		default:
			reason := fmt.Sprintf(
				"http_%d",
				result.statusCode,
			)

			declinedByReason[reason]++
		}
	}

	duration := time.Since(start)

	// ------------------------------------------------------------
	// 5. Reconcile final show state.
	// ------------------------------------------------------------

	fmt.Println()
	fmt.Println("[5/5] Fetching final show state...")

	finalShow, err := getShow(
		client,
		baseURL,
		showID,
	)

	if err != nil {
		fatal("failed to fetch final show state", err)
	}

	printSummary(
		concurrency,
		workers,
		userCount,
		duration,
		confirmed,
		declinedByReason,
		fiveXX,
		networkErrors,
		finalShow,
	)

	if confirmed != 1 {
		fmt.Println()
		fmt.Println("FAIL: expected exactly 1 confirmed reservation")
		os.Exit(1)
	}

	if fiveXX != 0 {
		fmt.Println()
		fmt.Println("FAIL: 5xx responses detected")
		os.Exit(1)
	}

	if networkErrors != 0 {
		fmt.Println()
		fmt.Println("FAIL: network errors detected")
		os.Exit(1)
	}

	if finalShow.Counts.Total !=
		finalShow.Counts.Available+
			finalShow.Counts.Held+
			finalShow.Counts.Confirmed {

		fmt.Println()
		fmt.Println("FAIL: seat reconciliation invariant violated")
		os.Exit(1)
	}

	if finalShow.Counts.Confirmed != 1 {
		fmt.Println()
		fmt.Println(
			"FAIL: final confirmed count is not 1",
		)
		os.Exit(1)
	}

	if finalShow.Counts.Available !=
		finalShow.Counts.Total-1 {

		fmt.Println()
		fmt.Println(
			"FAIL: final available count is incorrect",
		)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("             BURST TEST PASSED")
	fmt.Println("========================================")
}

func generateUserTokens(
	client *http.Client,
	baseURL string,
	userCount int,
) ([]string, error) {

	tokens := make([]string, userCount)

	// Generate these concurrently, but keep the amount modest.
	// This is authentication setup, not the actual stress test.
	const tokenWorkers = 20

	jobs := make(chan int)
	errCh := make(chan error, userCount)

	var wg sync.WaitGroup

	workers := tokenWorkers

	if workers > userCount {
		workers = userCount
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for userIndex := range jobs {
				userID := fmt.Sprintf(
					"burst-user-%d",
					userIndex+1,
				)

				token, err := generateToken(
					client,
					baseURL,
					userID,
					"user",
				)

				if err != nil {
					errCh <- fmt.Errorf(
						"%s: %w",
						userID,
						err,
					)
					continue
				}

				tokens[userIndex] = token
			}
		}()
	}

	for i := 0; i < userCount; i++ {
		jobs <- i
	}

	close(jobs)

	wg.Wait()
	close(errCh)

	for err := range errCh {
		return nil, err
	}

	return tokens, nil
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

	resp, err := postJSON(
		client,
		baseURL+"/dev/token",
		"",
		payload,
	)

	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "",
			fmt.Errorf(
				"token endpoint returned HTTP %d: %s",
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
		return "", errors.New(
			"token endpoint returned empty token",
		)
	}

	return response.Token, nil
}

func createShow(
	client *http.Client,
	baseURL string,
	adminToken string,
) (string, error) {

	payload, err := json.Marshal(
		createShowRequest{
			Name:         "Burst Test Show",
			Seats:        []string{testSeat},
			PricePaise:   testPricePaise,
			PerUserLimit: 4,
		},
	)

	if err != nil {
		return "", err
	}

	resp, err := postJSON(
		client,
		baseURL+"/shows",
		adminToken,
		payload,
	)

	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusCreated {
		return "",
			fmt.Errorf(
				"create show returned HTTP %d: %s",
				resp.StatusCode,
				string(body),
			)
	}

	var response createShowResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf(
			"decode show response: %w",
			err,
		)
	}

	if response.ID == "" {
		return "", errors.New(
			"create show returned empty ID",
		)
	}

	return response.ID, nil
}

func reserveSeat(
	client *http.Client,
	baseURL string,
	showID string,
	userID string,
	token string,
	requestID int,
) burstResult {

	payload, err := json.Marshal(
		reserveRequest{
			Seats: []string{testSeat},

			// Unique key per request.
			//
			// This test specifically measures hot-seat
			// contention, not idempotency replay.
			IdempotencyKey: fmt.Sprintf(
				"burst-%s-%d",
				userID,
				requestID,
			),
		},
	)

	if err != nil {
		return burstResult{
			err: err,
		}
	}

	url := fmt.Sprintf(
		"%s/shows/%s/reserve",
		baseURL,
		showID,
	)

	resp, err := postJSON(
		client,
		url,
		token,
		payload,
	)

	if err != nil {
		return burstResult{
			err: err,
		}
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return burstResult{
			statusCode: resp.StatusCode,
			err:        err,
		}
	}

	var errorResponse struct {
		Error string `json:"error"`
	}

	_ = json.Unmarshal(body, &errorResponse)

	return burstResult{
		statusCode: resp.StatusCode,
		reason:     errorResponse.Error,
	}
}

func getShow(
	client *http.Client,
	baseURL string,
	showID string,
) (*showResponse, error) {

	url := fmt.Sprintf(
		"%s/shows/%s",
		baseURL,
		showID,
	)

	req, err := http.NewRequest(
		http.MethodGet,
		url,
		nil,
	)

	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil,
			fmt.Errorf(
				"get show returned HTTP %d: %s",
				resp.StatusCode,
				string(body),
			)
	}

	var response showResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf(
			"decode show response: %w",
			err,
		)
	}

	return &response, nil
}

func postJSON(
	client *http.Client,
	url string,
	token string,
	payload []byte,
) (*http.Response, error) {

	req, err := http.NewRequest(
		http.MethodPost,
		url,
		bytes.NewReader(payload),
	)

	if err != nil {
		return nil, err
	}

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	if token != "" {
		req.Header.Set(
			"Authorization",
			"Bearer "+token,
		)
	}

	return client.Do(req)
}

func newHTTPClient(workers int) *http.Client {

	transport := &http.Transport{
		MaxIdleConns:        workers,
		MaxIdleConnsPerHost: workers,
		MaxConnsPerHost:     workers,

		IdleConnTimeout: 30 * time.Second,

		// Important for the 20k test.
		//
		// The server may legitimately take a while under
		// extreme contention, so don't use a tiny timeout.
		ResponseHeaderTimeout: 2 * time.Minute,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   3 * time.Minute,
	}
}

func printSummary(
	requests int,
	workers int,
	users int,
	duration time.Duration,
	confirmed int64,
	declined map[string]int64,
	fiveXX int64,
	networkErrors int64,
	show *showResponse,
) {

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("              BURST SUMMARY")
	fmt.Println("========================================")

	fmt.Printf("Requests       : %d\n", requests)
	fmt.Printf("Workers        : %d\n", workers)
	fmt.Printf("Users          : %d\n", users)
	fmt.Printf("Duration       : %s\n", duration.Round(time.Millisecond))

	fmt.Println()
	fmt.Println("Confirmed:")
	fmt.Printf("  confirmed    : %d\n", confirmed)

	fmt.Println()
	fmt.Println("Declined by reason:")

	if len(declined) == 0 {
		fmt.Println("  none")
	} else {
		for reason, count := range declined {
			fmt.Printf(
				"  %-20s : %d\n",
				reason,
				count,
			)
		}
	}

	fmt.Println()
	fmt.Printf("5xx            : %d\n", fiveXX)
	fmt.Printf("Network errors : %d\n", networkErrors)

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("          FINAL RECONCILIATION")
	fmt.Println("========================================")

	fmt.Printf("Show ID        : %s\n", show.ID)
	fmt.Printf("Total seats    : %d\n", show.Counts.Total)
	fmt.Printf("Available      : %d\n", show.Counts.Available)
	fmt.Printf("Held           : %d\n", show.Counts.Held)
	fmt.Printf("Confirmed      : %d\n", show.Counts.Confirmed)

	sum :=
		show.Counts.Available +
			show.Counts.Held +
			show.Counts.Confirmed

	fmt.Printf(
		"Invariant      : %d + %d + %d = %d\n",
		show.Counts.Available,
		show.Counts.Held,
		show.Counts.Confirmed,
		sum,
	)

	if sum == show.Counts.Total {
		fmt.Println("Reconciliation : PASS")
	} else {
		fmt.Println("Reconciliation : FAIL")
	}

	fmt.Println("========================================")
}

func getPositiveEnv(
	name string,
	fallback int,
) int {

	value := os.Getenv(name)

	if value == "" {
		return fallback
	}

	number, err := strconv.Atoi(value)

	if err != nil || number <= 0 {
		fmt.Printf(
			"invalid %s=%q, using %d\n",
			name,
			value,
			fallback,
		)

		return fallback
	}

	return number
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}

	return value
}

func fatal(message string, err error) {
	fmt.Printf("ERROR: %s: %v\n", message, err)
	os.Exit(1)
}